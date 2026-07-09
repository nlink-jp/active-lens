// Package sampler drives the daemon's periodic loop: every interval it reads a
// content-free snapshot, classifies it into an activity state, and appends the
// resulting sample to a recorder. Dependencies (the signal source, the
// recorder, the clock) are injected so the loop is unit-testable without real
// hardware or wall-clock waits.
package sampler

import (
	"context"
	"time"

	"github.com/nlink-jp/active-lens/core/activity"
	"github.com/nlink-jp/active-lens/core/signal"
)

// Recorder persists samples. store.Store satisfies this.
type Recorder interface {
	Append(activity.Sample) error
}

// tick performs one sample: read the snapshot, classify it at the given
// threshold, stamp it with now(), and record it.
func tick(s signal.Sampler, rec Recorder, threshold float64, now func() time.Time) (activity.Sample, error) {
	snap, err := s.Snapshot()
	if err != nil {
		return activity.Sample{}, err
	}
	sample := activity.Sample{TS: now(), State: activity.Classify(snap, threshold)}
	if err := rec.Append(sample); err != nil {
		return sample, err
	}
	return sample, nil
}

// Run samples immediately, then every interval until ctx is cancelled. A tick
// error is reported to onError (if non-nil) and the loop continues — a transient
// read/write failure should not kill long-running collection.
func Run(ctx context.Context, s signal.Sampler, rec Recorder, interval time.Duration, threshold float64, now func() time.Time, onError func(error)) {
	if now == nil {
		now = time.Now
	}
	sampleOnce := func() {
		if _, err := tick(s, rec, threshold, now); err != nil && onError != nil {
			onError(err)
		}
	}
	sampleOnce() // record at startup so a short session still leaves a mark
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			sampleOnce()
		}
	}
}
