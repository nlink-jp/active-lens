package aggregate

import (
	"testing"
	"time"

	"github.com/nlink-jp/active-lens/core/activity"
)

// genSamples produces samples every stepSec seconds over [startStr, endStr],
// inclusive of both ends, all in the given state — modeling the daemon's dense
// sampling of a continuous period.
func genSamples(startStr, endStr string, stepSec int, state activity.State) []activity.Sample {
	start, end := ts(startStr), ts(endStr)
	var out []activity.Sample
	for t := start; !t.After(end); t = t.Add(time.Duration(stepSec) * time.Second) {
		out = append(out, activity.Sample{TS: t, State: state})
	}
	return out
}

// p is the default derivation: a 10-minute break threshold, a 4-hour session
// gap, and a logical day starting at 04:00.
func p(maxGap time.Duration) Params {
	return Params{
		MaxGap:         maxGap,
		BreakThreshold: 10 * time.Minute,
		SessionGap:     4 * time.Hour,
		DayStartHour:   4,
	}
}

func hhmm(t time.Time) string { return t.Format("15:04") }

func TestSegments_Merge(t *testing.T) {
	// op, op, present, present, op, op -> 3 merged segments (the trailing op needs
	// a following sample to produce an interval).
	samples := []activity.Sample{
		samp("2026-07-09 10:00:00", activity.Operating),
		samp("2026-07-09 10:00:15", activity.Operating),
		samp("2026-07-09 10:00:30", activity.Present),
		samp("2026-07-09 10:00:45", activity.Present),
		samp("2026-07-09 10:01:00", activity.Operating),
		samp("2026-07-09 10:01:15", activity.Operating),
	}
	segs := Segments(samples, 45*time.Second, utc)
	if len(segs) != 3 {
		t.Fatalf("got %d segments, want 3: %+v", len(segs), segs)
	}
	if segs[0].State != activity.Operating || segs[0].Duration() != 30*time.Second {
		t.Errorf("seg0 = %+v, want operating 30s", segs[0])
	}
	if segs[1].State != activity.Present || segs[1].Duration() != 30*time.Second {
		t.Errorf("seg1 = %+v, want present 30s", segs[1])
	}
	if segs[2].State != activity.Operating {
		t.Errorf("seg2 = %+v, want operating", segs[2])
	}
}

func TestSegments_SleepGapBecomesAwaySegment(t *testing.T) {
	samples := []activity.Sample{
		samp("2026-07-09 10:00:00", activity.Operating),
		samp("2026-07-09 12:00:00", activity.Operating),
	}
	segs := Segments(samples, 45*time.Second, utc)
	// operating 45s, then away for the rest of the 2h gap.
	if len(segs) != 2 {
		t.Fatalf("got %d segments, want 2", len(segs))
	}
	if segs[0].State != activity.Operating || segs[0].Duration() != 45*time.Second {
		t.Errorf("seg0 = %+v, want operating 45s", segs[0])
	}
	if segs[1].State != activity.Away || segs[1].Duration() != 2*time.Hour-45*time.Second {
		t.Errorf("seg1 = %+v, want away ~2h", segs[1])
	}
}

func TestTimeline_WorkSession(t *testing.T) {
	// A realistic work day sampled every 60s: morning 09:00–12:00, a 30-min lunch
	// (no samples → away), afternoon 12:30–18:00. maxGap 120s bridges the 60s
	// cadence but not the lunch gap.
	var samples []activity.Sample
	samples = append(samples, genSamples("2026-07-09 09:00:00", "2026-07-09 12:00:00", 60, activity.Operating)...)
	samples = append(samples, genSamples("2026-07-09 12:30:00", "2026-07-09 18:00:00", 60, activity.Operating)...)

	days := Timeline(samples, p(120*time.Second), utc)
	if len(days) != 1 {
		t.Fatalf("got %d days, want 1", len(days))
	}
	d := days[0]
	if !d.HasWork {
		t.Fatal("expected HasWork")
	}
	if hhmm(d.WorkStart) != "09:00" {
		t.Errorf("WorkStart = %s, want 09:00", hhmm(d.WorkStart))
	}
	if hhmm(d.WorkEnd) != "18:00" {
		t.Errorf("WorkEnd = %s, want 18:00", hhmm(d.WorkEnd))
	}
	// The 30-min lunch is far below the 4h session gap, so it stays a break inside
	// one session rather than splitting the day in two.
	if len(d.Sessions) != 1 {
		t.Fatalf("got %d sessions, want 1", len(d.Sessions))
	}
	if len(d.Breaks) != 1 {
		t.Fatalf("got %d breaks, want 1: %+v", len(d.Breaks), d.Breaks)
	}
	if d.Breaks[0].Duration() < 25*time.Minute || d.Breaks[0].Duration() > 30*time.Minute {
		t.Errorf("break = %v, want ~28m", d.Breaks[0].Duration())
	}
	// Span 9h; active < span because of the lunch break.
	if d.SpanSeconds() != 9*3600 {
		t.Errorf("span = %ds, want 9h", d.SpanSeconds())
	}
	if d.ActiveSeconds() >= d.SpanSeconds() {
		t.Errorf("active %ds should be less than span %ds (break excluded)", d.ActiveSeconds(), d.SpanSeconds())
	}
	// The session is tiled work → break → work.
	var kinds []BlockKind
	for _, b := range d.Blocks {
		kinds = append(kinds, b.Kind)
	}
	if len(kinds) != 3 || kinds[0] != BlockWork || kinds[1] != BlockBreak || kinds[2] != BlockWork {
		t.Fatalf("blocks = %v, want work, break, work", kinds)
	}
	if d.Blocks[0].OperatingSeconds == 0 {
		t.Error("a work block must carry its operating seconds")
	}
	if d.Blocks[1].OperatingSeconds != 0 || d.Blocks[1].PresentSeconds != 0 {
		t.Errorf("a break block carries no active seconds: %+v", d.Blocks[1])
	}
}

func TestTimeline_ShortGapNotABreak(t *testing.T) {
	// A 5-minute away gap with a 10-minute threshold is folded into work, not a break.
	samples := []activity.Sample{
		samp("2026-07-09 09:00:00", activity.Operating),
		samp("2026-07-09 09:00:15", activity.Operating),
		samp("2026-07-09 09:05:15", activity.Operating), // 5-min gap -> away, below threshold
		samp("2026-07-09 09:05:30", activity.Operating),
	}
	days := Timeline(samples, p(30*time.Second), utc)
	if len(days) != 1 || len(days[0].Breaks) != 0 {
		t.Fatalf("expected no breaks for a sub-threshold gap, got %+v", days)
	}
	if len(days[0].Blocks) != 1 || days[0].Blocks[0].Kind != BlockWork {
		t.Errorf("a folded gap should leave one work block, got %+v", days[0].Blocks)
	}
}

func TestTimeline_NoActivityDay(t *testing.T) {
	// All-away (locked overnight): no session, therefore no day.
	samples := []activity.Sample{
		samp("2026-07-09 02:00:00", activity.Away),
		samp("2026-07-09 02:00:15", activity.Away),
	}
	if days := Timeline(samples, p(30*time.Minute), utc); len(days) != 0 {
		t.Errorf("expected no days without activity, got %+v", days)
	}
}

func TestTimeline_MidnightCrossingSessionStaysWhole(t *testing.T) {
	// The defect this design removes: an evening running to 00:59 used to be cut at
	// midnight, giving the 10th a 00:00 start and the 9th a 00:00 end.
	samples := genSamples("2026-07-09 20:44:00", "2026-07-10 00:59:00", 60, activity.Operating)

	days := Timeline(samples, p(120*time.Second), utc)
	if len(days) != 1 {
		t.Fatalf("got %d days, want 1 (the session belongs to the 9th): %+v", len(days), days)
	}
	d := days[0]
	if d.Date != "2026-07-09" {
		t.Errorf("date = %s, want 2026-07-09", d.Date)
	}
	if hhmm(d.WorkStart) != "20:44" || hhmm(d.WorkEnd) != "00:59" {
		t.Errorf("work = %s → %s, want 20:44 → 00:59", hhmm(d.WorkStart), hhmm(d.WorkEnd))
	}
	// The end is on the next calendar day, yet still inside this logical day —
	// that gap between the two is the whole reason day_start_hour exists.
	if d.WorkEnd.Day() != 10 {
		t.Errorf("WorkEnd %v should land on the 10th", d.WorkEnd)
	}
	if !d.WorkEnd.Before(d.DayStart.Add(24 * time.Hour)) {
		t.Errorf("WorkEnd %v should still be within the logical day starting %v", d.WorkEnd, d.DayStart)
	}
	if len(d.Breaks) != 0 {
		t.Errorf("midnight is not a break: %+v", d.Breaks)
	}
}

func TestTimeline_OvernightSleepIsNotABreak(t *testing.T) {
	// Work to 00:59, sleep, resume at 07:26. The sleep exceeds the session gap, so
	// it ends the session instead of becoming a 6h27m "break".
	var samples []activity.Sample
	samples = append(samples, genSamples("2026-07-09 20:44:00", "2026-07-10 00:59:00", 60, activity.Operating)...)
	samples = append(samples, genSamples("2026-07-10 07:26:00", "2026-07-10 09:29:00", 60, activity.Operating)...)

	days := Timeline(samples, p(120*time.Second), utc)
	if len(days) != 2 {
		t.Fatalf("got %d days, want 2", len(days))
	}
	if days[0].Date != "2026-07-09" || len(days[0].Breaks) != 0 {
		t.Errorf("day0 = %s with breaks %+v, want 2026-07-09 with none", days[0].Date, days[0].Breaks)
	}
	if days[1].Date != "2026-07-10" || len(days[1].Breaks) != 0 {
		t.Errorf("day1 = %s with breaks %+v, want 2026-07-10 with none", days[1].Date, days[1].Breaks)
	}
	if hhmm(days[1].WorkStart) != "07:26" {
		t.Errorf("day1 WorkStart = %s, want 07:26 (not 00:00)", hhmm(days[1].WorkStart))
	}
}

func TestTimeline_InterSessionGapIsNotABreak(t *testing.T) {
	// A five-hour absence ends one session and starts another. It is not a break —
	// calling it one would be the same error as calling sleep a break.
	var samples []activity.Sample
	samples = append(samples, genSamples("2026-07-09 09:00:00", "2026-07-09 12:00:00", 60, activity.Operating)...)
	samples = append(samples, genSamples("2026-07-09 17:00:00", "2026-07-09 20:00:00", 60, activity.Operating)...)

	days := Timeline(samples, p(120*time.Second), utc)
	if len(days) != 1 {
		t.Fatalf("got %d days, want 1", len(days))
	}
	d := days[0]
	if len(d.Sessions) != 2 {
		t.Fatalf("got %d sessions, want 2", len(d.Sessions))
	}
	if len(d.Breaks) != 0 {
		t.Errorf("an inter-session gap must not be a break: %+v", d.Breaks)
	}
	// The day's envelope still spans both sessions...
	if hhmm(d.WorkStart) != "09:00" || hhmm(d.WorkEnd) != "20:00" {
		t.Errorf("work = %s → %s, want 09:00 → 20:00", hhmm(d.WorkStart), hhmm(d.WorkEnd))
	}
	// ...but the absence is not counted as active time.
	if d.ActiveSeconds() > 7*3600 {
		t.Errorf("active = %ds, want ~6h (the 5h absence excluded)", d.ActiveSeconds())
	}
}

func TestTimeline_AllNighterCrossesOneBoundaryWhole(t *testing.T) {
	// 20:00 → 09:00 crosses 04:00 exactly once. The backstop must not fire.
	samples := genSamples("2026-07-09 20:00:00", "2026-07-10 09:00:00", 300, activity.Operating)

	days := Timeline(samples, p(10*time.Minute), utc)
	if len(days) != 1 {
		t.Fatalf("got %d days, want 1 (an all-nighter files under the night it began): %+v", len(days), days)
	}
	d := days[0]
	if d.Date != "2026-07-09" {
		t.Errorf("date = %s, want 2026-07-09", d.Date)
	}
	if len(d.Sessions) != 1 {
		t.Errorf("got %d sessions, want 1 unbroken", len(d.Sessions))
	}
	if hhmm(d.WorkEnd) != "09:00" {
		t.Errorf("WorkEnd = %s, want 09:00", hhmm(d.WorkEnd))
	}
	// 04:00 on the 9th → 09:00 on the 10th is 29 hours: this is the case where a
	// day column must be allowed to grow past 24h.
	if !d.WorkEnd.After(d.DayStart.Add(24 * time.Hour)) {
		t.Errorf("WorkEnd %v should be past DayStart+24h (%v)", d.WorkEnd, d.DayStart.Add(24*time.Hour))
	}
}

func TestSessions_BackstopCutsAtSecondBoundary(t *testing.T) {
	// A display that never sleeps: one merged "present" run of three days. maxGap is
	// wide enough that no away is ever inferred, so only the backstop can end the
	// session. It must cut at the second logical boundary (04:00 on the 11th).
	samples := []activity.Sample{
		samp("2026-07-09 10:00:00", activity.Present),
		samp("2026-07-12 10:00:00", activity.Present),
	}
	params := p(100 * time.Hour)
	sessions := Sessions(samples, params, utc)
	if len(sessions) != 2 {
		t.Fatalf("got %d sessions, want 2 (one backstop cut)", len(sessions))
	}
	cut := time.Date(2026, 7, 11, 4, 0, 0, 0, utc)
	if !sessions[0].End.Equal(cut) {
		t.Errorf("session0 ends %v, want the second boundary %v", sessions[0].End, cut)
	}
	if !sessions[1].Start.Equal(cut) {
		t.Errorf("session1 starts %v, want %v", sessions[1].Start, cut)
	}
	if d := sessions[0].Duration(); d >= 48*time.Hour {
		t.Errorf("session0 duration %v must stay below 48h", d)
	}
	// The 10th is swallowed by the first session, so it has no day of its own.
	days := Timeline(samples, params, utc)
	if len(days) != 2 || days[0].Date != "2026-07-09" || days[1].Date != "2026-07-11" {
		t.Errorf("days = %+v, want 2026-07-09 and 2026-07-11", days)
	}
}

func TestTimeline_SessionStartingAfterMidnightFilesUnderPreviousDay(t *testing.T) {
	// 01:00–03:00 is still "the night of the 9th" when the day starts at 04:00.
	samples := genSamples("2026-07-10 01:00:00", "2026-07-10 03:00:00", 60, activity.Operating)

	days := Timeline(samples, p(120*time.Second), utc)
	if len(days) != 1 || days[0].Date != "2026-07-09" {
		t.Fatalf("days = %+v, want a single 2026-07-09", days)
	}
	if hhmm(days[0].WorkStart) != "01:00" {
		t.Errorf("WorkStart = %s, want 01:00", hhmm(days[0].WorkStart))
	}
}

func TestTimeline_DayStartHourZeroKeepsSessionWhole(t *testing.T) {
	// day_start_hour = 0 makes the logical day the calendar day. It does NOT restore
	// midnight splitting: the session is still whole, filed under its start's date.
	samples := genSamples("2026-07-09 23:30:00", "2026-07-10 00:30:00", 60, activity.Operating)

	params := p(120 * time.Second)
	params.DayStartHour = 0
	days := Timeline(samples, params, utc)
	if len(days) != 1 || days[0].Date != "2026-07-09" {
		t.Fatalf("days = %+v, want a single 2026-07-09", days)
	}
	if len(days[0].Sessions) != 1 {
		t.Errorf("got %d sessions, want 1 whole", len(days[0].Sessions))
	}
	if hhmm(days[0].WorkEnd) != "00:30" {
		t.Errorf("WorkEnd = %s, want 00:30", hhmm(days[0].WorkEnd))
	}
}

func TestLogicalDate(t *testing.T) {
	cases := []struct {
		ts   string
		hour int
		want string
	}{
		{"2026-07-10 03:59:00", 4, "2026-07-09"}, // a minute before the boundary
		{"2026-07-10 04:00:00", 4, "2026-07-10"}, // the boundary itself starts the day
		{"2026-07-09 23:30:00", 4, "2026-07-09"},
		{"2026-07-10 00:30:00", 0, "2026-07-10"},
	}
	for _, c := range cases {
		if got := LogicalDate(ts(c.ts), c.hour); got != c.want {
			t.Errorf("LogicalDate(%s, %d) = %s, want %s", c.ts, c.hour, got, c.want)
		}
	}
}
