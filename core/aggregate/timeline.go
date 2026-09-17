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

// Params bundles the knobs the work-log derivation reads. They are applied at
// aggregation time, never at record time, so the stored history re-derives under
// new values without a migration.
type Params struct {
	// MaxGap caps how much an inter-sample interval credits to a state.
	MaxGap time.Duration
	// BreakThreshold is the shortest away span inside a session that counts as a
	// break; shorter ones fold into continuous work.
	BreakThreshold time.Duration
	// SessionGap is the away span that ends a session. Must exceed BreakThreshold.
	SessionGap time.Duration
	// DayStartHour (0..23) is the local hour a logical day begins at.
	DayStartHour int
	// DayBoundary selects what a day boundary does to a session in progress.
	// The zero value is BoundarySession, the behaviour of ADR 0001.
	DayBoundary DayBoundary
}

// DayBoundary is what a logical day boundary does to a session running across
// it. See docs/{en,ja}/adr/0002.
type DayBoundary string

const (
	// BoundarySession files a session, whole, under the logical day it started
	// in; only the backstop at the second boundary ever cuts one. Zero value.
	BoundarySession DayBoundary = "session"
	// BoundaryStrict ends a session at every logical day boundary, so each day is
	// credited exactly the work that fell inside it — the rule for a working day
	// defined by someone other than the user.
	BoundaryStrict DayBoundary = "strict"
)

// Boundary is the mode in effect, resolving the zero value to BoundarySession.
func (p Params) Boundary() DayBoundary {
	if p.DayBoundary == BoundaryStrict {
		return BoundaryStrict
	}
	return BoundarySession
}

// cutAfter returns the instant a session started at t must be closed at: the
// next boundary under BoundaryStrict, the one after that under BoundarySession,
// where it is a backstop against a session that never terminates itself rather
// than a routine cut.
func (p Params) cutAfter(t time.Time) time.Time {
	if p.Boundary() == BoundaryStrict {
		return boundaryAfter(t, p.DayStartHour)
	}
	return secondBoundaryAfter(t, p.DayStartHour)
}

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

// WorkBreak is an away span long enough to count as a break, within a session.
type WorkBreak struct {
	Start time.Time
	End   time.Time
}

// Duration is the break's length.
func (b WorkBreak) Duration() time.Duration { return b.End.Sub(b.Start) }

// isActive reports whether a state counts as "at the machine / working".
// Both operating and present count (a reading/thinking pause is still work).
func isActive(s activity.State) bool { return s == activity.Operating || s == activity.Present }

// --- sessions -------------------------------------------------------------

// Session is one unbroken stretch of work: a maximal run of segments containing
// at least one active segment, delimited by away spans of at least SessionGap.
// It begins and ends on activity — leading and trailing away is trimmed — so
// Start and End are real moments the user was at the machine — except where a
// day-boundary cut made one of them, which CarriedIn / CarriedOut record. Under
// BoundarySession a session is never split at a calendar boundary, which is what
// keeps an evening that runs past midnight in one piece.
type Session struct {
	Start    time.Time
	End      time.Time
	Segments []Segment // spans from Start to End, including the internal breaks
	Breaks   []WorkBreak

	// CarriedIn means Start is a day-boundary cut rather than a real start: work
	// was already in progress when the logical day turned over. CarriedOut means
	// End is such a cut — work continued past the boundary.
	CarriedIn  bool
	CarriedOut bool

	OperatingSeconds int
	PresentSeconds   int
}

// ActiveSeconds is operating + present within the session.
func (s Session) ActiveSeconds() int { return s.OperatingSeconds + s.PresentSeconds }

// Duration is the wall-clock span from start to end, breaks included.
func (s Session) Duration() time.Duration { return s.End.Sub(s.Start) }

// Sessions derives the work sessions from the sample stream, before any day
// bucketing. Two rules end a session:
//
//   - an away span of at least p.SessionGap (the away belongs to no session), and
//   - the day boundary p.cutAfter reports for the session's start.
//
// Under BoundarySession that second rule is a backstop, not a routine cut.
// activity.Classify reports "present" for as long as the display is on and the
// machine unlocked, however long the user has been idle, so a Mac held awake
// emits an activity run that never terminates on its own. Bounding a session at
// its second boundary keeps such a run below 48h instead of unbounded, while
// leaving a real all-nighter (which crosses exactly one boundary) whole. Under
// BoundaryStrict it is the routine cut: every boundary ends the session, and no
// session outlives its logical day.
//
// A session that a boundary cut ended, and the one that opens at that instant,
// are marked CarriedOut / CarriedIn so a consumer can tell a cut from a real
// start or end.
func Sessions(samples []activity.Sample, p Params, loc *time.Location) []Session {
	segs := Segments(samples, p.MaxGap, loc)

	var out []Session
	var buf []Segment
	// limit is the boundary this session must be closed at; the zero value means
	// the session has not begun (no active segment seen yet).
	var limit time.Time
	// carryFrom is the boundary the previous session was cut at, or zero when it
	// ended on its own. A session claims CarriedIn only if it starts on that very
	// instant: work interrupted at the boundary and resumed later began for real.
	var carryFrom time.Time

	// flush closes the buffered session. cutAt is the boundary that ended it, or
	// the zero time when an absence or the end of the stream did.
	flush := func(cutAt time.Time) {
		if s, ok := newSession(buf, p.BreakThreshold); ok {
			s.CarriedIn = !carryFrom.IsZero() && s.Start.Equal(carryFrom)
			s.CarriedOut = !cutAt.IsZero() && s.End.Equal(cutAt)
			out = append(out, s)
		}
		carryFrom = cutAt
		buf, limit = nil, time.Time{}
	}

	var pending *Segment
	for i := 0; i < len(segs) || pending != nil; {
		var seg Segment
		if pending != nil {
			seg, pending = *pending, nil
		} else {
			seg, i = segs[i], i+1
		}

		// A long absence ends the session and belongs to none.
		if seg.State == activity.Away && p.SessionGap > 0 && seg.Duration() >= p.SessionGap {
			flush(time.Time{})
			continue
		}

		if limit.IsZero() && isActive(seg.State) {
			limit = p.cutAfter(seg.Start)
		}

		// The boundary can fall between two segments as easily as inside one — a
		// state change or a max_gap split landing exactly on the hour. Close the
		// session here and re-read this segment with the limit cleared, so it
		// opens the next session and computes its own limit. Missing this case
		// left `limit` set to a boundary already passed, which is never met
		// again: the session then ran on until the next long absence.
		if !limit.IsZero() && !seg.Start.Before(limit) {
			flush(limit)
			pending = &seg
			continue
		}

		// A merged segment can be arbitrarily long, so the cut is applied within a
		// segment, not only between segments.
		if !limit.IsZero() && seg.End.After(limit) {
			tail := Segment{Start: limit, End: seg.End, State: seg.State}
			buf = append(buf, Segment{Start: seg.Start, End: limit, State: seg.State})
			flush(limit)
			pending = &tail
			continue
		}
		buf = append(buf, seg)
	}
	flush(time.Time{})
	return out
}

// newSession trims buf to its first..last active segment and derives the
// session's totals and breaks. ok is false when buf holds no activity.
func newSession(buf []Segment, breakThreshold time.Duration) (Session, bool) {
	first, last := -1, -1
	for i := range buf {
		if isActive(buf[i].State) {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	if first < 0 {
		return Session{}, false
	}
	segs := buf[first : last+1]
	s := Session{Start: segs[0].Start, End: segs[len(segs)-1].End, Segments: segs}
	for _, seg := range segs {
		switch seg.State {
		case activity.Operating:
			s.OperatingSeconds += int(seg.Duration().Seconds())
		case activity.Present:
			s.PresentSeconds += int(seg.Duration().Seconds())
		case activity.Away:
			// An away span inside a session is a break once it is long enough.
			// Session-ending absences never reach here: they are at least
			// SessionGap, which exceeds BreakThreshold, and were split off already.
			if seg.Duration() >= breakThreshold {
				s.Breaks = append(s.Breaks, WorkBreak{Start: seg.Start, End: seg.End})
			}
		}
	}
	return s, true
}

// --- blocks ---------------------------------------------------------------

// BlockKind distinguishes the two things a session is made of.
type BlockKind string

const (
	// BlockWork is a contiguous run of activity, short away gaps folded in.
	BlockWork BlockKind = "work"
	// BlockBreak is an away span long enough to count as a break.
	BlockBreak BlockKind = "break"
)

// Block is a session's coarse structure: the unit a pointer can actually hit.
// Raw segments alternate operating/present every few minutes and are sub-pixel
// on a day column, so the GUI hit-tests blocks and draws segments.
type Block struct {
	Kind  BlockKind
	Start time.Time
	End   time.Time

	OperatingSeconds int // zero for a break block
	PresentSeconds   int
}

// Duration is the block's length.
func (b Block) Duration() time.Duration { return b.End.Sub(b.Start) }

// blocksOf tiles a session: work blocks separated by break blocks. Because a
// session starts and ends on activity, the first and last block are always work.
func blocksOf(s Session, breakThreshold time.Duration) []Block {
	var out []Block
	cur := -1 // index of the open work block, or -1
	for _, seg := range s.Segments {
		if seg.State == activity.Away && seg.Duration() >= breakThreshold {
			cur = -1
			out = append(out, Block{Kind: BlockBreak, Start: seg.Start, End: seg.End})
			continue
		}
		if cur < 0 {
			out = append(out, Block{Kind: BlockWork, Start: seg.Start, End: seg.End})
			cur = len(out) - 1
		} else {
			out[cur].End = seg.End
		}
		switch seg.State {
		case activity.Operating:
			out[cur].OperatingSeconds += int(seg.Duration().Seconds())
		case activity.Present:
			out[cur].PresentSeconds += int(seg.Duration().Seconds())
		}
	}
	return out
}

// --- logical days ---------------------------------------------------------

// LogicalDayStart returns the instant the logical day containing t begins.
func LogicalDayStart(t time.Time, dayStartHour int) time.Time {
	y, m, d := t.Date()
	start := time.Date(y, m, d, dayStartHour, 0, 0, 0, t.Location())
	if t.Before(start) {
		start = start.AddDate(0, 0, -1)
	}
	return start
}

// LogicalDate is the "YYYY-MM-DD" key of the logical day containing t.
func LogicalDate(t time.Time, dayStartHour int) string {
	return LogicalDayStart(t, dayStartHour).Format("2006-01-02")
}

// boundaryAfter returns the first logical day boundary strictly after t.
func boundaryAfter(t time.Time, dayStartHour int) time.Time {
	y, m, d := t.Date()
	b := time.Date(y, m, d, dayStartHour, 0, 0, 0, t.Location())
	if !b.After(t) {
		b = b.AddDate(0, 0, 1)
	}
	return b
}

// secondBoundaryAfter returns the boundary one logical day past boundaryAfter(t).
func secondBoundaryAfter(t time.Time, dayStartHour int) time.Time {
	return boundaryAfter(boundaryAfter(t, dayStartHour), dayStartHour)
}

// --- day timelines --------------------------------------------------------

// DayTimeline is one logical day's sessions plus the derived work markers.
type DayTimeline struct {
	Date     string
	DayStart time.Time // the instant this logical day begins

	Sessions []Session
	Segments []Segment // every session's segments, in order
	Blocks   []Block   // every session's blocks, in order

	HasWork   bool
	WorkStart time.Time // first session's start
	WorkEnd   time.Time // last session's end — may fall past DayStart+24h
	Breaks    []WorkBreak

	// CarriedIn means WorkStart is this day's boundary rather than a real start —
	// work was already in progress when the day turned over. CarriedOut means the
	// day's last session was cut at the next boundary and work continued past it.
	CarriedIn  bool
	CarriedOut bool

	OperatingSeconds int
	PresentSeconds   int
}

// ActiveSeconds is operating + present across the day's sessions.
func (d DayTimeline) ActiveSeconds() int { return d.OperatingSeconds + d.PresentSeconds }

// SpanSeconds is the wall-clock from work start to work end (breaks and any
// between-session absences included).
func (d DayTimeline) SpanSeconds() int {
	if !d.HasWork {
		return 0
	}
	return int(d.WorkEnd.Sub(d.WorkStart).Seconds())
}

// Timeline derives sessions and files each one under the logical day it started
// in. Under BoundarySession a session is filed whole, so a day's work may end
// after the day does; under BoundaryStrict no session crosses a boundary in the
// first place, and the day totals match those of the report path. Days with any
// activity are returned, sorted ascending by date.
func Timeline(samples []activity.Sample, p Params, loc *time.Location) []DayTimeline {
	byDay := map[string][]Session{}
	for _, s := range Sessions(samples, p, loc) {
		date := LogicalDate(s.Start, p.DayStartHour)
		byDay[date] = append(byDay[date], s)
	}

	out := make([]DayTimeline, 0, len(byDay))
	for date, sessions := range byDay {
		sort.Slice(sessions, func(i, j int) bool { return sessions[i].Start.Before(sessions[j].Start) })
		out = append(out, deriveDay(date, sessions, p))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out
}

// deriveDay folds one logical day's sessions into its work markers. Gaps between
// sessions are not breaks: a five-hour absence ends a session and starts another,
// and calling that a break would be the very error this derivation removes.
func deriveDay(date string, sessions []Session, p Params) DayTimeline {
	d := DayTimeline{
		Date:       date,
		DayStart:   LogicalDayStart(sessions[0].Start, p.DayStartHour),
		Sessions:   sessions,
		HasWork:    true,
		WorkStart:  sessions[0].Start,
		WorkEnd:    sessions[len(sessions)-1].End,
		CarriedIn:  sessions[0].CarriedIn,
		CarriedOut: sessions[len(sessions)-1].CarriedOut,
	}
	for _, s := range sessions {
		d.Segments = append(d.Segments, s.Segments...)
		d.Blocks = append(d.Blocks, blocksOf(s, p.BreakThreshold)...)
		d.Breaks = append(d.Breaks, s.Breaks...)
		d.OperatingSeconds += s.OperatingSeconds
		d.PresentSeconds += s.PresentSeconds
	}
	return d
}
