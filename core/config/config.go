// Package config resolves runtime configuration from a small, optional TOML
// file overlaid on built-in defaults. It uses a minimal hand-rolled parser
// (flat sectioned key = value) to avoid an external dependency — the config
// surface is tiny and fully controlled.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Config is the fully-resolved runtime configuration.
type Config struct {
	// IntervalSeconds is how often the daemon samples.
	IntervalSeconds int
	// ActiveThresholdSeconds is the idle cutoff below which the user is
	// "operating".
	ActiveThresholdSeconds float64
	// MaxGapSeconds caps how much an inter-sample interval credits to a state;
	// excess is treated as system-sleep (away).
	MaxGapSeconds int
	// BreakMinutes is the minimum away span (in minutes) counted as a work break
	// in the timeline; shorter away gaps are folded into continuous work.
	BreakMinutes int
	// SessionGapMinutes is the away span (in minutes) that ends a work session.
	// It must exceed BreakMinutes, otherwise every break would also terminate the
	// session it sits in.
	SessionGapMinutes int
	// DayStartHour (0..23) is the local hour a logical day begins at. Activity
	// before it is filed under the previous day, so an evening that runs past
	// midnight stays on the day it started.
	DayStartHour int
	// IdleBeforeAwayMinutes is the longest no-input span (in minutes) that is
	// counted as away when the display then goes off and stays off: the absence
	// began when input stopped, not when the display timer fired. Match it to
	// the display-sleep timeout. Zero disables the rule. See ADR 0003.
	IdleBeforeAwayMinutes int
	// BriefVisitMinutes is the longest run of activity (in minutes) that, lying
	// between two away spans each at least BreakMinutes long, is not counted as
	// work: a touch in the middle of an absence. Zero disables the rule. See
	// ADR 0004.
	BriefVisitMinutes int
	// DayBoundary selects what a logical day boundary does to a session in
	// progress: DayBoundarySession files the session whole under the day it
	// started in, DayBoundaryStrict ends it at the boundary and opens a new one
	// there. See docs/{en,ja}/adr/0002.
	DayBoundary string
	// DBPath is where raw samples are stored.
	DBPath string
}

// Day boundary modes. These are the accepted spellings of work.day_boundary.
const (
	// DayBoundarySession files a session, whole, under the logical day it started
	// in — an evening that runs past the boundary stays one piece of work.
	DayBoundarySession = "session"
	// DayBoundaryStrict ends a session at every logical day boundary, so each
	// day is credited exactly the work that falls inside it. This is the mode for
	// a workplace whose working day is defined by someone other than the user.
	DayBoundaryStrict = "strict"
)

// Defaults returns the built-in defaults. dataDir seeds DBPath; MaxGap derives
// from the interval.
func Defaults(dataDir string) Config {
	const interval = 15
	return Config{
		IntervalSeconds:        interval,
		ActiveThresholdSeconds: 30,
		MaxGapSeconds:          interval * 3,
		BreakMinutes:           10,
		SessionGapMinutes:      240,
		IdleBeforeAwayMinutes:  10,
		BriefVisitMinutes:      2,
		DayStartHour:           4,
		DayBoundary:            DayBoundarySession,
		DBPath:                 filepath.Join(dataDir, "activity.db"),
	}
}

// Load resolves config from path (may be absent) overlaid on Defaults(dataDir).
// Unset keys keep their default; if the interval is overridden but max_gap is
// not, max_gap re-derives from the new interval.
func Load(path, dataDir string) (Config, error) {
	cfg := Defaults(dataDir)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}
	return apply(cfg, data)
}

// Upper bounds for durations given in the config. A year is far past any
// sensible value; the point is that the number, multiplied into a
// time.Duration, cannot overflow into a negative one — which every consumer
// would read as "off" or "zero" without a word.
const (
	maxSeconds = 366 * 24 * 60 * 60
	maxMinutes = 366 * 24 * 60
)

// apply overlays parsed TOML onto cfg. Exposed via Load; separated for testing.
func apply(cfg Config, data []byte) (Config, error) {
	kv, err := parse(data)
	if err != nil {
		return cfg, err
	}
	if v, ok := kv["sampling.interval_seconds"]; ok {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 || n > maxSeconds {
			return cfg, fmt.Errorf("sampling.interval_seconds: want integer 1..%d, got %q", maxSeconds, v)
		}
		cfg.IntervalSeconds = n
		// Keep the derived default in sync unless max_gap is set explicitly.
		if _, set := kv["sampling.max_gap_seconds"]; !set {
			cfg.MaxGapSeconds = n * 3
		}
	}
	if v, ok := kv["sampling.active_threshold_seconds"]; ok {
		f, err := strconv.ParseFloat(v, 64)
		// !(f >= 0) also rejects NaN, which compares false with everything and
		// would make no sample ever classify as operating.
		if err != nil || !(f >= 0) || f > maxSeconds {
			return cfg, fmt.Errorf("sampling.active_threshold_seconds: want number 0..%d, got %q", maxSeconds, v)
		}
		cfg.ActiveThresholdSeconds = f
	}
	if v, ok := kv["sampling.max_gap_seconds"]; ok {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 || n > maxSeconds {
			return cfg, fmt.Errorf("sampling.max_gap_seconds: want integer 1..%d, got %q", maxSeconds, v)
		}
		cfg.MaxGapSeconds = n
	}
	if v, ok := kv["work.break_minutes"]; ok {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 || n > maxMinutes {
			return cfg, fmt.Errorf("work.break_minutes: want integer 1..%d, got %q", maxMinutes, v)
		}
		cfg.BreakMinutes = n
	}
	if v, ok := kv["work.session_gap_minutes"]; ok {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 || n > maxMinutes {
			return cfg, fmt.Errorf("work.session_gap_minutes: want integer 1..%d, got %q", maxMinutes, v)
		}
		cfg.SessionGapMinutes = n
	}
	if v, ok := kv["work.idle_before_away_minutes"]; ok {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > maxMinutes {
			return cfg, fmt.Errorf("work.idle_before_away_minutes: want integer 0..%d, got %q", maxMinutes, v)
		}
		cfg.IdleBeforeAwayMinutes = n
	}
	if v, ok := kv["work.brief_visit_minutes"]; ok {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > maxMinutes {
			return cfg, fmt.Errorf("work.brief_visit_minutes: want integer 0..%d, got %q", maxMinutes, v)
		}
		cfg.BriefVisitMinutes = n
	}
	if v, ok := kv["work.day_start_hour"]; ok {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > 23 {
			return cfg, fmt.Errorf("work.day_start_hour: want integer 0..23, got %q", v)
		}
		cfg.DayStartHour = n
	}
	if v, ok := kv["work.day_boundary"]; ok {
		// A typo that silently kept the old attribution is exactly the failure this
		// option exists to prevent, so an unknown spelling is an error.
		if v != DayBoundarySession && v != DayBoundaryStrict {
			return cfg, fmt.Errorf("work.day_boundary: want %q or %q, got %q",
				DayBoundarySession, DayBoundaryStrict, v)
		}
		cfg.DayBoundary = v
	}
	if v, ok := kv["storage.db_path"]; ok && v != "" {
		cfg.DBPath = expandHome(v)
	}
	// A session gap at or below the break threshold would make every break end the
	// session it sits in, so no session could ever contain a break.
	if cfg.SessionGapMinutes <= cfg.BreakMinutes {
		return cfg, fmt.Errorf("work.session_gap_minutes (%d) must exceed work.break_minutes (%d)",
			cfg.SessionGapMinutes, cfg.BreakMinutes)
	}
	return cfg, nil
}

// expandHome expands a leading ~ to the user's home directory.
func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(p, "~"), "/"))
		}
	}
	return p
}

// parse reads a flat sectioned TOML into "section.key" -> value. Only string and
// scalar values are supported; surrounding quotes are stripped and trailing
// unquoted comments removed.
func parse(data []byte) (map[string]string, error) {
	out := map[string]string{}
	section := ""
	for i, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			if !strings.HasSuffix(line, "]") {
				return nil, fmt.Errorf("line %d: malformed section header %q", i+1, line)
			}
			section = strings.TrimSpace(line[1 : len(line)-1])
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			return nil, fmt.Errorf("line %d: expected key = value, got %q", i+1, line)
		}
		key := strings.TrimSpace(line[:eq])
		val := parseValue(strings.TrimSpace(line[eq+1:]))
		if section != "" {
			key = section + "." + key
		}
		out[key] = val
	}
	return out, nil
}

// parseValue returns the value's content: for a quoted value it returns the text
// inside the quotes (ignoring any trailing inline comment); for an unquoted value
// it trims a trailing # comment.
func parseValue(v string) string {
	if len(v) > 0 && (v[0] == '"' || v[0] == '\'') {
		if end := strings.IndexByte(v[1:], v[0]); end >= 0 {
			return v[1 : 1+end]
		}
		// Unterminated quote: fall through to comment-trimming.
	}
	if h := strings.IndexByte(v, '#'); h >= 0 {
		v = strings.TrimSpace(v[:h])
	}
	return v
}
