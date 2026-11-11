package aggregate

import (
	"testing"
	"time"

	"github.com/nlink-jp/active-lens/core/activity"
)

func TestDropBriefVisits(t *testing.T) {
	const brief, brk = 2 * time.Minute, 10 * time.Minute
	m, s := time.Minute, time.Second
	cases := []struct {
		name string
		in   []span
		want []span
	}{
		{"a touch in the middle of an absence is the absence",
			[]span{{aw, 30 * m}, {op, 30 * s}, {aw, 30 * m}},
			[]span{{aw, 60*m + 30*s}}},
		{"the limit is inclusive and a run is measured end to end",
			[]span{{aw, 30 * m}, {op, 60 * s}, {pr, 60 * s}, {aw, 30 * m}},
			[]span{{aw, 62 * m}}},
		{"a second past the limit is work",
			[]span{{aw, 30 * m}, {op, 61 * s}, {pr, 60 * s}, {aw, 30 * m}},
			[]span{{aw, 30 * m}, {op, 61 * s}, {pr, 60 * s}, {aw, 30 * m}}},
		{"the away before must be a break",
			[]span{{op, 5 * m}, {aw, 10*m - s}, {op, 30 * s}, {aw, 30 * m}},
			[]span{{op, 5 * m}, {aw, 10*m - s}, {op, 30 * s}, {aw, 30 * m}}},
		{"the away after must be a break",
			[]span{{aw, 30 * m}, {op, 30 * s}, {aw, 10*m - s}, {op, 5 * m}},
			[]span{{aw, 30 * m}, {op, 30 * s}, {aw, 10*m - s}, {op, 5 * m}}},
		{"exactly a break on both sides qualifies",
			[]span{{op, 5 * m}, {aw, 10 * m}, {op, 30 * s}, {aw, 10 * m}, {op, 5 * m}},
			[]span{{op, 5 * m}, {aw, 20*m + 30*s}, {op, 5 * m}}},
		{"a run at the head has an unseen side",
			[]span{{op, 30 * s}, {aw, 30 * m}, {op, 5 * m}},
			[]span{{op, 30 * s}, {aw, 30 * m}, {op, 5 * m}}},
		{"a run at the tail has an unseen side",
			[]span{{op, 5 * m}, {aw, 30 * m}, {op, 30 * s}},
			[]span{{op, 5 * m}, {aw, 30 * m}, {op, 30 * s}}},
		{"two touches with a short away between them: neither, in one pass",
			[]span{{aw, 30 * m}, {op, 30 * s}, {aw, 5 * m}, {op, 30 * s}, {aw, 30 * m}},
			[]span{{aw, 30 * m}, {op, 30 * s}, {aw, 5 * m}, {op, 30 * s}, {aw, 30 * m}}},
		{"work is untouched",
			[]span{{aw, 30 * m}, {op, 20 * m}, {aw, 30 * m}},
			[]span{{aw, 30 * m}, {op, 20 * m}, {aw, 30 * m}}},
	}
	for _, c := range cases {
		got := shape(dropBriefVisits(seq(c.in...), brief, brk))
		if !sameShape(got, c.want) {
			t.Errorf("%s:\n got  %v\n want %v", c.name, got, c.want)
		}
	}
}

func TestDropBriefVisits_OffAndUntouchedInput(t *testing.T) {
	in := seq(span{aw, 30 * time.Minute}, span{op, 30 * time.Second}, span{aw, 30 * time.Minute})
	for name, got := range map[string][]Segment{
		"brief = 0":          dropBriefVisits(in, 0, 10*time.Minute),
		"breakThreshold = 0": dropBriefVisits(in, 2*time.Minute, 0),
	} {
		if !sameShape(shape(got), shape(in)) {
			t.Errorf("%s rewrote the segments: %v", name, shape(got))
		}
	}
	dropBriefVisits(in, 2*time.Minute, 10*time.Minute)
	if in[1].State != op {
		t.Errorf("the caller's slice was rewritten")
	}
}

func TestDropBriefVisits_NeedsContiguity(t *testing.T) {
	in := seq(span{aw, 30 * time.Minute}, span{op, 30 * time.Second}, span{aw, 30 * time.Minute})
	in[2].Start = in[2].Start.Add(time.Second) // a hole after the touch
	if got := dropBriefVisits(in, 2*time.Minute, 10*time.Minute); got[1].State != op {
		t.Errorf("a touch not adjoining the away after it was rewritten")
	}
}

// stream lays down 15-second samples for each (state, duration) part.
func stream(base time.Time, parts ...span) []activity.Sample {
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

func withBrief(params Params) Params {
	params.BriefVisit = 2 * time.Minute
	return params
}

// A touch, then the idle until the display sleeps: the idle rule takes the idle
// first, and what is left is short enough to be a visit.
func TestSegments_IdleRuleRunsBeforeTheVisitRule(t *testing.T) {
	base := ts("2026-07-09 10:00:00")
	samples := stream(base, span{aw, 30 * time.Minute}, span{op, 30 * time.Second},
		span{pr, 570 * time.Second}, span{aw, 30 * time.Minute}, span{op, 20 * time.Minute})
	params := withBrief(withIdle(p(45 * time.Second)))
	segs := Segments(samples, params, utc)
	if len(segs) < 2 || segs[0].State != aw || segs[0].Duration() != 70*time.Minute || segs[1].State != op {
		t.Errorf("want one 70m away then the work, got %v", shape(segs))
	}
	// With the idle left in place the run is ten minutes long: not a visit.
	only := withBrief(p(45 * time.Second))
	if segs := Segments(samples, only, utc); segs[1].State != op {
		t.Errorf("without the idle rule the touch should stand: %v", shape(segs))
	}
}

// Work, three hours away, thirty seconds at the machine, six hours away, work.
func TestSessions_ATouchDoesNotStretchTheSession(t *testing.T) {
	base := ts("2026-07-09 08:00:00")
	samples := stream(base, span{op, time.Hour}, span{aw, 3 * time.Hour},
		span{op, 30 * time.Second}, span{aw, 6 * time.Hour}, span{op, time.Hour})

	off := Sessions(samples, p(45*time.Second), utc)
	if len(off) != 2 || len(off[0].Breaks) != 1 || !off[0].End.Equal(base.Add(4*time.Hour+30*time.Second)) {
		t.Fatalf("without the rule the first session runs to the touch across a 3h break: %+v", off)
	}
	on := Sessions(samples, withBrief(p(45*time.Second)), utc)
	if len(on) != 2 || len(on[0].Breaks) != 0 || !on[0].End.Equal(base.Add(time.Hour)) {
		t.Fatalf("with the rule the first session ends with its work and has no break: %+v", on)
	}
	if !on[1].Start.Equal(base.Add(10*time.Hour + 30*time.Second)) {
		t.Errorf("the second session starts at %s", on[1].Start)
	}
}

func TestSessions_AVisitAloneIsNotASession(t *testing.T) {
	base := ts("2026-07-09 00:00:00")
	samples := stream(base, span{aw, 9 * time.Hour}, span{op, 75 * time.Second},
		span{aw, 12 * time.Hour}, span{op, time.Hour})
	if ss := Sessions(samples, p(45*time.Second), utc); len(ss) != 2 {
		t.Fatalf("without the rule the visit is a session: %d sessions", len(ss))
	}
	ss := Sessions(samples, withBrief(p(45*time.Second)), utc)
	if len(ss) != 1 || ss[0].Duration() < 59*time.Minute {
		t.Fatalf("want only the real session, got %+v", ss)
	}
}

func TestTotalsAndSegmentsAgree_WithBriefVisits(t *testing.T) {
	base := ts("2026-07-09 08:00:00")
	samples := stream(base, span{op, time.Hour}, span{aw, 3 * time.Hour},
		span{op, 30 * time.Second}, span{aw, 6 * time.Hour}, span{op, time.Hour})
	for _, params := range []Params{p(45 * time.Second), withBrief(p(45 * time.Second))} {
		var want Totals
		for _, s := range Segments(samples, params, utc) {
			want.add(s.State, s.Duration())
		}
		if got := Range(samples, params, utc); got != want {
			t.Errorf("BriefVisit=%s: Range %+v, segments sum to %+v", params.BriefVisit, got, want)
		}
	}
	off, on := Range(samples, p(45*time.Second), utc), Range(samples, withBrief(p(45*time.Second)), utc)
	if off.Operating-on.Operating != 30*time.Second || on.Total() != off.Total() {
		t.Errorf("operating fell by %s, totals %s vs %s", off.Operating-on.Operating, on.Total(), off.Total())
	}
}
