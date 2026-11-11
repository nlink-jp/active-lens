package cmd

import (
	"reflect"
	"testing"
	"time"

	"github.com/nlink-jp/active-lens/core/activity"
	"github.com/nlink-jp/active-lens/core/aggregate"
	"github.com/nlink-jp/active-lens/core/config"
)

// reportOf derives the report for [since, until) the way the command does.
func reportOf(t *testing.T, all []activity.Sample, cfg config.Config, since, until time.Time) jsonReport {
	t.Helper()
	samples, _, err := samplesForWindow(&wholeStream{samples: all}, since, until, cfg, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	return buildReport(samples, since, until, paramsOf(cfg), time.UTC)
}

// wholeTotals is the yardstick: the segments of the whole record, cut to the
// window. A report must say the same, however little of the record it read.
func wholeTotals(all []activity.Sample, cfg config.Config, since, until time.Time) jsonTotals {
	segs := aggregate.Clip(aggregate.Segments(all, paramsOf(cfg), time.UTC), since, until)
	return toJSONTotals(aggregate.RangeOf(segs, time.UTC))
}

func sumTotals(t jsonTotals) int64 { return t.OperatingSeconds + t.PresentSeconds + t.AwaySeconds }

// The edges of a window used to be judged without what lay outside it.
func TestReport_AgreesWithTheWholeRecordAtTheEdgesOfItsWindow(t *testing.T) {
	cfg := config.Defaults("/data") // day starts 04:00
	day := time.Date(2026, 7, 9, 4, 0, 0, 0, time.UTC)
	next := day.AddDate(0, 0, 1)
	op, pr, aw := activity.Operating, activity.Present, activity.Away

	cases := []struct {
		name  string
		from  time.Time
		parts []part
		check func(t *testing.T, r jsonReport)
	}{
		{"a touch three minutes into the day, long absences around it",
			day.Add(-8 * time.Hour),
			[]part{{time.Hour, op}, {7*time.Hour + 3*time.Minute, aw}, {30 * time.Second, op}, {6 * time.Hour, aw}, {2 * time.Hour, op}, {20 * time.Hour, aw}},
			func(t *testing.T, r jsonReport) {
				if r.Total.OperatingSeconds != 2*3600 {
					t.Errorf("operating = %ds; the touch is not work, the two hours are", r.Total.OperatingSeconds)
				}
			}},
		{"twelve minutes of no input straddling the start, then away",
			day.Add(-8 * time.Hour),
			[]part{{7*time.Hour + 54*time.Minute, op}, {12 * time.Minute, pr}, {5 * time.Hour, aw}, {2 * time.Hour, op}, {20 * time.Hour, aw}},
			func(t *testing.T, r jsonReport) {
				if r.Total.PresentSeconds != 6*60 {
					t.Errorf("present = %ds; the six minutes inside the day stay present (the idle is over the limit)", r.Total.PresentSeconds)
				}
			}},
		{"a touch five minutes before the end, then a long absence",
			day.Add(-8 * time.Hour),
			[]part{{8*time.Hour + 5*time.Hour, aw}, {2 * time.Hour, op}, {16*time.Hour + 55*time.Minute, aw}, {30 * time.Second, op}, {6 * time.Hour, aw}, {time.Hour, op}},
			func(t *testing.T, r jsonReport) {
				if r.Total.OperatingSeconds != 2*3600 {
					t.Errorf("operating = %ds; the touch before the end is not work", r.Total.OperatingSeconds)
				}
			}},
		{"no input up to the end of the day, away from then on",
			day.Add(-8 * time.Hour),
			[]part{{8*time.Hour + 20*time.Hour, aw}, {3*time.Hour + 50*time.Minute + 30*time.Second, op}, {570 * time.Second, pr}, {6 * time.Hour, aw}, {time.Hour, op}},
			func(t *testing.T, r jsonReport) {
				if r.Total.PresentSeconds != 0 {
					t.Errorf("present = %ds; the idle before the away is the away", r.Total.PresentSeconds)
				}
			}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			all := lay(c.from, c.parts...)
			r := reportOf(t, all, cfg, day, next)
			if want := wholeTotals(all, cfg, day, next); r.Total != want {
				t.Errorf("report %+v, the whole record says %+v", r.Total, want)
			}
			if got := sumTotals(r.Total); got != 24*3600 {
				t.Errorf("the day sums to %ds, want 24h", got)
			}
			c.check(t, r)
		})
	}
}

// A sleep across the start of the window is away from the start, and the
// window is covered to its last second.
func TestReport_CountsTheWholeWindowAcrossASleep(t *testing.T) {
	cfg := config.Defaults("/data")
	day := time.Date(2026, 7, 9, 4, 0, 0, 0, time.UTC)
	// Samples stop at 01:00 and resume at 08:00: no sample in 04:00–08:00.
	all := append(lay(day.Add(-6*time.Hour), part{3 * time.Hour, activity.Operating}),
		lay(day.Add(4*time.Hour), part{2 * time.Hour, activity.Operating}, part{30 * time.Hour, activity.Away})...)
	r := reportOf(t, all, cfg, day, day.AddDate(0, 0, 1))
	if r.Total.OperatingSeconds != 2*3600 || r.Total.AwaySeconds != 22*3600 {
		t.Errorf("totals = %+v; want 2h operating and 22h away (4h of it before the first sample of the day)", r.Total)
	}
}

// The machine was off for a week. Each day inside the gap is a day of away,
// whatever window it is asked with.
func TestReport_ADayInsideALongGapIsAwayWhateverTheWindow(t *testing.T) {
	cfg := config.Defaults("/data")
	loc := time.UTC
	start := time.Date(2026, 7, 3, 9, 0, 0, 0, loc)
	all := append(lay(start, part{10 * time.Hour, activity.Operating}),
		lay(time.Date(2026, 7, 10, 9, 0, 0, 0, loc), part{3 * time.Hour, activity.Operating})...)
	d := func(day int) time.Time { return time.Date(2026, 7, day, 4, 0, 0, 0, loc) }

	alone := reportOf(t, all, cfg, d(6), d(7))
	if alone.Total.AwaySeconds != 24*3600 || alone.Total.OperatingSeconds != 0 || alone.SampleCount != 0 {
		t.Errorf("07-06 alone = %+v (%d samples); want 24h away and no samples", alone.Total, alone.SampleCount)
	}
	wide := reportOf(t, all, cfg, d(4), d(9))
	var inWide jsonTotals
	for _, day := range wide.Days {
		if day.Date == "2026-07-06" {
			inWide = day.Totals
		}
	}
	if inWide != alone.Total {
		t.Errorf("07-06 asked alone %+v, asked inside 07-04..07-08 %+v", alone.Total, inWide)
	}
	// The day the record resumes: away from 04:00 to 09:00, then the work.
	back := reportOf(t, all, cfg, d(10), d(11))
	// The last sample has no interval after it, so the work is 15s short of 3h.
	if back.Total.OperatingSeconds != 3*3600-15 || back.Total.AwaySeconds != 5*3600 {
		t.Errorf("07-10 = %+v; want 3h less a sample of operating and the 5h before it away (nothing is recorded after 12:00)", back.Total)
	}
	// Before the first sample ever, and after the last, nothing is counted.
	if before := reportOf(t, all, cfg, d(1), d(2)); sumTotals(before.Total) != 0 {
		t.Errorf("a day before recording began = %+v, want nothing", before.Total)
	}
	if after := reportOf(t, all, cfg, d(12), d(13)); sumTotals(after.Total) != 0 {
		t.Errorf("a day after the last sample = %+v, want nothing", after.Total)
	}
	if got := noSamplesHint(alone); got == "" || got == noSamplesHint(jsonReport{}) {
		t.Errorf("a day of away with no samples needs its own hint, got %q", got)
	}
}

// A sample exactly at `until` belongs to the next window, as in `timeline`.
func TestReport_SampleCountIsHalfOpen(t *testing.T) {
	cfg := config.Defaults("/data")
	day := time.Date(2026, 7, 9, 4, 0, 0, 0, time.UTC)
	next := day.AddDate(0, 0, 1)
	all := []activity.Sample{
		{TS: day, State: activity.Operating},
		{TS: day.Add(15 * time.Second), State: activity.Operating},
		{TS: next, State: activity.Operating},
		{TS: next.Add(15 * time.Second), State: activity.Operating},
	}
	if r := reportOf(t, all, cfg, day, next); r.SampleCount != 2 {
		t.Errorf("sample_count = %d, want 2 (the one at until is the next window's)", r.SampleCount)
	}
}

// With `until` after now — `--until <today>` — the read still stops at now.
func TestSamplesForWindow_StopsAtNowWhenUntilIsLater(t *testing.T) {
	cfg := config.Defaults("/data")
	base := time.Date(2026, 7, 9, 0, 0, 0, 0, time.UTC)
	all := lay(base, part{6 * time.Hour, activity.Away}, part{30 * time.Hour, activity.Operating})
	now := base.Add(8 * time.Hour)
	defer func(f func() time.Time) { clockNow = f }(clockNow)
	clockNow = func() time.Time { return now }
	samples, _, err := samplesForWindow(&wholeStream{samples: all}, base, base.Add(28*time.Hour), cfg, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range samples {
		if s.TS.After(now) {
			t.Fatalf("read a sample at %s, after now (%s)", s.TS, now)
		}
	}
}

// What a command derives from its read must be what the whole record gives:
// that is the point of reading past the window and of the neighbours. Streams
// are built from a seed — work, breaks, touches, idles and silences of up to
// eleven days — and each window's timeline days and report are compared with
// the same derivation over every sample.
func TestReadsGiveWhatTheWholeRecordGives(t *testing.T) {
	loc := time.UTC
	op, pr, aw := activity.Operating, activity.Present, activity.Away
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, loc)
	for _, boundary := range []string{config.DayBoundarySession, config.DayBoundaryStrict} {
		cfg := config.Defaults("/data")
		cfg.DayBoundary = boundary
		for seed := uint64(1); seed <= 40; seed++ {
			r := seed * 2654435761
			next := func(n uint64) uint64 { r = r*6364136223846793005 + 1442695040888963407; return (r >> 33) % n }
			var all []activity.Sample
			at := base
			for at.Before(base.AddDate(0, 0, 30)) {
				var p part
				switch next(10) {
				case 0, 1, 2:
					p = part{time.Duration(10+next(240)) * time.Minute, op}
				case 3:
					p = part{time.Duration(1+next(14)) * time.Minute, pr}
				case 4, 5:
					p = part{time.Duration(1+next(40)) * time.Minute, aw}
				case 6:
					p = part{time.Duration(15+next(120)) * time.Second, op} // a touch
				case 7:
					p = part{time.Duration(4+next(20)) * time.Hour, aw}
				case 8:
					p = part{time.Duration(1+next(5)) * time.Hour, aw}
				default: // nothing recorded at all, for up to eleven days
					at = at.Add(time.Duration(2+next(260)) * time.Hour)
					continue
				}
				all = append(all, lay(at, p)...)
				at = at.Add(p.d)
			}
			whole := buildTimeline(all, base.AddDate(0, 0, -3), base.AddDate(0, 0, 40), paramsOf(cfg), loc)
			wholeDay := map[string]jsonDayTimeline{}
			for _, d := range whole.Days {
				wholeDay[d.Date] = d
			}
			for day := 2; day <= 28; day += 3 {
				since := time.Date(2026, 7, day, 4, 0, 0, 0, loc)
				for _, span := range []int{1, 3} {
					until := since.AddDate(0, 0, span)
					samples, capped, err := samplesForWindow(&wholeStream{samples: all}, since, until, cfg, loc)
					if err != nil || capped {
						t.Fatalf("seed %d: capped=%v err=%v", seed, capped, err)
					}
					for _, d := range buildTimeline(samples, since, until, paramsOf(cfg), loc).Days {
						if !reflect.DeepEqual(d, wholeDay[d.Date]) {
							t.Fatalf("%s seed %d, %s asked as %d day(s) from 07-%02d: the timeline day differs from the whole record's",
								boundary, seed, d.Date, span, day)
						}
					}
					rep := buildReport(samples, since, until, paramsOf(cfg), loc)
					if want := wholeTotals(all, cfg, since, until); rep.Total != want {
						t.Fatalf("%s seed %d, report 07-%02d + %d day(s): %+v, the whole record says %+v",
							boundary, seed, day, span, rep.Total, want)
					}
				}
			}
		}
	}
}

// A laptop shut on Friday and opened on Monday: the read for Friday ends inside
// the silence. Its last sample is credited as the whole record credits it, and
// the away after it is seen, so the idle before it is the absence.
func TestTimeline_AReadEndingInASilenceSeesTheAwayAfterIt(t *testing.T) {
	cfg := config.Defaults("/data")
	loc := time.UTC
	fri := time.Date(2026, 7, 10, 4, 0, 0, 0, loc)
	all := append(lay(fri.Add(-12*time.Hour), part{17 * time.Hour, activity.Away},
		part{8 * time.Hour, activity.Operating}, part{4 * time.Minute, activity.Present}), // lid shut at 17:04
		lay(fri.Add(77*time.Hour), part{2 * time.Hour, activity.Operating}, part{30 * time.Hour, activity.Away})...)
	got := dayOf(t, all, cfg, fri, fri.AddDate(0, 0, 1), "2026-07-10")
	whole := dayOf(t, all, cfg, fri.AddDate(0, 0, -2), fri.AddDate(0, 0, 6), "2026-07-10")
	if !reflect.DeepEqual(got, whole) {
		t.Errorf("Friday asked alone differs from Friday in the whole week:\n alone %+v\n whole %+v", got, whole)
	}
	if want := fri.Add(13 * time.Hour).Unix(); got.WorkEndUnix != want || got.PresentSeconds != 0 {
		t.Errorf("work ends %d with %ds present; want 17:00 (%d) and none — the four idle minutes were the lid closing",
			got.WorkEndUnix, got.PresentSeconds, want)
	}
}

// After two weeks off, a thirty-second touch, a quarter of an hour away, then
// work: the touch is a visit, in `timeline` and in `now` alike.
func TestNowAndTimeline_ATouchAfterALongSilenceIsAVisit(t *testing.T) {
	cfg := config.Defaults("/data")
	loc := time.UTC
	back := time.Date(2026, 7, 15, 9, 0, 0, 0, loc)
	all := append(lay(time.Date(2026, 7, 1, 9, 0, 0, 0, loc), part{2 * time.Hour, activity.Operating}),
		lay(back, part{30 * time.Second, activity.Operating}, part{15 * time.Minute, activity.Away},
			part{3 * time.Hour, activity.Operating}, part{10 * time.Minute, activity.Away})...)
	workStart := back.Add(15*time.Minute + 30*time.Second).Unix()

	d := dayOf(t, all, cfg, time.Date(2026, 7, 15, 4, 0, 0, 0, loc), time.Date(2026, 7, 16, 4, 0, 0, 0, loc), "2026-07-15")
	if d.WorkStartUnix != workStart || len(d.Breaks) != 0 {
		t.Errorf("timeline: work starts %d with %d breaks; want %d and none", d.WorkStartUnix, len(d.Breaks), workStart)
	}
	now := back.Add(4 * time.Hour)
	defer func(f func() time.Time) { clockNow = f }(clockNow)
	clockNow = func() time.Time { return now }
	samples, _, err := samplesForWindow(&wholeStream{samples: all}, now, now, cfg, loc)
	if err != nil {
		t.Fatal(err)
	}
	if n := buildNow(samples, now, time.Minute, paramsOf(cfg), loc); n.Session == nil || n.Session.StartUnix != workStart {
		t.Errorf("now: session %+v; want it to start at %d, as the timeline's does", n.Session, workStart)
	}
}

// The daemon stopped three days ago. `now` has nothing in reach, and must not
// report the state of a marker it added itself.
func TestNow_WithNothingInReachReportsNoState(t *testing.T) {
	cfg := config.Defaults("/data")
	loc := time.UTC
	last := time.Date(2026, 7, 9, 9, 0, 0, 0, loc)
	all := lay(last.Add(-3*time.Hour), part{3 * time.Hour, activity.Operating})
	now := last.Add(72 * time.Hour)
	defer func(f func() time.Time) { clockNow = f }(clockNow)
	clockNow = func() time.Time { return now }
	samples, _, err := samplesForWindow(&wholeStream{samples: all}, now, now, cfg, loc)
	if err != nil {
		t.Fatal(err)
	}
	n := buildNow(samples, now, time.Minute, paramsOf(cfg), loc)
	if n.State != "" || n.Session != nil || n.Recording {
		t.Errorf("now = state %q, session %+v, recording %v; want nothing — no sample was read", n.State, n.Session, n.Recording)
	}
}

// The record begins part-way through the window, and ends a little before now.
func TestReport_CountsNothingOutsideTheRecord(t *testing.T) {
	cfg := config.Defaults("/data")
	loc := time.UTC
	day := time.Date(2026, 7, 9, 4, 0, 0, 0, loc)
	first := day.Add(6 * time.Hour) // the first sample ever, at 10:00
	all := lay(first, part{2 * time.Hour, activity.Operating}, part{time.Hour, activity.Away})
	lastSample := all[len(all)-1].TS

	// A whole day, the record starting inside it.
	r := reportOf(t, all, cfg, day, day.AddDate(0, 0, 1))
	if got, want := sumTotals(r.Total), int64(lastSample.Sub(first).Seconds()); got != want {
		t.Errorf("the day totals %ds; want %ds — only first sample to last, nothing before 10:00", got, want)
	}
	// `today`: until is now, a few minutes after the last sample.
	now := lastSample.Add(7 * time.Minute)
	defer func(f func() time.Time) { clockNow = f }(clockNow)
	clockNow = func() time.Time { return now }
	r = reportOf(t, all, cfg, day, now)
	if got, want := sumTotals(r.Total), int64(lastSample.Sub(first).Seconds()); got != want {
		t.Errorf("until now totals %ds; want %ds — nothing after the last sample", got, want)
	}
}

func TestResolveRange_UntilMustComeAfterSince(t *testing.T) {
	_, _, err := resolveRange("2026-07-10", "2026-07-09", time.UTC, 4)
	if err == nil {
		t.Fatal("--until the day before --since gave an empty window instead of an error")
	}
	// The message names the days that were asked for.
	if want := "--until (2026-07-09) is before --since (2026-07-10)"; err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}
	if _, _, err := resolveRange("2026-07-10", "2026-07-10", time.UTC, 4); err != nil {
		t.Errorf("a single day is a valid range: %v", err)
	}
}
