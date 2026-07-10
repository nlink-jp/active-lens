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
	if c.SessionGapMinutes != 240 || c.DayStartHour != 4 {
		t.Errorf("session gap / day start = %d / %d, want 240 / 4", c.SessionGapMinutes, c.DayStartHour)
	}
	if c.DBPath != "/data/activity.db" {
		t.Errorf("DBPath = %q", c.DBPath)
	}
}

func TestApply_WorkOverrides(t *testing.T) {
	toml := "[work]\nbreak_minutes = 5\nsession_gap_minutes = 90\nday_start_hour = 0\n"
	c, err := apply(Defaults("/data"), []byte(toml))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if c.BreakMinutes != 5 || c.SessionGapMinutes != 90 || c.DayStartHour != 0 {
		t.Errorf("work config = %+v", c)
	}
}

func TestApply_SessionGapMustExceedBreak(t *testing.T) {
	// Otherwise every break would also end the session it sits in, and no session
	// could ever contain one.
	for _, bad := range []string{
		"[work]\nbreak_minutes = 30\nsession_gap_minutes = 30\n",
		"[work]\nbreak_minutes = 30\nsession_gap_minutes = 10\n",
		"[work]\nbreak_minutes = 600\n", // above the 240-minute default gap
	} {
		if _, err := apply(Defaults("/data"), []byte(bad)); err == nil {
			t.Errorf("apply(%q) = nil error, want error", bad)
		}
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
		"[work]\nday_start_hour = 24\n",
		"[work]\nday_start_hour = -1\n",
		"[work]\nsession_gap_minutes = 0\n",
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
