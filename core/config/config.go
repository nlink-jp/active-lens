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
	// DBPath is where raw samples are stored.
	DBPath string
}

// Defaults returns the built-in defaults. dataDir seeds DBPath; MaxGap derives
// from the interval.
func Defaults(dataDir string) Config {
	const interval = 15
	return Config{
		IntervalSeconds:        interval,
		ActiveThresholdSeconds: 30,
		MaxGapSeconds:          interval * 3,
		BreakMinutes:           10,
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

// apply overlays parsed TOML onto cfg. Exposed via Load; separated for testing.
func apply(cfg Config, data []byte) (Config, error) {
	kv, err := parse(data)
	if err != nil {
		return cfg, err
	}
	if v, ok := kv["sampling.interval_seconds"]; ok {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return cfg, fmt.Errorf("sampling.interval_seconds: want positive integer, got %q", v)
		}
		cfg.IntervalSeconds = n
		// Keep the derived default in sync unless max_gap is set explicitly.
		if _, set := kv["sampling.max_gap_seconds"]; !set {
			cfg.MaxGapSeconds = n * 3
		}
	}
	if v, ok := kv["sampling.active_threshold_seconds"]; ok {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || f < 0 {
			return cfg, fmt.Errorf("sampling.active_threshold_seconds: want non-negative number, got %q", v)
		}
		cfg.ActiveThresholdSeconds = f
	}
	if v, ok := kv["sampling.max_gap_seconds"]; ok {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return cfg, fmt.Errorf("sampling.max_gap_seconds: want positive integer, got %q", v)
		}
		cfg.MaxGapSeconds = n
	}
	if v, ok := kv["work.break_minutes"]; ok {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return cfg, fmt.Errorf("work.break_minutes: want positive integer, got %q", v)
		}
		cfg.BreakMinutes = n
	}
	if v, ok := kv["storage.db_path"]; ok && v != "" {
		cfg.DBPath = expandHome(v)
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
