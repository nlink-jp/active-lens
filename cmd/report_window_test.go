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

// Adding the neighbours must not move the work log. Each stream is derived
// from the command's read with them and from the same read without.
func TestSamplesForWindow_NeighboursDoNotMoveTheWorkLog(t *testing.T) {
	cfg := config.Defaults("/data")
	loc := time.UTC
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, loc)
	op, pr, aw := activity.Operating, activity.Present, activity.Away
	streams := map[string][]activity.Sample{
		"anchored, continuous": lay(base, part{time.Hour, op}, part{9 * time.Hour, aw}, part{3 * time.Hour, op},
			part{30 * time.Minute, aw}, part{2 * time.Hour, pr}, part{200 * time.Hour, aw}, part{3 * time.Hour, op}, part{40 * time.Hour, aw}),
		"a week with no samples": append(lay(base, part{10 * time.Hour, op}),
			lay(base.AddDate(0, 0, 8), part{5 * time.Hour, op}, part{20 * time.Minute, aw}, part{2 * time.Hour, op}, part{30 * time.Hour, aw})...),
		"begins in activity":    lay(base.AddDate(0, 0, 7), part{30 * time.Hour, op}, part{20 * time.Hour, aw}, part{4 * time.Hour, op}),
		"awake for twenty days": lay(base.AddDate(0, 0, -12), part{20 * 24 * time.Hour, op}, part{40 * time.Hour, aw}),
	}
	for name, all := range streams {
		for day := 5; day <= 11; day++ {
			since := time.Date(2026, 7, day, 4, 0, 0, 0, loc)
			until := since.AddDate(0, 0, 2)
			with, _, err := samplesForWindow(&wholeStream{samples: all}, since, until, cfg, loc)
			if err != nil {
				t.Fatal(err)
			}
			bare, _, _ := samplesForWindow(&wholeStream{samples: all, noNeighbours: true}, since, until, cfg, loc)
			if name == "a week with no samples" && day == 5 && len(with) == len(bare) {
				t.Fatalf("the scenario no longer exercises a neighbour (both reads hold %d samples)", len(with))
			}
			a := buildTimeline(with, since, until, paramsOf(cfg), loc)
			b := buildTimeline(bare, since, until, paramsOf(cfg), loc)
			if !reflect.DeepEqual(a.Days, b.Days) {
				t.Errorf("%s, window 07-%02d: the timeline differs with the neighbours added", name, day)
			}
			now := until
			if an, bn := buildNow(with, now, time.Minute, paramsOf(cfg), loc), buildNow(bare, now, time.Minute, paramsOf(cfg), loc); !reflect.DeepEqual(an.Session, bn.Session) {
				t.Errorf("%s, window 07-%02d: the now-session differs with the neighbours added", name, day)
			}
		}
	}
}
