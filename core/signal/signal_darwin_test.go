//go:build darwin

package signal

import "testing"

// TestLiveSnapshot exercises the real CoreGraphics path. It asserts only
// invariants that hold regardless of the machine's current state, so it is safe
// in any environment: idle seconds are non-negative and the call succeeds.
func TestLiveSnapshot(t *testing.T) {
	s, err := NewSampler().Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if s.IdleSeconds < 0 {
		t.Errorf("IdleSeconds = %v, want >= 0", s.IdleSeconds)
	}
	t.Logf("live snapshot: idle=%.1fs displayAsleep=%v locked=%v",
		s.IdleSeconds, s.DisplayAsleep, s.Locked)
}
