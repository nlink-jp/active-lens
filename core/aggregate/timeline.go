package aggregate

import (
	"sort"
	"time"

	"github.com/nlink-jp/active-lens/core/activity"
)

// Segment is a contiguous run of a single state over [Start, End).
type Segment struct {
	Start time.Time
	End   time.Time
	State activity.State
}

// Duration is the segment's length.
func (s Segment) Duration() time.Duration { return s.End.Sub(s.Start) }

// Segments collapses the sample stream into contiguous same-state spans (times
// in loc). It uses the same interval attribution as the totals aggregation —
// each gap up to maxGap is credited to the earlier sample's state, and any
// excess is a system-sleep "away" span — then merges adjacent same-state pieces.
func Segments(samples []activity.Sample, maxGap time.Duration, loc *time.Location) []Segment {
	if len(samples) < 2 {
		return nil
	}
	sorted := make([]activity.Sample, len(samples))
	copy(sorted, samples)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].TS.Before(sorted[j].TS) })

	var pieces []Segment
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
		pieces = append(pieces, Segment{a.TS.In(loc), mid.In(loc), a.State})
		if delta > eff {
			pieces = append(pieces, Segment{mid.In(loc), b.TS.In(loc), activity.Away})
		}
	}

	// Merge adjacent, contiguous, same-state pieces.
	var merged []Segment
	for _, p := range pieces {
		if n := len(merged); n > 0 && merged[n-1].State == p.State && merged[n-1].End.Equal(p.Start) {
			merged[n-1].End = p.End
		} else {
			merged = append(merged, p)
		}
	}
	return merged
}

// WorkBreak is an away span long enough to count as a break, within the workday.
type WorkBreak struct {
	Start time.Time
	End   time.Time
}

// Duration is the break's length.
func (b WorkBreak) Duration() time.Duration { return b.End.Sub(b.Start) }

// DayTimeline is one day's segments plus the derived work-session markers.
type DayTimeline struct {
	Date     string
	Segments []Segment // clipped to this day, sorted

	HasWork   bool
	WorkStart time.Time // first active (operating|present) moment
	WorkEnd   time.Time // last active moment
	Breaks    []WorkBreak

	OperatingSeconds int
	PresentSeconds   int
}

// ActiveSeconds is operating + present within the day.
func (d DayTimeline) ActiveSeconds() int { return d.OperatingSeconds + d.PresentSeconds }

// SpanSeconds is the wall-clock from work start to work end (includes breaks).
func (d DayTimeline) SpanSeconds() int {
	if !d.HasWork {
		return 0
	}
	return int(d.WorkEnd.Sub(d.WorkStart).Seconds())
}

// isActive reports whether a state counts as "at the machine / working".
// Both operating and present count (a reading/thinking pause is still work).
func isActive(s activity.State) bool { return s == activity.Operating || s == activity.Present }

// Timeline builds per-day timelines with work-session derivation. Segments that
// span midnight are split at the local day boundary. A break is an away span of
// at least breakThreshold that falls between the day's first and last active
// moment; shorter away gaps are folded into continuous work. Days with any
// activity are returned, sorted ascending by date.
func Timeline(samples []activity.Sample, maxGap, breakThreshold time.Duration, loc *time.Location) []DayTimeline {
	segs := Segments(samples, maxGap, loc)

	byDay := map[string][]Segment{}
	for _, s := range segs {
		splitByDay(s, loc, func(date string, part Segment) {
			byDay[date] = append(byDay[date], part)
		})
	}

	out := make([]DayTimeline, 0, len(byDay))
	for date, daySegs := range byDay {
		sort.Slice(daySegs, func(i, j int) bool { return daySegs[i].Start.Before(daySegs[j].Start) })
		out = append(out, deriveDay(date, daySegs, breakThreshold))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out
}

// deriveDay computes the work-session markers for one day's sorted segments.
func deriveDay(date string, segs []Segment, breakThreshold time.Duration) DayTimeline {
	d := DayTimeline{Date: date, Segments: segs}
	firstActive := -1
	lastActive := -1
	for i := range segs {
		switch segs[i].State {
		case activity.Operating:
			d.OperatingSeconds += int(segs[i].Duration().Seconds())
		case activity.Present:
			d.PresentSeconds += int(segs[i].Duration().Seconds())
		}
		if isActive(segs[i].State) {
			if firstActive < 0 {
				firstActive = i
			}
			lastActive = i
		}
	}
	if firstActive < 0 {
		return d // no work this day
	}
	d.HasWork = true
	d.WorkStart = segs[firstActive].Start
	d.WorkEnd = segs[lastActive].End

	// Breaks: away segments >= threshold between work start and work end.
	for _, s := range segs {
		if s.State != activity.Away {
			continue
		}
		if !s.Start.Before(d.WorkStart) && !s.End.After(d.WorkEnd) && s.Duration() >= breakThreshold {
			d.Breaks = append(d.Breaks, WorkBreak{Start: s.Start, End: s.End})
		}
	}
	return d
}

// splitByDay cuts a segment at local midnight boundaries, calling fn per day part.
func splitByDay(seg Segment, loc *time.Location, fn func(date string, part Segment)) {
	start := seg.Start.In(loc)
	end := seg.End.In(loc)
	for start.Before(end) {
		nb := nextMidnight(start)
		segEnd := end
		if nb.Before(segEnd) {
			segEnd = nb
		}
		fn(start.Format("2006-01-02"), Segment{Start: start, End: segEnd, State: seg.State})
		start = segEnd
	}
}

// nextMidnight returns 00:00 of the day after t, in t's location.
func nextMidnight(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location()).AddDate(0, 0, 1)
}
