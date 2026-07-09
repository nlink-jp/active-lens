package config

import "testing"

func TestDefaults(t *testing.T) {
	c := Defaults("/data")
	if c.IntervalSeconds != 15 || c.ActiveThresholdSeconds != 30 || c.MaxGapSeconds != 45 {
		t.Errorf("defaults = %+v", c)
	}
	if c.BreakMinutes != 10 {
		t.Errorf("BreakMinutes = %d, want 10", c.BreakMinutes)
	}
	if c.DBPath != "/data/activity.db" {
		t.Errorf("DBPath = %q", c.DBPath)
	}
}

func TestApply_Overrides(t *testing.T) {
	toml := `
# a comment
[sampling]
interval_seconds = 30
active_threshold_seconds = 20

[storage]
db_path = "/custom/place.db"  # inline comment
`
	c, err := apply(Defaults("/data"), []byte(toml))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if c.IntervalSeconds != 30 {
		t.Errorf("interval = %d, want 30", c.IntervalSeconds)
	}
	// max_gap not set -> re-derives from the new interval (30*3).
	if c.MaxGapSeconds != 90 {
		t.Errorf("max_gap = %d, want 90 (derived)", c.MaxGapSeconds)
	}
	if c.ActiveThresholdSeconds != 20 {
		t.Errorf("threshold = %v, want 20", c.ActiveThresholdSeconds)
	}
	if c.DBPath != "/custom/place.db" {
		t.Errorf("db_path = %q", c.DBPath)
	}
}

func TestApply_ExplicitMaxGapWins(t *testing.T) {
	toml := "[sampling]\ninterval_seconds = 10\nmax_gap_seconds = 100\n"
	c, err := apply(Defaults("/data"), []byte(toml))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if c.MaxGapSeconds != 100 {
		t.Errorf("max_gap = %d, want 100 (explicit)", c.MaxGapSeconds)
	}
}

func TestApply_Invalid(t *testing.T) {
	for _, bad := range []string{
		"[sampling]\ninterval_seconds = 0\n",
		"[sampling]\ninterval_seconds = -5\n",
		"[sampling]\nactive_threshold_seconds = nope\n",
		"[sampling]\nmax_gap_seconds = zero\n",
	} {
		if _, err := apply(Defaults("/data"), []byte(bad)); err == nil {
			t.Errorf("apply(%q) = nil error, want error", bad)
		}
	}
}

func TestParse_Malformed(t *testing.T) {
	if _, err := parse([]byte("[unclosed\n")); err == nil {
		t.Error("unclosed section should error")
	}
	if _, err := parse([]byte("no_equals_here\n")); err == nil {
		t.Error("line without = should error")
	}
}
