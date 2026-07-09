//go:build darwin

package signal

/*
#cgo LDFLAGS: -framework CoreGraphics -framework CoreFoundation
#include <CoreGraphics/CoreGraphics.h>
#include <CoreFoundation/CoreFoundation.h>

// CGSessionCopyCurrentDictionary is part of the CoreGraphics framework but is
// not always exposed by the umbrella header, so declare it explicitly. It
// returns the current login session's state dictionary (or NULL).
extern CFDictionaryRef CGSessionCopyCurrentDictionary(void);

// al_idle_seconds returns seconds since the last HID input event (keyboard or
// mouse) across the whole session. It reads elapsed time only, never content.
static double al_idle_seconds(void) {
	return CGEventSourceSecondsSinceLastEventType(
		kCGEventSourceStateCombinedSessionState, kCGAnyInputEventType);
}

// al_display_asleep reports whether the main display is powered down.
static int al_display_asleep(void) {
	return CGDisplayIsAsleep(CGMainDisplayID()) ? 1 : 0;
}

// al_screen_locked reports whether the session screen is locked, read from the
// session dictionary's CGSSessionScreenIsLocked flag.
static int al_screen_locked(void) {
	int locked = 0;
	CFDictionaryRef d = CGSessionCopyCurrentDictionary();
	if (d != NULL) {
		const void *v = CFDictionaryGetValue(d, CFSTR("CGSSessionScreenIsLocked"));
		if (v != NULL && CFBooleanGetValue((CFBooleanRef)v)) {
			locked = 1;
		}
		CFRelease(d);
	}
	return locked;
}
*/
import "C"

// cgSampler reads presence signals through CoreGraphics.
type cgSampler struct{}

// NewSampler returns the CoreGraphics-backed Sampler.
func NewSampler() Sampler { return cgSampler{} }

func (cgSampler) Snapshot() (Snapshot, error) {
	return Snapshot{
		IdleSeconds:   float64(C.al_idle_seconds()),
		DisplayAsleep: C.al_display_asleep() != 0,
		Locked:        C.al_screen_locked() != 0,
	}, nil
}
