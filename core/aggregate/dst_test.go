package aggregate

import (
	"testing"
	"time"

	"github.com/nlink-jp/active-lens/core/activity"
)

// When clocks fall back, a local hour happens twice. Splitting at local hour
// boundaries must still advance through the repeated hour.
func TestTotals_AcrossDaylightSavingFallBack(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no tzdata")
	}
	start := time.Date(2026, 11, 1, 0, 30, 0, 0, ny) // EDT; 02:00 EDT -> 01:00 EST
	var samples []activity.Sample
	for at := start; !at.After(start.Add(4 * time.Hour)); at = at.Add(time.Minute) {
		samples = append(samples, activity.Sample{TS: at, State: activity.Operating})
	}
	done := make(chan Totals, 1)
	go func() { done <- Range(samples, Params{MaxGap: 2 * time.Minute}, ny) }()
	select {
	case got := <-done:
		if got.Operating != 4*time.Hour {
			t.Errorf("operating = %s, want 4h", got.Operating)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Range did not return: the hour split does not advance through the repeated hour")
	}
	// The repeated 01:00 hour holds two hours of wall time.
	if h := ByHourOfDay(samples, Params{MaxGap: 2 * time.Minute}, ny); h[1].Operating != 2*time.Hour {
		t.Errorf("hour 1 = %s, want 2h (it happens twice)", h[1].Operating)
	}
}
