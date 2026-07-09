package aggregate

import (
	"testing"
	"time"

	"github.com/nlink-jp/active-lens/core/activity"
)

var utc = time.UTC

func ts(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04:05", s, utc)
	if err != nil {
		panic(err)
	}
	return t
}

func samp(s string, st activity.State) activity.Sample {
	return activity.Sample{TS: ts(s), State: st}
}

func TestRange_SimpleAttribution(t *testing.T) {
	// Three 15s intervals: operating, operating, present.
	samples := []activity.Sample{
		samp("2026-07-09 10:00:00", activity.Operating),
		samp("2026-07-09 10:00:15", activity.Operating),
		samp("2026-07-09 10:00:30", activity.Present),
		samp("2026-07-09 10:00:45", activity.Present),
	}
	got := Range(samples, 45*time.Second, utc)
	// Intervals attributed to the START state: op, op, present = 30s op, 15s present.
	if got.Operating != 30*time.Second {
		t.Errorf("Operating = %v, want 30s", got.Operating)
	}
	if got.Present != 15*time.Second {
		t.Errorf("Present = %v, want 15s", got.Present)
	}
	if got.Away != 0 {
		t.Errorf("Away = %v, want 0", got.Away)
	}
}

func TestRange_SleepGapBecomesAway(t *testing.T) {
	// A 2-hour gap between two samples with maxGap=45s. The first 45s is credited
	// to the start state (operating), the remaining ~2h to away (system asleep).
	samples := []activity.Sample{
		samp("2026-07-09 10:00:00", activity.Operating),
		samp("2026-07-09 12:00:00", activity.Operating),
	}
	got := Range(samples, 45*time.Second, utc)
	if got.Operating != 45*time.Second {
		t.Errorf("Operating = %v, want 45s", got.Operating)
	}
	wantAway := 2*time.Hour - 45*time.Second
	if got.Away != wantAway {
		t.Errorf("Away = %v, want %v", got.Away, wantAway)
	}
	// Full wall clock is accounted for.
	if got.Total() != 2*time.Hour {
		t.Errorf("Total = %v, want 2h", got.Total())
	}
}

func TestByHourOfDay_SplitsAcrossBoundary(t *testing.T) {
	// One sample at 10:59:30, next at 11:00:30 — a 60s operating interval that
	// straddles the hour boundary: 30s in hour 10, 30s in hour 11.
	samples := []activity.Sample{
		samp("2026-07-09 10:59:30", activity.Operating),
		samp("2026-07-09 11:00:30", activity.Operating),
	}
	buckets := ByHourOfDay(samples, 90*time.Second, utc)
	if buckets[10].Operating != 30*time.Second {
		t.Errorf("hour 10 Operating = %v, want 30s", buckets[10].Operating)
	}
	if buckets[11].Operating != 30*time.Second {
		t.Errorf("hour 11 Operating = %v, want 30s", buckets[11].Operating)
	}
}

func TestByHourOfDay_SleepSpansManyHours(t *testing.T) {
	// A 3-hour sleep gap must be spread across hour buckets, not lumped into one.
	samples := []activity.Sample{
		samp("2026-07-09 01:00:00", activity.Operating),
		samp("2026-07-09 04:00:00", activity.Operating),
	}
	buckets := ByHourOfDay(samples, 60*time.Second, utc)
	// Hour 1: 60s operating + 3540s away. Hours 2,3: full hour away.
	if buckets[1].Operating != 60*time.Second {
		t.Errorf("hour 1 Operating = %v, want 60s", buckets[1].Operating)
	}
	if buckets[1].Away != time.Hour-60*time.Second {
		t.Errorf("hour 1 Away = %v, want %v", buckets[1].Away, time.Hour-60*time.Second)
	}
	if buckets[2].Away != time.Hour {
		t.Errorf("hour 2 Away = %v, want 1h", buckets[2].Away)
	}
	if buckets[3].Away != time.Hour {
		t.Errorf("hour 3 Away = %v, want 1h", buckets[3].Away)
	}
}

func TestByDay_SplitsAcrossMidnight(t *testing.T) {
	// Gap straddling midnight: 30 min before, 30 min after (with a large maxGap so
	// it is all credited to the start state).
	samples := []activity.Sample{
		samp("2026-07-09 23:30:00", activity.Present),
		samp("2026-07-10 00:30:00", activity.Present),
	}
	days := ByDay(samples, 2*time.Hour, utc)
	if len(days) != 2 {
		t.Fatalf("got %d days, want 2", len(days))
	}
	if days[0].Date != "2026-07-09" || days[0].Totals.Present != 30*time.Minute {
		t.Errorf("day0 = %+v, want 2026-07-09 present=30m", days[0])
	}
	if days[1].Date != "2026-07-10" || days[1].Totals.Present != 30*time.Minute {
		t.Errorf("day1 = %+v, want 2026-07-10 present=30m", days[1])
	}
}

func TestWalk_UnsortedAndDegenerate(t *testing.T) {
	// Out-of-order input is sorted; a single sample yields nothing.
	if got := Range([]activity.Sample{samp("2026-07-09 10:00:00", activity.Operating)}, time.Minute, utc); got.Total() != 0 {
		t.Errorf("single sample Total = %v, want 0", got.Total())
	}
	unsorted := []activity.Sample{
		samp("2026-07-09 10:00:15", activity.Present),
		samp("2026-07-09 10:00:00", activity.Operating),
	}
	got := Range(unsorted, time.Minute, utc)
	if got.Operating != 15*time.Second || got.Present != 0 {
		t.Errorf("unsorted Range = %+v, want operating=15s present=0", got)
	}
}
