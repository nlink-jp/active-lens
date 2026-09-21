package aggregate

import (
	"testing"
	"time"

	"github.com/nlink-jp/active-lens/core/activity"
)

func anchorParams() Params {
	return Params{MaxGap: 5 * time.Minute, BreakThreshold: 10 * time.Minute,
		SessionGap: 30 * time.Minute, DayStartHour: 5}
}

// run appends samples of one state, one per minute, for d.
func run(from time.Time, d time.Duration, state activity.State) []activity.Sample {
	var out []activity.Sample
	for ts := from; !ts.After(from.Add(d)); ts = ts.Add(time.Minute) {
		out = append(out, activity.Sample{TS: ts, State: state})
	}
	return out
}

func TestStartsAfterABreak(t *testing.T) {
	p := anchorParams()
	base := time.Date(2026, 9, 10, 6, 0, 0, 0, time.UTC)

	cases := map[string]struct {
		samples []activity.Sample
		want    bool
	}{
		"begins inside activity": {
			run(base, 2*time.Hour, activity.Operating), false},
		"begins after an absence at least as long as session_gap": {
			append(run(base, p.SessionGap, activity.Away),
				run(base.Add(p.SessionGap+time.Minute), time.Hour, activity.Operating)...), true},
		"begins after an absence shorter than session_gap": {
			append(run(base, 10*time.Minute, activity.Away),
				run(base.Add(11*time.Minute), time.Hour, activity.Operating)...), false},
		"no activity at all": {
			run(base, 3*time.Hour, activity.Away), true},
		"empty stream": {nil, true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if got := StartsAfterABreak(c.samples, p, time.UTC); got != c.want {
				t.Errorf("StartsAfterABreak = %v, want %v", got, c.want)
			}
		})
	}
}
