// Package aggregate turns a stream of raw activity samples into time totals per
// state, split across day and hour-of-day buckets. It is pure: given the same
// samples and parameters it always yields the same totals, so thresholds and the
// gap cap can be changed and re-aggregated at any time.
//
// Attribution model: for each consecutive pair a -> b, the interval
// delta = b.TS - a.TS is credited to a.State, but capped at maxGap. Any excess
// (delta - maxGap) means the daemon was not running — i.e. the system was
// asleep — and is credited to Away, located in the gap period itself. Every
// second between the first and last sample is therefore allocated to exactly one
// state. Intervals are split at local hour boundaries so multi-hour gaps land in
// the correct buckets.
package aggregate

import (
	"sort"
	"time"

	"github.com/nlink-jp/active-lens/core/activity"
)

// Totals holds accumulated durations per state.
type Totals struct {
	Operating time.Duration
	Present   time.Duration
	Away      time.Duration
}

// Total returns the sum across all three states.
func (t Totals) Total() time.Duration { return t.Operating + t.Present + t.Away }

func (t *Totals) add(st activity.State, d time.Duration) {
	switch st {
	case activity.Operating:
		t.Operating += d
	case activity.Present:
		t.Present += d
	case activity.Away:
		t.Away += d
	}
}

// DayTotals is a single logical day's totals, keyed by its date.
type DayTotals struct {
	Date   string // "YYYY-MM-DD" in the aggregation location
	Totals Totals
}

// Clip cuts a segment list down to [since, until): each segment keeps the part
// of itself inside the window, and one wholly outside is dropped. Order is
// kept and the input is not modified. Pure.
func Clip(segs []Segment, since, until time.Time) []Segment {
	if !until.After(since) {
		return nil
	}
	var out []Segment
	for _, seg := range segs {
		start, end := seg.Start, seg.End
		if start.Before(since) {
			start = since
		}
		if end.After(until) {
			end = until
		}
		if start.Before(end) {
			out = append(out, Segment{Start: start, End: end, State: seg.State})
		}
	}
	return out
}

// walkSegments replays a segment list split at local hour boundaries, invoking
// fn for each piece.
func walkSegments(segs []Segment, loc *time.Location, fn func(start time.Time, dur time.Duration, st activity.State)) {
	for _, seg := range segs {
		emitSplit(seg.Start, seg.End, seg.State, loc, fn)
	}
}

// emitSplit slices [start,end) at local hour boundaries and calls fn per piece.
// Every step moves strictly forward (see nextHour), so it terminates.
func emitSplit(start, end time.Time, st activity.State, loc *time.Location, fn func(start time.Time, dur time.Duration, st activity.State)) {
	start = start.In(loc)
	end = end.In(loc)
	for start.Before(end) {
		nb := nextHour(start)
		segEnd := end
		if nb.Before(segEnd) {
			segEnd = nb
		}
		fn(start, segEnd.Sub(start), st)
		start = segEnd
	}
}

// nextHour returns the next instant after t at which a local hour begins or the
// clocks change, in t's location.
//
// It steps forward from t, rather than rebuilding the hour from its calendar
// fields. When clocks fall back a local
// hour happens twice, and time.Date resolves the repeated "01:00" to its first
// occurrence — so from inside the second one the rebuilt hour plus one landed
// back on the instant it started from, and the split never advanced. A zone on
// summer time hung `report` and `today` on the night it ended.
//
// Stepping a whole hour is not enough either: where the clocks change part-way
// through a local hour (Chatham changes at :45) the hour after t is not sixty
// minutes away, and one piece would span two local hours. So it walks the UTC
// quarter-hours — every zone offset in use is a multiple of fifteen minutes,
// which puts every local hour start and every clock change on one — and stops
// at the first that begins a local hour or carries a different offset. A piece
// therefore never crosses a local hour or a clock change, and always advances.
func nextHour(t time.Time) time.Time {
	const quarter = 15 * time.Minute
	_, offset := t.Zone()
	n := t.Truncate(quarter).Add(quarter)
	for i := 0; i < 8; i++ {
		if _, o := n.Zone(); o != offset || (n.Minute() == 0 && n.Second() == 0) {
			return n
		}
		n = n.Add(quarter)
	}
	return n
}

// The totals below come in two forms. The ...Of functions total a segment list
// the caller already holds — `report` derives one from the read `timeline`
// uses and clips it to the window (ADR 0005). The others derive the list from
// samples (see Segments) and total all of it.

// RangeOf returns the grand totals of a segment list.
func RangeOf(segs []Segment, loc *time.Location) Totals {
	var tot Totals
	walkSegments(segs, loc, func(_ time.Time, d time.Duration, st activity.State) {
		tot.add(st, d)
	})
	return tot
}

// Range returns the grand totals across all samples.
func Range(samples []activity.Sample, p Params, loc *time.Location) Totals {
	return RangeOf(Segments(samples, p, loc), loc)
}

// ByDayOf returns per-logical-day totals of a segment list, sorted ascending by
// date. Each second is attributed to the logical day it falls in — unlike
// Timeline, which attributes a whole session to the day it started in. The two
// agree except when a session crosses a logical day boundary.
//
// Pieces are split at hour boundaries and dayStartHour is a whole hour, so no
// piece straddles a day boundary and only the bucket key changes.
func ByDayOf(segs []Segment, loc *time.Location, dayStartHour int) []DayTotals {
	byKey := map[string]*Totals{}
	walkSegments(segs, loc, func(start time.Time, d time.Duration, st activity.State) {
		key := LogicalDate(start, dayStartHour)
		t := byKey[key]
		if t == nil {
			t = &Totals{}
			byKey[key] = t
		}
		t.add(st, d)
	})
	out := make([]DayTotals, 0, len(byKey))
	for k, v := range byKey {
		out = append(out, DayTotals{Date: k, Totals: *v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out
}

// ByDay returns per-logical-day totals across all samples.
func ByDay(samples []activity.Sample, p Params, loc *time.Location) []DayTotals {
	return ByDayOf(Segments(samples, p, loc), loc, p.DayStartHour)
}

// ByHourOfDayOf folds a segment list into 24 hour-of-day buckets (index 0..23
// in the aggregation location) — the shape a time-of-day heatmap wants.
func ByHourOfDayOf(segs []Segment, loc *time.Location) [24]Totals {
	var buckets [24]Totals
	walkSegments(segs, loc, func(start time.Time, d time.Duration, st activity.State) {
		buckets[start.Hour()].add(st, d)
	})
	return buckets
}

// ByHourOfDay folds every day across all samples into hour-of-day buckets.
func ByHourOfDay(samples []activity.Sample, p Params, loc *time.Location) [24]Totals {
	return ByHourOfDayOf(Segments(samples, p, loc), loc)
}
