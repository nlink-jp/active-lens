package cmd

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/nlink-jp/active-lens/core/activity"
	"github.com/nlink-jp/active-lens/core/aggregate"
	"github.com/nlink-jp/active-lens/core/config"
)

// wholeStream answers like the store, from one slice held in memory.
type wholeStream struct {
	samples []activity.Sample
	queries int
}

func (w *wholeStream) Query(since, until time.Time) ([]activity.Sample, error) {
	w.queries++
	var out []activity.Sample
	for _, s := range w.samples {
		if !s.TS.Before(since) && !s.TS.After(until) {
			out = append(out, s)
		}
	}
	return out, nil
}

func unbroken(from time.Time, d time.Duration) []activity.Sample {
	var out []activity.Sample
	for ts := from; ts.Before(from.Add(d)); ts = ts.Add(time.Minute) {
		out = append(out, activity.Sample{TS: ts, State: activity.Operating})
	}
	return out
}

func lookbackConfig() config.Config {
	return config.Config{SessionGapMinutes: 30, MaxGapSeconds: 300,
		BreakMinutes: 10, DayStartHour: 5}
}

// The defect this closes: a session is cut at the *second* day boundary after
// its first active segment, so a read that begins one logical day later than the
// real start cuts a day later — and so does every session after it. A Mac awake
// for more than two days is all it takes. Measured before the fix: from a window
// offset of 71.5 h onwards the window's session came out as 09-13 05:00 → 09-15
// 05:00 where the whole stream gave 09-12 05:00 → 09-14 05:00. The phases are
// swept because which ones bite depends on where the read lands in the day.
func TestSamplesForWindowDoNotDependOnWhereTheReadBegins(t *testing.T) {
	loc := time.UTC
	cfg := lookbackConfig()
	p := paramsOf(cfg)
	start := time.Date(2026, 9, 10, 6, 0, 0, 0, loc)
	// A break before the activity, so the whole stream has an anchor of its own.
	stream := append([]activity.Sample{
		{TS: start.Add(-2 * time.Hour), State: activity.Away},
		{TS: start.Add(-time.Minute), State: activity.Away},
	}, unbroken(start, 120*time.Hour)...)

	inWindow := func(all []aggregate.Session, since time.Time) []aggregate.Session {
		var out []aggregate.Session
		for _, s := range all {
			if !s.End.Before(since) {
				out = append(out, s)
			}
		}
		return out
	}
	wide := aggregate.Sessions(stream, p, loc)

	for offset := 50 * time.Hour; offset < 98*time.Hour; offset += 30 * time.Minute {
		since := start.Add(offset)
		st := &wholeStream{samples: stream}
		got, capped, err := samplesForWindow(st, since, start.Add(120*time.Hour), cfg, loc)
		if err != nil {
			t.Fatalf("offset %s: %v", offset, err)
		}
		if capped {
			t.Fatalf("offset %s: the read hit the cap on a %s stream", offset, 120*time.Hour)
		}
		want, have := inWindow(wide, since), inWindow(aggregate.Sessions(got, p, loc), since)
		if len(want) != len(have) {
			t.Fatalf("offset %s: %d sessions in the window from the whole stream, %d from the read",
				offset, len(want), len(have))
		}
		for i := range want {
			if !want[i].Start.Equal(have[i].Start) || !want[i].End.Equal(have[i].End) ||
				want[i].CarriedIn != have[i].CarriedIn || want[i].CarriedOut != have[i].CarriedOut {
				t.Fatalf("offset %s, session %d:\n whole stream %s→%s in=%v out=%v\n read         %s→%s in=%v out=%v",
					offset, i,
					want[i].Start.Format("01-02 15:04"), want[i].End.Format("01-02 15:04"), want[i].CarriedIn, want[i].CarriedOut,
					have[i].Start.Format("01-02 15:04"), have[i].End.Format("01-02 15:04"), have[i].CarriedIn, have[i].CarriedOut)
			}
		}
	}
}

func TestSamplesForWindowStopsAtTheFirstBreakItFinds(t *testing.T) {
	loc := time.UTC
	cfg := lookbackConfig()
	start := time.Date(2026, 9, 10, 6, 0, 0, 0, loc)
	stream := append([]activity.Sample{
		{TS: start.Add(-2 * time.Hour), State: activity.Away},
		{TS: start.Add(-time.Minute), State: activity.Away},
	}, unbroken(start, 72*time.Hour)...)

	st := &wholeStream{samples: stream}
	_, capped, err := samplesForWindow(st, start.Add(70*time.Hour), start.Add(72*time.Hour), cfg, loc)
	if err != nil {
		t.Fatal(err)
	}
	if capped {
		t.Error("the break is 70 h back; the read should not have hit the cap")
	}
	if st.queries < 2 {
		t.Errorf("the first read began inside activity, so it had to widen: %d queries", st.queries)
	}
}

// A machine awake for longer than the cap: the derivation is the best the cap
// allows, and the user is told rather than left with a silent guess.
func TestSamplesForWindowReportsACappedRead(t *testing.T) {
	loc := time.UTC
	cfg := lookbackConfig()
	start := time.Date(2026, 9, 1, 6, 0, 0, 0, loc)
	st := &wholeStream{samples: unbroken(start, maxLookback+48*time.Hour)}

	_, capped, err := samplesForWindow(st, start.Add(maxLookback+24*time.Hour),
		start.Add(maxLookback+48*time.Hour), cfg, loc)
	if err != nil {
		t.Fatal(err)
	}
	if !capped {
		t.Fatal("an unbroken stream longer than the cap must report the cap")
	}
	var note bytes.Buffer
	noteIfCapped(&note, capped)
	if !strings.Contains(note.String(), "awake without a break") {
		t.Errorf("the note does not say what happened: %q", note.String())
	}
	var quiet bytes.Buffer
	noteIfCapped(&quiet, false)
	if quiet.Len() != 0 {
		t.Errorf("a read that found its break must say nothing: %q", quiet.String())
	}
}
