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

// --- ADR 0002: day_boundary = "strict" -------------------------------------

// pStrict is the default derivation with the boundary cutting every session —
// the workplace rule, where the day belongs to the employer, not the night.
func pStrict(maxGap time.Duration) Params {
	params := p(maxGap)
	params.DayBoundary = BoundaryStrict
	return params
}

func TestSessions_StrictCutsAtEveryBoundary(t *testing.T) {
	// The same 20:00 → 09:00 all-nighter that BoundarySession keeps whole.
	samples := genSamples("2026-07-09 20:00:00", "2026-07-10 09:00:00", 300, activity.Operating)

	whole := Sessions(samples, p(10*time.Minute), utc)
	if len(whole) != 1 {
		t.Fatalf("session mode: got %d sessions, want 1 (the ADR 0001 behaviour)", len(whole))
	}

	sessions := Sessions(samples, pStrict(10*time.Minute), utc)
	if len(sessions) != 2 {
		t.Fatalf("strict: got %d sessions, want 2 (cut at 04:00): %+v", len(sessions), sessions)
	}
	cut := time.Date(2026, 7, 10, 4, 0, 0, 0, utc)
	if !sessions[0].End.Equal(cut) || !sessions[1].Start.Equal(cut) {
		t.Errorf("cut = %v/%v, want both at the boundary %v", sessions[0].End, sessions[1].Start, cut)
	}
	if !sessions[0].CarriedOut || sessions[0].CarriedIn {
		t.Errorf("head carried flags = in:%v out:%v, want in:false out:true", sessions[0].CarriedIn, sessions[0].CarriedOut)
	}
	if !sessions[1].CarriedIn || sessions[1].CarriedOut {
		t.Errorf("tail carried flags = in:%v out:%v, want in:true out:false", sessions[1].CarriedIn, sessions[1].CarriedOut)
	}
	// Cutting must move seconds between days, never create or destroy them.
	if got, want := sessions[0].ActiveSeconds()+sessions[1].ActiveSeconds(), whole[0].ActiveSeconds(); got != want {
		t.Errorf("active seconds after the cut = %d, want %d (the uncut session)", got, want)
	}
}

func TestTimeline_StrictFilesEachPieceUnderItsOwnDay(t *testing.T) {
	// The workplace case: a day that starts at 05:00, worked straight through it.
	params := pStrict(10 * time.Minute)
	params.DayStartHour = 5
	samples := genSamples("2026-07-09 22:00:00", "2026-07-10 10:00:00", 300, activity.Operating)

	days := Timeline(samples, params, utc)
	if len(days) != 2 {
		t.Fatalf("got %d days, want 2 (the boundary splits the night): %+v", len(days), days)
	}
	if days[0].Date != "2026-07-09" || days[1].Date != "2026-07-10" {
		t.Fatalf("dates = %s, %s; want 2026-07-09 and 2026-07-10", days[0].Date, days[1].Date)
	}
	if hhmm(days[0].WorkEnd) != "05:00" || !days[0].CarriedOut {
		t.Errorf("day0 ends %s (carried_out=%v), want 05:00 carried out",
			hhmm(days[0].WorkEnd), days[0].CarriedOut)
	}
	if hhmm(days[1].WorkStart) != "05:00" || !days[1].CarriedIn {
		t.Errorf("day1 starts %s (carried_in=%v), want 05:00 carried in",
			hhmm(days[1].WorkStart), days[1].CarriedIn)
	}
	// 22:00→05:00 is 7h, 05:00→10:00 is 5h.
	if got := days[0].ActiveSeconds(); got != 7*3600 {
		t.Errorf("day0 active = %ds, want 7h", got)
	}
	if got := days[1].ActiveSeconds(); got != 5*3600 {
		t.Errorf("day1 active = %ds, want 5h", got)
	}
}

func TestSessions_StrictAwayAcrossBoundaryCarriesNeither(t *testing.T) {
	// Away when the day turns over: each day begins and ends on real activity, so
	// there is no cut to label. 03:30 → 04:25 away is over the break threshold but
	// well under the session gap, and it straddles 04:00.
	var samples []activity.Sample
	samples = append(samples, genSamples("2026-07-09 23:00:00", "2026-07-10 03:25:00", 300, activity.Operating)...)
	samples = append(samples, genSamples("2026-07-10 03:30:00", "2026-07-10 04:20:00", 300, activity.Away)...)
	samples = append(samples, genSamples("2026-07-10 04:25:00", "2026-07-10 06:00:00", 300, activity.Operating)...)

	sessions := Sessions(samples, pStrict(10*time.Minute), utc)
	if len(sessions) != 2 {
		t.Fatalf("got %d sessions, want 2: %+v", len(sessions), sessions)
	}
	if hhmm(sessions[0].End) != "03:30" || sessions[0].CarriedOut {
		t.Errorf("session0 ends %s (carried_out=%v), want 03:30 and no carry",
			hhmm(sessions[0].End), sessions[0].CarriedOut)
	}
	if hhmm(sessions[1].Start) != "04:25" || sessions[1].CarriedIn {
		t.Errorf("session1 starts %s (carried_in=%v), want 04:25 and no carry",
			hhmm(sessions[1].Start), sessions[1].CarriedIn)
	}
	// The straddling away belongs to neither day's work log.
	for _, s := range sessions {
		for _, b := range s.Breaks {
			t.Errorf("break %s–%s: an away that ends a day is not a break", hhmm(b.Start), hhmm(b.End))
		}
	}
}

func TestSessions_StrictNeverOutlivesItsLogicalDay(t *testing.T) {
	// The display that never sleeps: under BoundarySession the backstop caps this
	// at the second boundary; under strict every boundary cuts, so the three-day
	// run becomes one session per logical day, each below 24h.
	samples := []activity.Sample{
		samp("2026-07-09 10:00:00", activity.Present),
		samp("2026-07-12 10:00:00", activity.Present),
	}
	sessions := Sessions(samples, pStrict(100*time.Hour), utc)
	if len(sessions) != 4 {
		t.Fatalf("got %d sessions, want 4 (07-09, 07-10, 07-11, 07-12): %+v", len(sessions), sessions)
	}
	for i, s := range sessions {
		// A session filling a whole logical day is exactly 24h; more than that
		// would mean it outlived the day it is filed under.
		if s.Duration() > 24*time.Hour {
			t.Errorf("session %d lasts %v; strict sessions cannot outlive their day", i, s.Duration())
		}
	}
	days := Timeline(samples, pStrict(100*time.Hour), utc)
	if len(days) != 4 {
		t.Errorf("got %d days, want 4 — no day is swallowed by its predecessor: %+v", len(days), days)
	}
}

func TestTimeline_StrictDayTotalsMatchReportDayTotals(t *testing.T) {
	// ADR 0001 §5's accepted divergence, inverted: under strict the work-log
	// ledger and the totals ledger must agree day for day.
	params := pStrict(10 * time.Minute)
	var samples []activity.Sample
	samples = append(samples, genSamples("2026-07-09 20:00:00", "2026-07-10 09:00:00", 300, activity.Operating)...)
	samples = append(samples, genSamples("2026-07-10 09:05:00", "2026-07-10 15:00:00", 300, activity.Away)...)
	samples = append(samples, genSamples("2026-07-10 15:05:00", "2026-07-10 18:00:00", 300, activity.Present)...)

	byDate := map[string]int{}
	for _, d := range Timeline(samples, params, utc) {
		byDate[d.Date] = d.ActiveSeconds()
	}
	for _, d := range ByDay(samples, params.MaxGap, utc, params.DayStartHour) {
		want := int((d.Totals.Operating + d.Totals.Present).Seconds())
		if got := byDate[d.Date]; got != want {
			t.Errorf("%s: timeline active %ds, report active %ds", d.Date, got, want)
		}
		delete(byDate, d.Date)
	}
	for date, secs := range byDate {
		t.Errorf("%s: timeline reports %ds the report path never saw", date, secs)
	}
}

func TestSessions_BackstopCutIsLabelledInSessionMode(t *testing.T) {
	// The default mode has exactly one artificial boundary. It gets the same
	// labels, so a consumer never has to guess whether 04:00 was a real start.
	samples := []activity.Sample{
		samp("2026-07-09 10:00:00", activity.Present),
		samp("2026-07-12 10:00:00", activity.Present),
	}
	sessions := Sessions(samples, p(100*time.Hour), utc)
	if len(sessions) != 2 {
		t.Fatalf("got %d sessions, want 2", len(sessions))
	}
	if !sessions[0].CarriedOut || !sessions[1].CarriedIn {
		t.Errorf("backstop cut unlabelled: out=%v in=%v", sessions[0].CarriedOut, sessions[1].CarriedIn)
	}
}

func TestParams_BoundaryDefaultsToSession(t *testing.T) {
	if got := (Params{}).Boundary(); got != BoundarySession {
		t.Errorf("zero Params boundary = %q, want %q", got, BoundarySession)
	}
	if got := pStrict(time.Minute).Boundary(); got != BoundaryStrict {
		t.Errorf("strict params boundary = %q, want %q", got, BoundaryStrict)
	}
}

func TestSessions_CutsWhenTheBoundaryFallsBetweenSegments(t *testing.T) {
	// The boundary needs no segment to straddle it: a state change (or a max_gap
	// split) landing exactly on the hour leaves one segment ending at 04:00 and
	// the next beginning there. The cut must still happen — and the limit must
	// still advance, or every later boundary is missed too.
	var samples []activity.Sample
	samples = append(samples, genSamples("2026-07-09 20:00:00", "2026-07-10 03:55:00", 300, activity.Operating)...)
	samples = append(samples, genSamples("2026-07-10 04:00:00", "2026-07-10 09:00:00", 300, activity.Present)...)

	params := pStrict(10 * time.Minute)
	sessions := Sessions(samples, params, utc)
	if len(sessions) != 2 {
		t.Fatalf("got %d sessions, want 2 (cut at 04:00): %+v", len(sessions), sessions)
	}
	cut := time.Date(2026, 7, 10, 4, 0, 0, 0, utc)
	if !sessions[0].End.Equal(cut) || !sessions[0].CarriedOut {
		t.Errorf("head ends %v (out=%v), want the boundary, carried out", sessions[0].End, sessions[0].CarriedOut)
	}
	if !sessions[1].Start.Equal(cut) || !sessions[1].CarriedIn {
		t.Errorf("tail starts %v (in=%v), want the boundary, carried in", sessions[1].Start, sessions[1].CarriedIn)
	}

	// And the day ledgers must agree, which is the whole point of strict.
	byDate := map[string]int{}
	for _, d := range Timeline(samples, params, utc) {
		byDate[d.Date] = d.ActiveSeconds()
	}
	for _, d := range ByDay(samples, params.MaxGap, utc, params.DayStartHour) {
		want := int((d.Totals.Operating + d.Totals.Present).Seconds())
		if got := byDate[d.Date]; got != want {
			t.Errorf("%s: timeline %ds, report %ds", d.Date, got, want)
		}
	}
}

func TestSessions_BackstopFiresWhenSamplesLandOnTheBoundary(t *testing.T) {
	// The same defect reached BoundarySession: with the limit left behind at a
	// boundary that is never met again, a display that never sleeps ran past the
	// 48h backstop. Samples every 15 minutes land exactly on 04:00.
	samples := genSamples("2026-07-09 10:00:00", "2026-07-13 10:00:00", 900, activity.Present)

	sessions := Sessions(samples, p(30*time.Minute), utc)
	for i, s := range sessions {
		// A session that itself began on a boundary spans exactly two logical
		// days; anything past that is the limit having been left behind.
		if s.Duration() > 48*time.Hour {
			t.Errorf("session %d lasts %v; the backstop must keep it within 48h", i, s.Duration())
		}
	}
	if len(sessions) < 2 {
		t.Fatalf("got %d sessions, want the run cut by the backstop", len(sessions))
	}
}

func TestSessions_StrictNoBoundaryIsSkippedOverAMultiDayRun(t *testing.T) {
	// Every logical day in the span must get its own session, whatever the phase
	// of the sampling ticker relative to the boundary.
	for _, step := range []int{60, 300, 900, 901} {
		var samples []activity.Sample
		for _, offset := range []int{0, 7, 13} {
			samples = genSamples(
				time.Date(2026, 7, 9, 10, 0, offset, 0, utc).Format("2006-01-02 15:04:05"),
				"2026-07-13 10:00:00", step, activity.Present)
			sessions := Sessions(samples, pStrict(time.Duration(step*2)*time.Second), utc)
			if len(sessions) != 5 {
				t.Errorf("step %ds offset %ds: got %d sessions, want 5 (07-09..07-13)",
					step, offset, len(sessions))
			}
			for i, s := range sessions {
				if s.Duration() > 24*time.Hour {
					t.Errorf("step %ds offset %ds: session %d lasts %v, past its day",
						step, offset, i, s.Duration())
				}
			}
		}
	}
}

func TestSessions_StrictAcrossDaylightSaving(t *testing.T) {
	// The boundary is a wall-clock hour, so a logical day across a DST change is
	// 23 or 25 hours long and a strict session can run past 24h. That is the
	// boundary doing its job, not a defect — but seconds must still be conserved,
	// and the cut must still happen exactly once per calendar day.
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("tzdata unavailable")
	}
	params := pStrict(10 * time.Minute)
	params.DayStartHour = 1 // 01:00, which 2026-11-01 passes twice

	// 2026-10-31 20:00 EDT through 2026-11-02 06:00 EST, dense.
	start := time.Date(2026, 10, 31, 20, 0, 0, 0, ny)
	end := time.Date(2026, 11, 2, 6, 0, 0, 0, ny)
	var samples []activity.Sample
	for ts := start; !ts.After(end); ts = ts.Add(5 * time.Minute) {
		samples = append(samples, activity.Sample{TS: ts, State: activity.Operating})
	}

	whole := Sessions(samples, p(10*time.Minute), ny)
	cut := Sessions(samples, params, ny)
	var wholeSecs, cutSecs int
	for _, s := range whole {
		wholeSecs += s.ActiveSeconds()
	}
	for _, s := range cut {
		cutSecs += s.ActiveSeconds()
	}
	if wholeSecs != cutSecs {
		t.Errorf("strict total %ds != session total %ds across a DST change", cutSecs, wholeSecs)
	}

	days := Timeline(samples, params, ny)
	if len(days) != 3 {
		t.Fatalf("got %d days, want 3 (10-31, 11-01, 11-02): %+v", len(days), days)
	}
	// The repeated hour makes 11-01 a 25-hour logical day.
	var nov1 DayTimeline
	for _, d := range days {
		if d.Date == "2026-11-01" {
			nov1 = d
		}
	}
	if got := nov1.ActiveSeconds(); got != 25*3600 {
		t.Errorf("2026-11-01 active = %ds, want 25h — the day the clocks went back", got)
	}
}
