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

// Chatham changes its clocks at 03:45 local, part-way through an hour. A piece
// must not span two local hours, or it lands in the wrong hour-of-day bucket.
func TestHourBuckets_WhenTheClocksChangePartWayThroughAnHour(t *testing.T) {
	ch, err := time.LoadLocation("Pacific/Chatham")
	if err != nil {
		t.Skip("no tzdata")
	}
	// 2026-04-05 03:45 +1345 becomes 02:45 +1245.
	start := time.Date(2026, 4, 5, 2, 0, 0, 0, ch)
	if _, off := start.Zone(); off != 13*3600+45*60 {
		t.Skipf("tzdata disagrees about Chatham's offset on that night (%d)", off)
	}
	var samples []activity.Sample
	for at := start; !at.After(start.Add(3 * time.Hour)); at = at.Add(time.Minute) {
		samples = append(samples, activity.Sample{TS: at, State: activity.Operating})
	}
	params := Params{MaxGap: 2 * time.Minute}
	h := ByHourOfDay(samples, params, ch)
	// 02:00–03:00 and 03:00–03:45 before the change; 02:45–03:00 and
	// 03:00–03:45 after it.
	if h[2].Operating != 75*time.Minute || h[3].Operating != 105*time.Minute {
		t.Errorf("hour 2 = %s, hour 3 = %s; want 1h15m and 1h45m", h[2].Operating, h[3].Operating)
	}
	if got := Range(samples, params, ch); got.Operating != 3*time.Hour {
		t.Errorf("total = %s, want 3h", got.Operating)
	}
}

// Every piece the split emits lies inside one local hour, in zones that change
// by an hour, by half an hour, and part-way through an hour, across a year.
func TestEmitSplit_NoPieceSpansTwoLocalHours(t *testing.T) {
	for _, name := range []string{"America/New_York", "Australia/Lord_Howe", "Pacific/Chatham", "Asia/Kathmandu", "Asia/Tokyo"} {
		loc, err := time.LoadLocation(name)
		if err != nil {
			t.Skip("no tzdata")
		}
		start := time.Date(2026, 1, 1, 0, 7, 0, 0, loc)
		end := start.AddDate(1, 0, 0)
		var total time.Duration
		emitSplit(start, end, activity.Operating, loc, func(s time.Time, d time.Duration, _ activity.State) {
			total += d
			last := s.Add(d - time.Nanosecond)
			_, o1 := s.Zone()
			_, o2 := last.Zone()
			if d <= 0 || d > time.Hour || s.Hour() != last.Hour() || o1 != o2 {
				t.Fatalf("%s: piece %s + %s spans an hour or a clock change", name, s, d)
			}
		})
		if total != end.Sub(start) {
			t.Errorf("%s: pieces sum to %s, want %s", name, total, end.Sub(start))
		}
	}
}
