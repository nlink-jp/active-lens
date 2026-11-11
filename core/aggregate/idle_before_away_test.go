package aggregate

import (
	"testing"
	"time"

	"github.com/nlink-jp/active-lens/core/activity"
)

// seq builds contiguous segments from (state, duration) pairs starting at base.
type span struct {
	st activity.State
	d  time.Duration
}

func seq(spans ...span) []Segment {
	at := ts("2026-07-09 10:00:00")
	var out []Segment
	for _, s := range spans {
		out = append(out, Segment{Start: at, End: at.Add(s.d), State: s.st})
		at = at.Add(s.d)
	}
	return out
}

func shape(segs []Segment) []span {
	var out []span
	for _, s := range segs {
		out = append(out, span{s.State, s.Duration()})
	}
	return out
}

func sameShape(a, b []span) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

const (
	op = activity.Operating
	pr = activity.Present
	aw = activity.Away
)

func TestReattributeIdleBeforeAway(t *testing.T) {
	const limit, confirm = 10 * time.Minute, 60 * time.Second
	m := time.Minute
	cases := []struct {
		name string
		in   []span
		want []span
	}{
		{"idle then a lasting away becomes one away",
			[]span{{op, 30 * m}, {pr, 570 * time.Second}, {aw, 5 * m}},
			[]span{{op, 30 * m}, {aw, 570*time.Second + 5*m}}},
		{"away too short to confirm: someone woke the display",
			[]span{{op, 30 * m}, {pr, 570 * time.Second}, {aw, 30 * time.Second}, {op, m}},
			[]span{{op, 30 * m}, {pr, 570 * time.Second}, {aw, 30 * time.Second}, {op, m}}},
		{"idle longer than the limit: the display was held awake",
			[]span{{op, 30 * m}, {pr, 12 * m}, {aw, 60 * m}},
			[]span{{op, 30 * m}, {pr, 12 * m}, {aw, 60 * m}}},
		{"both bounds are inclusive",
			[]span{{op, m}, {pr, 10 * m}, {aw, 60 * time.Second}},
			[]span{{op, m}, {aw, 11 * m}}},
		{"just past either bound is left alone",
			[]span{{op, m}, {pr, 10*m + time.Second}, {aw, 5 * m}, {op, m}, {pr, 5 * m}, {aw, 59 * time.Second}, {op, m}},
			[]span{{op, m}, {pr, 10*m + time.Second}, {aw, 5 * m}, {op, m}, {pr, 5 * m}, {aw, 59 * time.Second}, {op, m}}},
		{"operating straight into away is untouched",
			[]span{{op, 30 * m}, {aw, 20 * m}},
			[]span{{op, 30 * m}, {aw, 20 * m}}},
		{"idle followed by input is untouched",
			[]span{{op, m}, {pr, 5 * m}, {op, m}},
			[]span{{op, m}, {pr, 5 * m}, {op, m}}},
		{"a display that lit with no input fuses with the aways around it",
			[]span{{aw, 60 * m}, {pr, 3 * m}, {aw, 5 * m}, {op, m}},
			[]span{{aw, 68 * m}, {op, m}}},
		{"a trailing idle has nothing after it to confirm",
			[]span{{op, m}, {pr, 5 * m}},
			[]span{{op, m}, {pr, 5 * m}}},
	}
	for _, c := range cases {
		got := shape(reattributeIdleBeforeAway(seq(c.in...), limit, confirm))
		if !sameShape(got, c.want) {
			t.Errorf("%s:\n got  %v\n want %v", c.name, got, c.want)
		}
	}
}

func TestReattributeIdleBeforeAway_ZeroLimitIsOff(t *testing.T) {
	in := seq(span{op, time.Minute}, span{pr, 9 * time.Minute}, span{aw, time.Hour})
	got := reattributeIdleBeforeAway(in, 0, 60*time.Second)
	if !sameShape(shape(got), shape(in)) {
		t.Errorf("limit 0 rewrote the segments: %v", shape(got))
	}
}

func TestReattributeIdleBeforeAway_NeedsContiguity(t *testing.T) {
	in := seq(span{pr, 5 * time.Minute}, span{aw, 10 * time.Minute})
	in[1].Start = in[1].Start.Add(time.Second) // a hole between them
	got := reattributeIdleBeforeAway(in, 10*time.Minute, 60*time.Second)
	if got[0].State != pr {
		t.Errorf("a present not touching the away was rewritten")
	}
}

func TestReattributeIdleBeforeAway_DoesNotMutateItsInput(t *testing.T) {
	in := seq(span{pr, 5 * time.Minute}, span{aw, 10 * time.Minute})
	reattributeIdleBeforeAway(in, 10*time.Minute, 60*time.Second)
	if in[0].State != pr {
		t.Errorf("the caller's slice was rewritten")
	}
}

// idleStream: operating, then no input until the display sleeps, then away.
func idleStream(idle, away time.Duration) []activity.Sample {
	base := ts("2026-07-09 10:00:00")
	var out []activity.Sample
	add := func(from time.Time, d time.Duration, st activity.State) time.Time {
		for t := from; t.Before(from.Add(d)); t = t.Add(15 * time.Second) {
			out = append(out, activity.Sample{TS: t, State: st})
		}
		return from.Add(d)
	}
	at := add(base, 30*time.Minute, op)
	at = add(at, idle, pr)
	at = add(at, away, aw)
	add(at, 10*time.Minute, op)
	return out
}

func withIdle(params Params) Params {
	params.IdleBeforeAway = 10 * time.Minute
	return params
}

// Every aggregation reads one segment list, so the totals and the list agree.
func TestTotalsAndSegmentsAgree(t *testing.T) {
	samples := idleStream(570*time.Second, 20*time.Minute)
	// A day starting at 11:00 puts the logical day boundary inside the absence,
	// so the per-day totals are summed across a split.
	split := withIdle(p(45 * time.Second))
	split.DayStartHour = 11
	for _, params := range []Params{p(45 * time.Second), withIdle(p(45 * time.Second)), split} {
		var want Totals
		for _, s := range Segments(samples, params, utc) {
			want.add(s.State, s.Duration())
		}
		if got := Range(samples, params, utc); got != want {
			t.Errorf("IdleBeforeAway=%s: Range %+v, segments sum to %+v", params.IdleBeforeAway, got, want)
		}
		var days Totals
		for _, d := range ByDay(samples, params, utc) {
			days.Operating += d.Totals.Operating
			days.Present += d.Totals.Present
			days.Away += d.Totals.Away
		}
		if params.DayStartHour == 11 && len(ByDay(samples, params, utc)) != 2 {
			t.Errorf("the stream was meant to straddle a logical day boundary")
		}
		if days != want {
			t.Errorf("IdleBeforeAway=%s: ByDay sums to %+v, want %+v", params.IdleBeforeAway, days, want)
		}
	}
}

func TestRange_IdleBeforeAwayMovesPresentToAway(t *testing.T) {
	samples := idleStream(570*time.Second, 20*time.Minute)
	off := Range(samples, p(45*time.Second), utc)
	on := Range(samples, withIdle(p(45*time.Second)), utc)
	if on.Operating != off.Operating {
		t.Errorf("operating changed: %s → %s", off.Operating, on.Operating)
	}
	if off.Present != 570*time.Second || on.Present != 0 {
		t.Errorf("present: off %s, on %s; want 9m30s and 0", off.Present, on.Present)
	}
	if on.Away-off.Away != 570*time.Second || on.Total() != off.Total() {
		t.Errorf("away grew by %s, totals %s vs %s", on.Away-off.Away, on.Total(), off.Total())
	}
}

// The session ends where input stopped, and the break is the whole absence.
func TestSessions_IdleBeforeAwayEndsWorkWhereInputStopped(t *testing.T) {
	base := ts("2026-07-09 10:00:00")
	samples := idleStream(8*time.Minute, 20*time.Minute)
	ss := Sessions(samples, withIdle(p(45*time.Second)), utc)
	if len(ss) != 1 || len(ss[0].Breaks) != 1 {
		t.Fatalf("want 1 session with 1 break, got %+v", ss)
	}
	b := ss[0].Breaks[0]
	if !b.Start.Equal(base.Add(30*time.Minute)) || b.Duration() != 28*time.Minute {
		t.Errorf("break %s for %s; want it to start when input stopped and last 28m", hhmm(b.Start), b.Duration())
	}
	if ss[0].PresentSeconds != 0 {
		t.Errorf("present %ds; the idle belonged to the absence", ss[0].PresentSeconds)
	}
}

// A short absence that the idle ahead of it pushes past the break threshold.
func TestSessions_IdleBeforeAwayCanMakeAnAbsenceABreak(t *testing.T) {
	samples := idleStream(570*time.Second, 5*time.Minute)
	if ss := Sessions(samples, p(45*time.Second), utc); len(ss) != 1 || len(ss[0].Breaks) != 0 {
		t.Fatalf("without the rule a 5m away is no break: %+v", ss)
	}
	ss := Sessions(samples, withIdle(p(45*time.Second)), utc)
	if len(ss) != 1 || len(ss[0].Breaks) != 1 {
		t.Fatalf("with the rule the 14m30s absence is a break: %+v", ss)
	}
}

// A display that lit with no input after a long absence does not open a session:
// the session begins on input, an hour later.
func TestSessions_InputlessWakeIsNotASession(t *testing.T) {
	base := ts("2026-07-09 10:00:00")
	var samples []activity.Sample
	add := func(from time.Time, d time.Duration, st activity.State) time.Time {
		for t := from; t.Before(from.Add(d)); t = t.Add(15 * time.Second) {
			samples = append(samples, activity.Sample{TS: t, State: st})
		}
		return from.Add(d)
	}
	at := add(base, 5*time.Hour, aw)
	at = add(at, 3*time.Minute, pr)
	at = add(at, time.Hour, aw)
	add(at, 10*time.Minute, op)

	if ss := Sessions(samples, p(45*time.Second), utc); len(ss) != 1 || !ss[0].Start.Equal(base.Add(5*time.Hour)) {
		t.Fatalf("without the rule the session opens on the wake: %+v", ss)
	}
	ss := Sessions(samples, withIdle(p(45*time.Second)), utc)
	if len(ss) != 1 || !ss[0].Start.Equal(base.Add(5*time.Hour+63*time.Minute)) {
		t.Fatalf("want only the session that began on input, got %+v", ss)
	}
}

// The head of a read truncates the span it lands in. Judged on re-attributed
// segments, the visible five minutes of a three-hour idle would be counted as
// away and fuse with the away after it into an absence long enough to anchor.
func TestStartsAfterABreak_IsNotFooledByATruncatedIdle(t *testing.T) {
	params := withIdle(p(45 * time.Second))
	base := ts("2026-07-09 10:00:00")
	var samples []activity.Sample
	add := func(from time.Time, d time.Duration, st activity.State) time.Time {
		for t := from; t.Before(from.Add(d)); t = t.Add(15 * time.Second) {
			samples = append(samples, activity.Sample{TS: t, State: st})
		}
		return from.Add(d)
	}
	at := add(base, 5*time.Minute, pr)                // the tail of a long idle
	at = add(at, params.SessionGap-3*time.Minute, aw) // not a session gap by itself
	add(at, 10*time.Minute, op)

	if segs := Segments(samples, params, utc); segs[0].State != aw || segs[0].Duration() < params.SessionGap {
		t.Fatalf("the scenario no longer builds the false anchor it guards against: %v", shape(segs))
	}
	if StartsAfterABreak(samples, params, utc) {
		t.Errorf("a truncated idle was taken for the absence that opens a session")
	}
}
