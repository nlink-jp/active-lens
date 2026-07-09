// Package signal reads coarse, content-free activity signals from the OS: how
// long since the last input event, whether the display is asleep, and whether
// the screen is locked. It never observes *what* the user did — no keystrokes,
// no mouse coordinates, no app identity.
//
// The CoreGraphics dependency is confined to the darwin build. The Sampler
// interface is the seam that keeps the sampling loop and aggregation pure and
// testable on any platform.
package signal

import "errors"

// ErrUnsupported is returned by the Sampler on non-darwin platforms.
// active-lens targets darwin/arm64 only; the stub exists so the package stays
// buildable and vettable elsewhere.
var ErrUnsupported = errors.New("signal: activity sampling is only supported on macOS (darwin)")

// Snapshot is a single content-free reading of the machine's input/presence
// state. It carries no keystrokes, mouse coordinates, or app identity — only
// elapsed-idle time and two boolean presence flags.
type Snapshot struct {
	// IdleSeconds is seconds since the last keyboard/mouse input event.
	IdleSeconds float64
	// DisplayAsleep reports whether the main display is powered down.
	DisplayAsleep bool
	// Locked reports whether the login session screen is locked.
	Locked bool
}

// Sampler reads a Snapshot. It isolates the cgo/CoreGraphics dependency so the
// sampling loop and aggregation can be exercised with a fake.
type Sampler interface {
	Snapshot() (Snapshot, error)
}
