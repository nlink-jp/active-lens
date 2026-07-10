package cmd

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/nlink-jp/active-lens/core/activity"
	"github.com/nlink-jp/active-lens/core/aggregate"
)

// testParams mirrors the shipped defaults: 10-minute breaks, a 4-hour session
// gap, and a logical day starting at 04:00.
func testParams(maxGap time.Duration) aggregate.Params {
	return aggregate.Params{
		MaxGap:         maxGap,
		BreakThreshold: 10 * time.Minute,
		SessionGap:     4 * time.Hour,
		DayStartHour:   4,
	}
}

func TestFormatSeconds(t *testing.T) {
	cases := map[int64]string{
		0:    "0s",
		20:   "20s",    // sub-minute values show seconds, not "0m"
		59:   "59s",
		90:   "2m",     // 1m30s rounds to 2m
		600:  "10m",
		3599: "1h 00m", // rounds up cleanly to an hour, not "60m"
		3600: "1h 00m",
		9000: "2h 30m",
		3661: "1h 01m",
	}
	for sec, want := range cases {
		if got := formatSeconds(sec); got != want {
			t.Errorf("formatSeconds(%d) = %q, want %q", sec, got, want)
		}
	}
}

func TestResolveRange_Defaults(t *testing.T) {
	loc := time.UTC
	since, until, err := resolveRange("", "", loc, 4)
	if err != nil {
		t.Fatalf("resolveRange: %v", err)
	}
	// Default window is 7 logical days (today + 6 prior).
	if d := until.Sub(since); d < 6*24*time.Hour || d > 7*24*time.Hour {
		t.Errorf("default window = %v, want ~7 days", d)
	}
	if since.Hour() != 4 {
		t.Errorf("since = %v, want it aligned to the 04:00 boundary", since)
	}
}

func TestResolveRange_ExplicitAlignsToLogicalDay(t *testing.T) {
	loc := time.UTC
	since, until, err := resolveRange("2026-07-01", "2026-07-03", loc, 4)
	if err != nil {
		t.Fatalf("resolveRange: %v", err)
	}
	if since.Format("2006-01-02 15:04") != "2026-07-01 04:00" {
		t.Errorf("since = %v, want 2026-07-01 04:00", since)
	}
	// Inclusive end: until is the start of the logical day after 07-03.
	if until.Format("2006-01-02 15:04") != "2026-07-04 04:00" {
		t.Errorf("until = %v, want 2026-07-04 04:00", until)
	}
}

func TestResolveRange_Errors(t *testing.T) {
	loc := time.UTC
	if _, _, err := resolveRange("not-a-date", "", loc, 4); err == nil {
		t.Error("bad --since should error")
	}
	if _, _, err := resolveRange("2026-07-10", "2026-07-01", loc, 4); err == nil {
		t.Error("until before since should error")
	}
}

func TestResolveLastDays(t *testing.T) {
	loc := time.UTC
	since, until, err := resolveLastDays(7, loc, 4)
	if err != nil {
		t.Fatalf("resolveLastDays: %v", err)
	}
	if since.Hour() != 4 {
		t.Errorf("since = %v, want it aligned to the 04:00 boundary", since)
	}
	if d := until.Sub(since); d < 6*24*time.Hour || d > 7*24*time.Hour {
		t.Errorf("window = %v, want ~7 days", d)
	}
	if _, _, err := resolveLastDays(0, loc, 4); err == nil {
		t.Error("--days 0 should error")
	}
}

func TestBuildReport(t *testing.T) {
	loc := time.UTC
	base := time.Date(2026, 7, 9, 10, 0, 0, 0, loc)
	samples := []activity.Sample{
		{TS: base, State: activity.Operating},
		{TS: base.Add(15 * time.Second), State: activity.Operating},
		{TS: base.Add(30 * time.Second), State: activity.Present},
		{TS: base.Add(45 * time.Second), State: activity.Present},
	}
	rep := buildReport(samples, base, base.Add(time.Minute), 45*time.Second, loc, 4)
	if rep.SampleCount != 4 {
		t.Errorf("SampleCount = %d, want 4", rep.SampleCount)
	}
	if rep.Total.OperatingSeconds != 30 || rep.Total.PresentSeconds != 15 {
		t.Errorf("totals = %+v, want op=30 present=15", rep.Total)
	}
	if len(rep.HourOfDay) != 24 {
		t.Errorf("HourOfDay len = %d, want 24", len(rep.HourOfDay))
	}
	// The heatmap keeps wall-clock hours, whatever the logical day boundary is.
	if rep.HourOfDay[10].OperatingSeconds != 30 {
		t.Errorf("hour 10 operating = %d, want 30", rep.HourOfDay[10].OperatingSeconds)
	}
	if len(rep.Days) != 1 || rep.Days[0].Date != "2026-07-09" {
		t.Errorf("days = %+v, want one day 2026-07-09", rep.Days)
	}
}

func TestBuildTimeline_DenseDays(t *testing.T) {
	loc := time.UTC
	since := time.Date(2026, 7, 1, 4, 0, 0, 0, loc)
	until := time.Date(2026, 7, 4, 4, 0, 0, 0, loc) // exclusive end → covers 07-01..07-03
	base := time.Date(2026, 7, 2, 10, 0, 0, 0, loc)
	samples := []activity.Sample{
		{TS: base, State: activity.Operating},
		{TS: base.Add(time.Minute), State: activity.Operating},
	}
	tl := buildTimeline(samples, since, until, testParams(2*time.Minute), loc)

	if len(tl.Days) != 3 {
		t.Fatalf("got %d days, want 3 (dense 07-01..07-03)", len(tl.Days))
	}
	if tl.Days[0].Date != "2026-07-01" || tl.Days[2].Date != "2026-07-03" {
		t.Errorf("dates = %s .. %s, want 07-01 .. 07-03", tl.Days[0].Date, tl.Days[2].Date)
	}
	if tl.Days[0].HasWork || tl.Days[2].HasWork {
		t.Error("edge days should be empty (no activity)")
	}
	if !tl.Days[1].HasWork {
		t.Error("07-02 should have work")
	}
	// Empty days must carry [] arrays (non-nil) for the JSON contract, and still
	// name the instant their logical day begins so the chart can place them.
	d := tl.Days[0]
	if d.Segments == nil || d.Breaks == nil || d.Blocks == nil || d.Sessions == nil {
		t.Error("empty day arrays must be non-nil")
	}
	if want := since.Unix(); d.DayStartUnix != want {
		t.Errorf("day_start_unix = %d, want %d (07-01 04:00)", d.DayStartUnix, want)
	}
	if tl.DayStartHour != 4 || tl.SessionGapSeconds != int64((4 * time.Hour).Seconds()) {
		t.Errorf("payload must publish the derivation knobs: %+v", tl)
	}
}

func TestBuildTimeline_MidnightCrossingDay(t *testing.T) {
	loc := time.UTC
	since := time.Date(2026, 7, 9, 4, 0, 0, 0, loc)
	until := time.Date(2026, 7, 11, 4, 0, 0, 0, loc)
	var samples []activity.Sample
	for ts := time.Date(2026, 7, 9, 22, 0, 0, 0, loc); !ts.After(time.Date(2026, 7, 10, 1, 0, 0, 0, loc)); ts = ts.Add(time.Minute) {
		samples = append(samples, activity.Sample{TS: ts, State: activity.Operating})
	}
	tl := buildTimeline(samples, since, until, testParams(2*time.Minute), loc)

	d := tl.Days[0]
	if d.Date != "2026-07-09" || !d.HasWork {
		t.Fatalf("day0 = %+v, want a working 2026-07-09", d)
	}
	if d.WorkStart != "22:00" || d.WorkEnd != "01:00" {
		t.Errorf("work = %s → %s, want 22:00 → 01:00", d.WorkStart, d.WorkEnd)
	}
	// The evening ends on the next calendar day but inside this logical day, so the
	// chart column stays under 24h. A consumer that needs the crossing reads the
	// unix stamps.
	if d.WorkEndUnix <= d.WorkStartUnix {
		t.Error("work_end must be after work_start even across midnight")
	}
	if d.WorkEndUnix >= d.DayStartUnix+86400 {
		t.Error("a 22:00→01:00 evening still fits inside its logical day")
	}
	if tl.Days[1].HasWork {
		t.Error("the 10th borrowed no work from the session that began on the 9th")
	}
}

func TestPrintTimelineHuman_MarksNextDayEnd(t *testing.T) {
	loc := time.UTC
	since := time.Date(2026, 7, 9, 4, 0, 0, 0, loc)
	until := time.Date(2026, 7, 10, 4, 0, 0, 0, loc)
	var samples []activity.Sample
	for ts := time.Date(2026, 7, 9, 22, 0, 0, 0, loc); !ts.After(time.Date(2026, 7, 10, 1, 0, 0, 0, loc)); ts = ts.Add(time.Minute) {
		samples = append(samples, activity.Sample{TS: ts, State: activity.Operating})
	}
	tl := buildTimeline(samples, since, until, testParams(2*time.Minute), loc)

	var buf bytes.Buffer
	printTimelineHuman(&buf, tl, loc)
	out := buf.String()
	for _, must := range []string{"2026-07-09", "22:00 → 01:00 (+1d)", "Day starts at 04:00"} {
		if !strings.Contains(out, must) {
			t.Errorf("output missing %q:\n%s", must, out)
		}
	}
}

func TestPrintReportHuman(t *testing.T) {
	loc := time.UTC
	base := time.Date(2026, 7, 9, 10, 0, 0, 0, loc)
	samples := []activity.Sample{
		{TS: base, State: activity.Operating},
		{TS: base.Add(time.Hour), State: activity.Operating},
	}
	rep := buildReport(samples, base, base.Add(2*time.Hour), 2*time.Hour, loc, 4)
	var buf bytes.Buffer
	printReportHuman(&buf, rep)
	out := buf.String()
	for _, must := range []string{"Timezone:", "Total", "operating 1h 00m", "By day", "2026-07-09"} {
		if !strings.Contains(out, must) {
			t.Errorf("output missing %q:\n%s", must, out)
		}
	}
}

// --- now ------------------------------------------------------------------

// workSamples fills [from, to] with one-minute samples in the given state.
func workSamples(from, to time.Time, state activity.State) []activity.Sample {
	var out []activity.Sample
	for ts := from; !ts.After(to); ts = ts.Add(time.Minute) {
		out = append(out, activity.Sample{TS: ts, State: state})
	}
	return out
}

func TestBuildNow_OpenSession(t *testing.T) {
	loc := time.UTC
	start := time.Date(2026, 7, 9, 20, 44, 0, 0, loc)
	last := time.Date(2026, 7, 9, 23, 0, 0, 0, loc)
	now := last.Add(30 * time.Second)

	n := buildNow(workSamples(start, last, activity.Operating), now, time.Minute, testParams(2*time.Minute), loc)
	if n.Session == nil {
		t.Fatal("expected a session")
	}
	if !n.Session.Open || n.Session.Paused {
		t.Errorf("session should be open and not paused: %+v", n.Session)
	}
	if !n.Recording || n.State != "operating" {
		t.Errorf("state = %q recording = %v, want operating/true", n.State, n.Recording)
	}
	if n.Session.Start != "20:44" {
		t.Errorf("start = %s, want 20:44", n.Session.Start)
	}
	if n.Day.Date != "2026-07-09" {
		t.Errorf("day = %s, want 2026-07-09", n.Day.Date)
	}
}

func TestBuildNow_PausedWhileAway(t *testing.T) {
	// Thirty minutes into an absence: still the same session, but paused. No rule
	// can yet say whether this is a break or the end of the day.
	loc := time.UTC
	start := time.Date(2026, 7, 9, 20, 44, 0, 0, loc)
	last := time.Date(2026, 7, 9, 23, 0, 0, 0, loc)
	samples := workSamples(start, last, activity.Operating)
	samples = append(samples, activity.Sample{TS: last.Add(time.Minute), State: activity.Away})
	now := last.Add(30 * time.Minute)

	n := buildNow(samples, now, time.Hour, testParams(2*time.Minute), loc)
	if n.Session == nil || !n.Session.Open || !n.Session.Paused {
		t.Fatalf("expected an open, paused session: %+v", n.Session)
	}
	if n.Session.Start != "20:44" {
		t.Errorf("start = %s, want 20:44 — a session's start is never provisional", n.Session.Start)
	}
}

func TestBuildNow_ClosedAfterSessionGap(t *testing.T) {
	// The same session, observed five hours later: the absence has passed the gap,
	// so `open` has flipped. The start is unchanged.
	loc := time.UTC
	start := time.Date(2026, 7, 9, 20, 44, 0, 0, loc)
	last := time.Date(2026, 7, 9, 23, 0, 0, 0, loc)
	now := last.Add(5 * time.Hour)

	n := buildNow(workSamples(start, last, activity.Operating), now, time.Minute, testParams(2*time.Minute), loc)
	if n.Session == nil {
		t.Fatal("expected the last session to still be reported")
	}
	if n.Session.Open || n.Session.Paused {
		t.Errorf("session should be closed: %+v", n.Session)
	}
	if n.Recording {
		t.Error("a five-hour-old sample is not 'recording'")
	}
	if n.Session.Start != "20:44" {
		t.Errorf("start = %s, want 20:44", n.Session.Start)
	}
}

func TestBuildNow_NoSamples(t *testing.T) {
	loc := time.UTC
	now := time.Date(2026, 7, 10, 9, 0, 0, 0, loc)
	n := buildNow(nil, now, time.Minute, testParams(2*time.Minute), loc)
	if n.Session != nil {
		t.Errorf("session = %+v, want null", n.Session)
	}
	if n.State != "" || n.Recording {
		t.Errorf("state = %q recording = %v, want empty/false", n.State, n.Recording)
	}
	if n.Day.Date != "2026-07-10" {
		t.Errorf("day = %s, want today's logical date", n.Day.Date)
	}
}

func TestBuildNow_AllNighterFilesUnderTheNightItBegan(t *testing.T) {
	// It is 09:00 on the 10th, but the session began at 20:00 on the 9th. The day
	// figure follows the session, not the wall clock.
	loc := time.UTC
	start := time.Date(2026, 7, 9, 20, 0, 0, 0, loc)
	last := time.Date(2026, 7, 10, 9, 0, 0, 0, loc)
	now := last.Add(30 * time.Second)

	n := buildNow(workSamples(start, last, activity.Operating), now, time.Minute, testParams(2*time.Minute), loc)
	if n.Session == nil || n.Session.Start != "20:00" {
		t.Fatalf("session = %+v, want one starting 20:00", n.Session)
	}
	if n.Day.Date != "2026-07-09" {
		t.Errorf("day = %s, want 2026-07-09 (not the calendar today)", n.Day.Date)
	}
	if n.Day.ActiveSeconds != n.Session.ActiveSeconds {
		t.Errorf("day active %d should equal the only session's %d", n.Day.ActiveSeconds, n.Session.ActiveSeconds)
	}
}
