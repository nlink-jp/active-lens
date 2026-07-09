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

	days := Timeline(samples, 120*time.Second, 10*time.Minute, utc)
	if len(days) != 1 {
		t.Fatalf("got %d days, want 1", len(days))
	}
	d := days[0]
	if !d.HasWork {
		t.Fatal("expected HasWork")
	}
	if d.WorkStart.Format("15:04") != "09:00" {
		t.Errorf("WorkStart = %s, want 09:00", d.WorkStart.Format("15:04"))
	}
	if d.WorkEnd.Format("15:04") != "18:00" {
		t.Errorf("WorkEnd = %s, want 18:00", d.WorkEnd.Format("15:04"))
	}
	// Exactly one lunch break (~28 min: 12:02 after the maxGap cap → 12:30).
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
}

func TestTimeline_ShortGapNotABreak(t *testing.T) {
	// A 5-minute away gap with a 10-minute threshold is folded into work, not a break.
	samples := []activity.Sample{
		samp("2026-07-09 09:00:00", activity.Operating),
		samp("2026-07-09 09:00:15", activity.Operating),
		samp("2026-07-09 09:05:15", activity.Operating), // 5-min gap -> away, below threshold
		samp("2026-07-09 09:05:30", activity.Operating),
	}
	days := Timeline(samples, 30*time.Second, 10*time.Minute, utc)
	if len(days) != 1 || len(days[0].Breaks) != 0 {
		t.Errorf("expected no breaks for a sub-threshold gap, got %+v", days[0].Breaks)
	}
}

func TestTimeline_NoActivityDay(t *testing.T) {
	// All-away day (locked overnight): no work session.
	samples := []activity.Sample{
		samp("2026-07-09 02:00:00", activity.Away),
		samp("2026-07-09 02:00:15", activity.Away),
	}
	days := Timeline(samples, 30*time.Second, 10*time.Minute, utc)
	if len(days) != 1 || days[0].HasWork {
		t.Errorf("expected a day with no work, got %+v", days)
	}
}

func TestTimeline_SplitsAcrossMidnight(t *testing.T) {
	// Active spanning midnight lands on both days.
	samples := []activity.Sample{
		samp("2026-07-09 23:59:00", activity.Operating),
		samp("2026-07-10 00:01:00", activity.Operating),
	}
	days := Timeline(samples, 5*time.Minute, 10*time.Minute, utc)
	if len(days) != 2 {
		t.Fatalf("got %d days, want 2", len(days))
	}
	if days[0].Date != "2026-07-09" || days[1].Date != "2026-07-10" {
		t.Errorf("dates = %s, %s", days[0].Date, days[1].Date)
	}
}
