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

// walkSegments replays the samples as attributed [start,end) segments, split at
// local hour boundaries, invoking fn for each. samples need not be pre-sorted.
func walkSegments(samples []activity.Sample, maxGap time.Duration, loc *time.Location, fn func(start time.Time, dur time.Duration, st activity.State)) {
	if len(samples) < 2 {
		return
	}
	sorted := make([]activity.Sample, len(samples))
	copy(sorted, samples)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].TS.Before(sorted[j].TS) })

	for i := 0; i+1 < len(sorted); i++ {
		a, b := sorted[i], sorted[i+1]
		delta := b.TS.Sub(a.TS)
		if delta <= 0 {
			continue
		}
		eff := delta
		if maxGap > 0 && eff > maxGap {
			eff = maxGap
		}
		mid := a.TS.Add(eff)
		emitSplit(a.TS, mid, a.State, loc, fn)
		if delta > eff {
			// The daemon was not sampling here: treat the gap as system sleep.
			emitSplit(mid, b.TS, activity.Away, loc, fn)
		}
	}
}

// emitSplit slices [start,end) at local hour boundaries and calls fn per piece.
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

// nextHour returns the start of the hour strictly after t, in t's location.
func nextHour(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, t.Hour(), 0, 0, 0, t.Location()).Add(time.Hour)
}

// Range returns the grand totals across all samples.
func Range(samples []activity.Sample, maxGap time.Duration, loc *time.Location) Totals {
	var tot Totals
	walkSegments(samples, maxGap, loc, func(_ time.Time, d time.Duration, st activity.State) {
		tot.add(st, d)
	})
	return tot
}

// ByDay returns per-logical-day totals, sorted ascending by date. Each second is
// attributed to the logical day it falls in — unlike Timeline, which attributes a
// whole session to the day it started in. The two agree except when a session
// crosses a logical day boundary.
//
// Intervals are already split at hour boundaries and dayStartHour is a whole
// hour, so no piece straddles a boundary and only the bucket key changes.
func ByDay(samples []activity.Sample, maxGap time.Duration, loc *time.Location, dayStartHour int) []DayTotals {
	byKey := map[string]*Totals{}
	walkSegments(samples, maxGap, loc, func(start time.Time, d time.Duration, st activity.State) {
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

// ByHourOfDay folds every day in the range into 24 hour-of-day buckets (index
// 0..23 in the aggregation location) — the shape a time-of-day heatmap wants.
func ByHourOfDay(samples []activity.Sample, maxGap time.Duration, loc *time.Location) [24]Totals {
	var buckets [24]Totals
	walkSegments(samples, maxGap, loc, func(start time.Time, d time.Duration, st activity.State) {
		buckets[start.Hour()].add(st, d)
	})
	return buckets
}
