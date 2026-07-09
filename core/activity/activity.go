// Package activity defines the three activity states and the pure classification
// of a content-free signal snapshot into one of them. Keeping this logic pure
// (no I/O, no cgo) makes the thresholding behavior fully unit-testable and lets
// stored raw samples be re-aggregated later under a different threshold.
package activity

import (
	"time"

	"github.com/nlink-jp/active-lens/core/signal"
)

// State is one of the three mutually exclusive activity states.
type State string

const (
	// Operating: the machine is awake, the display is on, and there was input
	// within the active threshold.
	Operating State = "operating"
	// Present: awake and display on, but no input within the threshold — the
	// user is likely present but only watching/reading.
	Present State = "present"
	// Away: the display is off, the screen is locked, or the system is asleep.
	Away State = "away"
)

// Valid reports whether s is one of the known states.
func (s State) Valid() bool {
	switch s {
	case Operating, Present, Away:
		return true
	}
	return false
}

// Classify maps a content-free snapshot to an activity state. activeThreshold is
// the idle cutoff (in seconds) below which the user counts as operating.
//
// Away wins over everything: a locked or display-off machine is "away"
// regardless of the idle counter. Note that true system sleep is not observed
// here (the daemon is frozen); it is inferred from sample gaps during
// aggregation.
func Classify(s signal.Snapshot, activeThreshold float64) State {
	if s.DisplayAsleep || s.Locked {
		return Away
	}
	if s.IdleSeconds < activeThreshold {
		return Operating
	}
	return Present
}

// Sample is a single recorded observation: the wall-clock instant and the state
// classified at that instant. This is the only thing persisted — no signal
// content is stored.
type Sample struct {
	TS    time.Time
	State State
}
