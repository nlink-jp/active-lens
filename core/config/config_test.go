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

func TestApply_DayBoundary(t *testing.T) {
	if got := Defaults("/data").DayBoundary; got != DayBoundarySession {
		t.Errorf("default day_boundary = %q, want %q", got, DayBoundarySession)
	}
	for _, want := range []string{DayBoundarySession, DayBoundaryStrict} {
		toml := "[work]\nday_boundary = \"" + want + "\"\n"
		c, err := apply(Defaults("/data"), []byte(toml))
		if err != nil {
			t.Fatalf("apply %q: %v", want, err)
		}
		if c.DayBoundary != want {
			t.Errorf("day_boundary = %q, want %q", c.DayBoundary, want)
		}
	}
}

func TestApply_DayBoundaryRejectsUnknownSpelling(t *testing.T) {
	// Silently keeping the old attribution is the failure the option exists to
	// prevent, so a typo must be loud.
	for _, bad := range []string{"Strict", "calendar", "true", ""} {
		toml := "[work]\nday_boundary = \"" + bad + "\"\n"
		if _, err := apply(Defaults("/data"), []byte(toml)); err == nil {
			t.Errorf("day_boundary = %q accepted, want an error", bad)
		}
	}
}

func TestIdleBeforeAway(t *testing.T) {
	if c := Defaults("/data"); c.IdleBeforeAwayMinutes != 10 {
		t.Errorf("default IdleBeforeAwayMinutes = %d, want 10", c.IdleBeforeAwayMinutes)
	}
	// 0 turns the rule off; a quoted number reads as a number, as every key does.
	for in, want := range map[string]int{
		"[work]\nidle_before_away_minutes = 0\n":      0,
		"[work]\nidle_before_away_minutes = 2\n":      2,
		"[work]\nidle_before_away_minutes = \"15\"\n": 15,
	} {
		c, err := apply(Defaults("/data"), []byte(in))
		if err != nil || c.IdleBeforeAwayMinutes != want {
			t.Errorf("apply(%q) = %d, %v; want %d", in, c.IdleBeforeAwayMinutes, err, want)
		}
	}
	for _, bad := range []string{
		"[work]\nidle_before_away_minutes = -1\n",
		"[work]\nidle_before_away_minutes = soon\n",
		"[work]\nidle_before_away_minutes = 1.5\n",
	} {
		if _, err := apply(Defaults("/data"), []byte(bad)); err == nil {
			t.Errorf("apply(%q) = nil error, want error", bad)
		}
	}
}

// A value large enough to overflow a time.Duration turns negative, and every
// consumer reads a negative duration as "off". NaN compares false with
// everything, so a NaN threshold would classify nothing as operating.
func TestApply_RejectsValuesThatOverflowOrAreNotNumbers(t *testing.T) {
	for _, bad := range []string{
		"[work]\nidle_before_away_minutes = 153722868\n",
		"[work]\nbreak_minutes = 9223372036854775807\n",
		"[work]\nsession_gap_minutes = 153722868\n",
		"[sampling]\ninterval_seconds = 9223372036854775807\n",
		"[sampling]\nmax_gap_seconds = 9223372037\n",
		"[sampling]\nactive_threshold_seconds = NaN\n",
		"[sampling]\nactive_threshold_seconds = Inf\n",
	} {
		if _, err := apply(Defaults("/data"), []byte(bad)); err == nil {
			t.Errorf("apply(%q) = nil error, want error", bad)
		}
	}
	// The bound itself is accepted.
	if _, err := apply(Defaults("/data"), []byte("[work]\nidle_before_away_minutes = 527040\n")); err != nil {
		t.Errorf("the upper bound was rejected: %v", err)
	}
}

func TestBriefVisit(t *testing.T) {
	if c := Defaults("/data"); c.BriefVisitMinutes != 2 {
		t.Errorf("default BriefVisitMinutes = %d, want 2", c.BriefVisitMinutes)
	}
	for in, want := range map[string]int{
		"[work]\nbrief_visit_minutes = 0\n": 0,
		"[work]\nbrief_visit_minutes = 5\n": 5,
	} {
		c, err := apply(Defaults("/data"), []byte(in))
		if err != nil || c.BriefVisitMinutes != want {
			t.Errorf("apply(%q) = %d, %v; want %d", in, c.BriefVisitMinutes, err, want)
		}
	}
	for _, bad := range []string{
		"[work]\nbrief_visit_minutes = -1\n",
		"[work]\nbrief_visit_minutes = briefly\n",
		"[work]\nbrief_visit_minutes = 153722868\n",
	} {
		if _, err := apply(Defaults("/data"), []byte(bad)); err == nil {
			t.Errorf("apply(%q) = nil error, want error", bad)
		}
	}
}

// A misspelt key used to be skipped, leaving the default in force without a
// word — the opposite of what a setting written to turn a rule off asks for.
func TestApply_RejectsUnknownKeys(t *testing.T) {
	for _, bad := range []string{
		"[work]\nbrief_visit_minute = 0\n",
		"[work]\nidle_before_away = 0\n",
		"[sampling]\ninterval = 15\n",
		"brief_visit_minutes = 0\n", // no section
	} {
		if _, err := apply(Defaults("/data"), []byte(bad)); err == nil {
			t.Errorf("apply(%q) = nil error, want error", bad)
		}
	}
	// Every documented key is accepted.
	all := "[sampling]\ninterval_seconds = 15\nactive_threshold_seconds = 30\nmax_gap_seconds = 45\n" +
		"[work]\nbreak_minutes = 10\nsession_gap_minutes = 240\nidle_before_away_minutes = 10\n" +
		"brief_visit_minutes = 2\nday_start_hour = 4\nday_boundary = \"session\"\n" +
		"[storage]\ndb_path = \"/tmp/x.db\"\n"
	if _, err := apply(Defaults("/data"), []byte(all)); err != nil {
		t.Errorf("the documented keys were rejected: %v", err)
	}
}
