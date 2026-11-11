package aggregate

import (
	"testing"
	"time"

	"github.com/nlink-jp/active-lens/core/activity"
)

func TestClip(t *testing.T) {
	m := time.Minute
	// 10:00 op 30m | 10:30 pr 30m | 11:00 aw 60m | 12:00 op 30m
	in := seq(span{op, 30 * m}, span{pr, 30 * m}, span{aw, 60 * m}, span{op, 30 * m})
	base := in[0].Start
	cases := []struct {
		name         string
		since, until time.Duration
		want         []span
	}{
		{"a window holding whole segments", 30 * m, 120 * m, []span{{pr, 30 * m}, {aw, 60 * m}}},
		{"a segment straddling the start", 10 * m, 60 * m, []span{{op, 20 * m}, {pr, 30 * m}}},
		{"a segment straddling the end", 60 * m, 130 * m, []span{{aw, 60 * m}, {op, 10 * m}}},
		{"one segment covering the window", 70 * m, 100 * m, []span{{aw, 30 * m}}},
		{"a window past everything", 200 * m, 300 * m, nil},
		{"a window before everything", -60 * m, 0, nil},
		{"an empty window", 30 * m, 30 * m, nil},
		{"a window running backwards", 60 * m, 30 * m, nil},
		{"a window wider than everything", -60 * m, 300 * m, []span{{op, 30 * m}, {pr, 30 * m}, {aw, 60 * m}, {op, 30 * m}}},
	}
	for _, c := range cases {
		got := shape(Clip(in, base.Add(c.since), base.Add(c.until)))
		if !sameShape(got, c.want) {
			t.Errorf("%s:\n got  %v\n want %v", c.name, got, c.want)
		}
	}
	// The cut segment starts at the window, not at its own start.
	if got := Clip(in, base.Add(10*m), base.Add(20*m)); len(got) != 1 || !got[0].Start.Equal(base.Add(10*m)) || !got[0].End.Equal(base.Add(20*m)) {
		t.Errorf("clipped segment = %+v", got)
	}
	if in[0].Duration() != 30*m {
		t.Errorf("the caller's slice was rewritten")
	}
}

// The sample-taking totals are the segment-taking ones over Segments.
func TestTotalsFromSamplesAreTotalsOfTheirSegments(t *testing.T) {
	base := ts("2026-07-09 03:00:00") // crosses the 04:00 day boundary
	samples := stream(base, span{op, 50 * time.Minute}, span{pr, 570 * time.Second},
		span{aw, 40 * time.Minute}, span{op, 30 * time.Second}, span{aw, 30 * time.Minute}, span{op, time.Hour})
	for _, params := range []Params{p(45 * time.Second), withBrief(withIdle(p(45 * time.Second)))} {
		segs := Segments(samples, params, utc)
		if a, b := Range(samples, params, utc), RangeOf(segs, utc); a != b {
			t.Errorf("Range %+v, RangeOf %+v", a, b)
		}
		if a, b := ByHourOfDay(samples, params, utc), ByHourOfDayOf(segs, utc); a != b {
			t.Errorf("ByHourOfDay differs from ByHourOfDayOf")
		}
		a, b := ByDay(samples, params, utc), ByDayOf(segs, utc, params.DayStartHour)
		if len(a) != len(b) || len(a) != 2 {
			t.Fatalf("ByDay %d days, ByDayOf %d; want 2", len(a), len(b))
		}
		for i := range a {
			if a[i] != b[i] {
				t.Errorf("day %d: ByDay %+v, ByDayOf %+v", i, a[i], b[i])
			}
		}
	}
}

var _ = activity.Away
