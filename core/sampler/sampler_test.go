package sampler

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/nlink-jp/active-lens/core/activity"
	"github.com/nlink-jp/active-lens/core/signal"
)

type fakeSampler struct {
	snap signal.Snapshot
	err  error
}

func (f fakeSampler) Snapshot() (signal.Snapshot, error) { return f.snap, f.err }

type fakeRecorder struct {
	mu      sync.Mutex
	samples []activity.Sample
	err     error
}

func (r *fakeRecorder) Append(s activity.Sample) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	r.samples = append(r.samples, s)
	return nil
}

func (r *fakeRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.samples)
}

func TestTick_ClassifiesAndRecords(t *testing.T) {
	fixed := time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC)
	rec := &fakeRecorder{}
	fs := fakeSampler{snap: signal.Snapshot{IdleSeconds: 5}} // operating
	got, err := tick(fs, rec, 30, func() time.Time { return fixed })
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if got.State != activity.Operating || !got.TS.Equal(fixed) {
		t.Errorf("sample = %+v, want operating @ fixed", got)
	}
	if rec.count() != 1 {
		t.Errorf("recorded %d, want 1", rec.count())
	}
}

func TestTick_SamplerError(t *testing.T) {
	rec := &fakeRecorder{}
	fs := fakeSampler{err: errors.New("boom")}
	if _, err := tick(fs, rec, 30, time.Now); err == nil {
		t.Error("want error from sampler")
	}
	if rec.count() != 0 {
		t.Error("nothing should be recorded on sampler error")
	}
}

func TestRun_StartsImmediatelyAndStops(t *testing.T) {
	rec := &fakeRecorder{}
	fs := fakeSampler{snap: signal.Snapshot{IdleSeconds: 100}} // present
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		Run(ctx, fs, rec, time.Hour, 30, time.Now, nil) // long interval: only the startup sample fires
		close(done)
	}()

	// The immediate startup sample should land quickly.
	deadline := time.After(2 * time.Second)
	for rec.count() == 0 {
		select {
		case <-deadline:
			t.Fatal("no startup sample within 2s")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
	if rec.count() != 1 {
		t.Errorf("recorded %d, want exactly 1 (startup only)", rec.count())
	}
}

func TestRun_ContinuesOnError(t *testing.T) {
	rec := &fakeRecorder{err: errors.New("write fail")}
	fs := fakeSampler{snap: signal.Snapshot{IdleSeconds: 1}}
	var errCount int
	var mu sync.Mutex
	ctx, cancel := context.WithCancel(context.Background())
	go Run(ctx, fs, rec, 5*time.Millisecond, 30, time.Now, func(error) {
		mu.Lock()
		errCount++
		mu.Unlock()
	})
	time.Sleep(50 * time.Millisecond)
	cancel()
	mu.Lock()
	defer mu.Unlock()
	if errCount < 2 {
		t.Errorf("onError called %d times, want it to keep firing (>=2)", errCount)
	}
}
