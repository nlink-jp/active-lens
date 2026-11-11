package cmd

import (
	"testing"
	"time"

	"github.com/nlink-jp/active-lens/core/activity"
	"github.com/nlink-jp/active-lens/core/config"
)

// lay puts down 15-second samples for each part, in order, from base.
func lay(base time.Time, parts ...struct {
	d  time.Duration
	st activity.State
}) []activity.Sample {
	var out []activity.Sample
	at := base
	for _, part := range parts {
		for t := at; t.Before(at.Add(part.d)); t = t.Add(15 * time.Second) {
			out = append(out, activity.Sample{TS: t, State: part.st})
		}
		at = at.Add(part.d)
	}
	return out
}

type part = struct {
	d  time.Duration
	st activity.State
}

// dayOf derives the timeline for [since, until] the way the command does and
// returns the entry for date.
func dayOf(t *testing.T, all []activity.Sample, cfg config.Config, since, until time.Time, date string) jsonDayTimeline {
	t.Helper()
	loc := time.UTC
	samples, _, err := samplesForWindow(&wholeStream{samples: all}, since, until, cfg, loc)
	if err != nil {
		t.Fatal(err)
	}
	tl := buildTimeline(samples, since, until, paramsOf(cfg), loc)
	for _, d := range tl.Days {
		if d.Date == date {
			return d
		}
	}
	t.Fatalf("no entry for %s in %s..%s", date, tl.Since, tl.Until)
	return jsonDayTimeline{}
}

// A day's entry must not depend on the --until it was asked with. A session
// that ran past the end of its logical day used to be cut where the read
// stopped.
func TestTimeline_ADayReadsTheSameWhateverUntilIs(t *testing.T) {
	loc := time.UTC
	cfg := config.Defaults("/data") // day starts 04:00
	day := time.Date(2026, 7, 9, 4, 0, 0, 0, loc)
	next := day.AddDate(0, 0, 1)
	all := lay(day.Add(-6*time.Hour),
		part{6*time.Hour + 16*time.Hour, activity.Away}, // away until 20:00
		part{9 * time.Hour, activity.Operating},         // 20:00 → 05:00, across the boundary
		part{30 * time.Minute, activity.Away},           // a break after the boundary
		part{30 * time.Minute, activity.Operating},      // → 06:00
		part{8 * time.Hour, activity.Away},
		part{2 * time.Hour, activity.Operating}, // the next day's own work
		part{6 * time.Hour, activity.Away},
	)
	alone := dayOf(t, all, cfg, day, next, "2026-07-09")
	wider := dayOf(t, all, cfg, day, next.AddDate(0, 0, 1), "2026-07-09")
	if len(alone.Breaks) != 1 || len(wider.Breaks) != 1 || alone.Breaks[0] != wider.Breaks[0] {
		t.Errorf("breaks: asked alone %+v, asked with the next day %+v; want the one 05:00 break in both",
			alone.Breaks, wider.Breaks)
	}
	if alone.WorkEndUnix != wider.WorkEndUnix || alone.ActiveSeconds != wider.ActiveSeconds || len(alone.Breaks) != len(wider.Breaks) {
		t.Errorf("asked alone: end %d active %d breaks %d; asked with the next day: end %d active %d breaks %d",
			alone.WorkEndUnix, alone.ActiveSeconds, len(alone.Breaks),
			wider.WorkEndUnix, wider.ActiveSeconds, len(wider.Breaks))
	}
	if want := next.Add(2 * time.Hour).Unix(); alone.WorkEndUnix != want {
		t.Errorf("the session ends at %d, want 06:00 the next morning (%d)", alone.WorkEndUnix, want)
	}

	// A session that began after `until` is not this window's, and the sample
	// count still describes [since, until).
	samples, _, _ := samplesForWindow(&wholeStream{samples: all}, day, next, cfg, loc)
	tl := buildTimeline(samples, day, next, paramsOf(cfg), loc)
	if len(tl.Days) != 1 || tl.Days[0].Date != "2026-07-09" {
		t.Errorf("days emitted: %d, first %q", len(tl.Days), tl.Days[0].Date)
	}
	if want := countInRange(all, day, next); tl.SampleCount != want {
		t.Errorf("sample_count = %d, want %d", tl.SampleCount, want)
	}
}

// Thirty seconds at the machine five minutes before the day ends, then a long
// absence: read to the end of the day only, the away after it was too short to
// see as a break, and the touch stood.
func TestTimeline_ATouchNearTheEndOfTheWindowIsStillAVisit(t *testing.T) {
	loc := time.UTC
	cfg := config.Defaults("/data")
	day := time.Date(2026, 7, 9, 4, 0, 0, 0, loc)
	next := day.AddDate(0, 0, 1)
	all := lay(day.Add(-6*time.Hour),
		part{6*time.Hour + 5*time.Hour, activity.Away},
		part{2 * time.Hour, activity.Operating}, // 09:00 → 11:00
		part{16*time.Hour + 55*time.Minute, activity.Away},
		part{30 * time.Second, activity.Operating}, // 03:55 the next morning
		part{5 * time.Hour, activity.Away},
		part{time.Hour, activity.Operating},
	)
	for _, until := range []time.Time{next, next.AddDate(0, 0, 1)} {
		d := dayOf(t, all, cfg, day, until, "2026-07-09")
		if want := day.Add(7 * time.Hour).Unix(); d.WorkEndUnix != want || len(d.Breaks) != 0 {
			t.Errorf("until %s: work ends %d with %d breaks; want %d (11:00) and none",
				until.Format("01-02 15:04"), d.WorkEndUnix, len(d.Breaks), want)
		}
	}
	if tl := buildTimeline(all, day, next, paramsOf(cfg), loc); tl.BriefVisitSeconds != 120 {
		t.Errorf("brief_visit_seconds = %d, want 120", tl.BriefVisitSeconds)
	}
}

// Reading from the middle of the anchoring absence gives the sessions the whole
// stream gives, with a visit right after the anchor.
func TestSamplesForWindow_AVisitAfterTheAnchorReadsTheSame(t *testing.T) {
	loc := time.UTC
	cfg := config.Defaults("/data")
	base := time.Date(2026, 7, 9, 0, 0, 0, 0, loc)
	all := lay(base,
		part{time.Hour, activity.Operating},
		part{9 * time.Hour, activity.Away}, // the anchor
		part{75 * time.Second, activity.Operating},
		part{2 * time.Hour, activity.Away},
		part{3 * time.Hour, activity.Operating},
		part{5 * time.Hour, activity.Away},
	)
	since, until := base.Add(4*time.Hour), base.Add(20*time.Hour)
	whole := buildTimeline(all, since, until, paramsOf(cfg), loc)
	samples, _, err := samplesForWindow(&wholeStream{samples: all[len(all)/8:]}, since, until, cfg, loc)
	if err != nil {
		t.Fatal(err)
	}
	read := buildTimeline(samples, since, until, paramsOf(cfg), loc)
	for i := range whole.Days {
		w, r := whole.Days[i], read.Days[i]
		if w.WorkStartUnix != r.WorkStartUnix || w.WorkEndUnix != r.WorkEndUnix || w.ActiveSeconds != r.ActiveSeconds {
			t.Errorf("%s: whole %d→%d active %d; read %d→%d active %d", w.Date,
				w.WorkStartUnix, w.WorkEndUnix, w.ActiveSeconds, r.WorkStartUnix, r.WorkEndUnix, r.ActiveSeconds)
		}
	}
	// The 75-second visit opens nothing: the day's work starts at 12:01:15.
	for _, d := range whole.Days {
		if d.HasWork && d.WorkStartUnix != base.Add(12*time.Hour+75*time.Second).Unix() {
			t.Errorf("%s: work starts at %d; the visit should not open the session", d.Date, d.WorkStartUnix)
		}
	}
}

// A database whose first sample is activity has no break to anchor on, and no
// wider read will find one. That is the whole record, not a machine awake for
// two weeks.
func TestSamplesForWindow_TheFirstSampleEverIsNotACappedRead(t *testing.T) {
	loc := time.UTC
	cfg := config.Defaults("/data")
	first := time.Date(2026, 7, 9, 9, 0, 0, 0, loc)
	all := lay(first, part{3 * time.Hour, activity.Operating})

	// Asked for now, on a fresh install.
	st := &wholeStream{samples: all}
	now := first.Add(3 * time.Hour)
	if _, capped, err := samplesForWindow(st, now, now, cfg, loc); err != nil || capped {
		t.Errorf("fresh install: capped=%v err=%v", capped, err)
	}
	if st.queries > 2 {
		t.Errorf("fresh install: %d queries to learn there is nothing earlier", st.queries)
	}
	// Asked for the days before recording began: the forward read reaches the
	// first samples, and that is still not a capped read.
	st = &wholeStream{samples: all}
	since := first.AddDate(0, 0, -3)
	if _, capped, _ := samplesForWindow(st, since, since.AddDate(0, 0, 2), cfg, loc); capped {
		t.Errorf("a window before recording began was reported as capped")
	}
}

// With the clock set back, samples stamped after now exist. A read for now must
// not include them: a session cannot end in the future.
func TestSamplesForWindow_DoesNotReadPastThePresent(t *testing.T) {
	loc := time.UTC
	cfg := config.Defaults("/data")
	base := time.Date(2026, 7, 9, 0, 0, 0, 0, loc)
	all := lay(base, part{6 * time.Hour, activity.Away}, part{4 * time.Hour, activity.Operating})
	now := base.Add(8 * time.Hour)
	defer func(f func() time.Time) { clockNow = f }(clockNow)
	clockNow = func() time.Time { return now }

	samples, _, err := samplesForWindow(&wholeStream{samples: all}, now, now, cfg, loc)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range samples {
		if s.TS.After(now) {
			t.Fatalf("read a sample at %s, after now (%s)", s.TS, now)
		}
	}
	// A window that ended in the past still reads ahead of its own end.
	past := base.Add(7 * time.Hour)
	samples, _, _ = samplesForWindow(&wholeStream{samples: all}, base, past, cfg, loc)
	if last := samples[len(samples)-1].TS; !last.After(past) {
		t.Errorf("a past window stopped at its own end (%s)", last)
	}
}

func TestBuildNow_AFutureSampleIsNotRecording(t *testing.T) {
	loc := time.UTC
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, loc)
	samples := []activity.Sample{
		{TS: now.Add(time.Hour), State: activity.Operating},
		{TS: now.Add(time.Hour + 15*time.Second), State: activity.Operating},
	}
	if n := buildNow(samples, now, time.Minute, paramsOf(config.Defaults("/data")), loc); n.Recording {
		t.Errorf("a sample an hour ahead of now reads as recording")
	}
}
