package activity

import (
	"testing"

	"github.com/nlink-jp/active-lens/core/signal"
)

func TestClassify(t *testing.T) {
	const threshold = 30.0
	cases := []struct {
		name string
		snap signal.Snapshot
		want State
	}{
		{"recent input -> operating", signal.Snapshot{IdleSeconds: 5}, Operating},
		{"just under threshold -> operating", signal.Snapshot{IdleSeconds: 29.9}, Operating},
		{"at threshold -> present", signal.Snapshot{IdleSeconds: 30}, Present},
		{"long idle but on screen -> present", signal.Snapshot{IdleSeconds: 600}, Present},
		{"display asleep -> away", signal.Snapshot{IdleSeconds: 1, DisplayAsleep: true}, Away},
		{"locked -> away", signal.Snapshot{IdleSeconds: 1, Locked: true}, Away},
		{"locked beats recent input -> away", signal.Snapshot{IdleSeconds: 0, Locked: true}, Away},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Classify(c.snap, threshold); got != c.want {
				t.Errorf("Classify(%+v) = %q, want %q", c.snap, got, c.want)
			}
		})
	}
}

func TestStateValid(t *testing.T) {
	for _, s := range []State{Operating, Present, Away} {
		if !s.Valid() {
			t.Errorf("%q should be valid", s)
		}
	}
	if State("bogus").Valid() {
		t.Error("bogus state should be invalid")
	}
}
