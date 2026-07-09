package cmd

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/nlink-jp/active-lens/core/activity"
)

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
	since, until, err := resolveRange("", "", loc)
	if err != nil {
		t.Fatalf("resolveRange: %v", err)
	}
	// Default window is 7 days (today + 6 prior).
	if d := until.Sub(since); d < 6*24*time.Hour || d > 7*24*time.Hour {
		t.Errorf("default window = %v, want ~7 days", d)
	}
}

func TestResolveRange_Explicit(t *testing.T) {
	loc := time.UTC
	since, until, err := resolveRange("2026-07-01", "2026-07-03", loc)
	if err != nil {
		t.Fatalf("resolveRange: %v", err)
	}
	if since.Format("2006-01-02 15:04") != "2026-07-01 00:00" {
		t.Errorf("since = %v", since)
	}
	// Inclusive end: until is midnight after 07-03.
	if until.Format("2006-01-02 15:04") != "2026-07-04 00:00" {
		t.Errorf("until = %v, want 2026-07-04 00:00", until)
	}
}

func TestResolveRange_Errors(t *testing.T) {
	loc := time.UTC
	if _, _, err := resolveRange("not-a-date", "", loc); err == nil {
		t.Error("bad --since should error")
	}
	if _, _, err := resolveRange("2026-07-10", "2026-07-01", loc); err == nil {
		t.Error("until before since should error")
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
	rep := buildReport(samples, base, base.Add(time.Minute), 45*time.Second, loc)
	if rep.SampleCount != 4 {
		t.Errorf("SampleCount = %d, want 4", rep.SampleCount)
	}
	if rep.Total.OperatingSeconds != 30 || rep.Total.PresentSeconds != 15 {
		t.Errorf("totals = %+v, want op=30 present=15", rep.Total)
	}
	if len(rep.HourOfDay) != 24 {
		t.Errorf("HourOfDay len = %d, want 24", len(rep.HourOfDay))
	}
	if rep.HourOfDay[10].OperatingSeconds != 30 {
		t.Errorf("hour 10 operating = %d, want 30", rep.HourOfDay[10].OperatingSeconds)
	}
	if len(rep.Days) != 1 || rep.Days[0].Date != "2026-07-09" {
		t.Errorf("days = %+v, want one day 2026-07-09", rep.Days)
	}
}

func TestBuildTimeline_DenseDays(t *testing.T) {
	loc := time.UTC
	since := time.Date(2026, 7, 1, 0, 0, 0, 0, loc)
	until := time.Date(2026, 7, 4, 0, 0, 0, 0, loc) // exclusive end → covers 07-01..07-03
	base := time.Date(2026, 7, 2, 10, 0, 0, 0, loc)
	samples := []activity.Sample{
		{TS: base, State: activity.Operating},
		{TS: base.Add(time.Minute), State: activity.Operating},
	}
	tl := buildTimeline(samples, since, until, 2*time.Minute, 10*time.Minute, loc)

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
	// Empty days must carry [] arrays (non-nil) for the JSON contract.
	if tl.Days[0].Segments == nil || tl.Days[0].Breaks == nil {
		t.Error("empty day arrays must be non-nil")
	}
}

func TestPrintReportHuman(t *testing.T) {
	loc := time.UTC
	base := time.Date(2026, 7, 9, 10, 0, 0, 0, loc)
	samples := []activity.Sample{
		{TS: base, State: activity.Operating},
		{TS: base.Add(time.Hour), State: activity.Operating},
	}
	rep := buildReport(samples, base, base.Add(2*time.Hour), 2*time.Hour, loc)
	var buf bytes.Buffer
	printReportHuman(&buf, rep)
	out := buf.String()
	for _, must := range []string{"Timezone:", "Total", "operating 1h 00m", "By day", "2026-07-09"} {
		if !strings.Contains(out, must) {
			t.Errorf("output missing %q:\n%s", must, out)
		}
	}
}
