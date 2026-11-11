package cmd

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	ossignal "os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/nlink-jp/active-lens/core/activity"
	"github.com/nlink-jp/active-lens/core/aggregate"
	"github.com/nlink-jp/active-lens/core/config"
	"github.com/nlink-jp/active-lens/core/platform"
	"github.com/nlink-jp/active-lens/core/sampler"
	"github.com/nlink-jp/active-lens/core/signal"
	"github.com/nlink-jp/active-lens/core/store"
)

// loadConfig resolves the config file and data dir, then loads config.
func loadConfig() (config.Config, error) {
	dataDir, err := platform.DataDir()
	if err != nil {
		return config.Config{}, err
	}
	cfgPath, err := platform.ConfigFilePath()
	if err != nil {
		return config.Config{}, err
	}
	return config.Load(cfgPath, dataDir)
}

// paramsOf lifts the derivation knobs out of config. They are applied here, not
// at record time, so the stored history re-derives whenever they change.
func paramsOf(cfg config.Config) aggregate.Params {
	return aggregate.Params{
		MaxGap:         time.Duration(cfg.MaxGapSeconds) * time.Second,
		BreakThreshold: time.Duration(cfg.BreakMinutes) * time.Minute,
		SessionGap:     time.Duration(cfg.SessionGapMinutes) * time.Minute,
		DayStartHour:   cfg.DayStartHour,
		DayBoundary:    aggregate.DayBoundary(cfg.DayBoundary),
		IdleBeforeAway: time.Duration(cfg.IdleBeforeAwayMinutes) * time.Minute,
		BriefVisit:     time.Duration(cfg.BriefVisitMinutes) * time.Minute,
	}
}

// idleBeforeAwayGloss says what the idle_before_away setting does to the
// numbers, since "10m" alone does not tell a reader that active time moved.
func idleBeforeAwayGloss(minutes int) string {
	if minutes <= 0 {
		return "off (no input before the display goes off stays present)"
	}
	return fmt.Sprintf("%dm (no input up to this long before the display goes off counts as away)", minutes)
}

// briefVisitGloss says what the brief_visit setting does to the work log.
func briefVisitGloss(minutes, breakMinutes int) string {
	if minutes <= 0 {
		return "off (any activity between two breaks is work)"
	}
	return fmt.Sprintf("%dm (activity up to this long between two away spans of %dm or more is not work)",
		minutes, breakMinutes)
}

// dayBoundaryGloss explains a day_boundary value in one clause, so `doctor`
// answers "which day does work past the boundary count against" without the
// reader going to the config docs.
func dayBoundaryGloss(mode string) string {
	if mode == config.DayBoundaryStrict {
		return "work past the boundary counts against the new day"
	}
	return "a session stays on the day it started"
}

// staleAfter is how long a sample drought means recording has stopped.
func staleAfter(cfg config.Config) time.Duration {
	if d := time.Duration(cfg.IntervalSeconds*4) * time.Second; d > time.Minute {
		return d
	}
	return time.Minute
}

// --- daemon ---------------------------------------------------------------

func runDaemon(args []string) error {
	fs := flag.NewFlagSet("daemon", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer st.Close()

	ctx, stop := ossignal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	interval := time.Duration(cfg.IntervalSeconds) * time.Second
	fmt.Fprintf(os.Stderr, "active-lens daemon: sampling every %s, threshold %.0fs, db %s\n",
		interval, cfg.ActiveThresholdSeconds, cfg.DBPath)

	sampler.Run(ctx, signal.NewSampler(), st, interval, cfg.ActiveThresholdSeconds,
		time.Now, func(e error) {
			fmt.Fprintf(os.Stderr, "active-lens daemon: sample error: %v\n", e)
		})
	fmt.Fprintln(os.Stderr, "active-lens daemon: stopped")
	return nil
}

// --- now ------------------------------------------------------------------

// lookback is the first step back before a window, and the one reported to the
// user. A single session is bounded at 48h by the day-boundary cut and the
// absence that opens one is at most a session_gap longer, so this contains the
// session the window's first instant belongs to — but not the *chain* of
// sessions a Mac kept awake produces, each cut at a boundary and the next
// beginning there. `samplesForWindow` widens the read until it reaches a real
// break; see `aggregate.StartsAfterABreak`.
//
// Without any lookback, the oldest day of a `--days N` window is derived from a
// stream that begins exactly at its boundary: work already under way looks like
// work that started there, and the day loses the carried_in that says otherwise.
func lookback(cfg config.Config) time.Duration {
	return 48*time.Hour + time.Duration(cfg.SessionGapMinutes)*time.Minute
}

// maxLookback caps how far `samplesForWindow` will read back when the machine
// has been awake without a break. Two weeks of chained sessions is already far
// past anything seen; beyond it the derivation is the best the cap allows, and
// says so on stderr, so a `--json` consumer's stdout stays a document.
const maxLookback = 14 * 24 * time.Hour

// samplesForWindow reads [since-lookback, until+lookback] and keeps widening the
// read backwards until the stream begins after a break, so the sessions inside
// the window do not depend on where the read happened to start — or stop.
//
// The read runs past `until` because a session is filed, whole, under the day it
// started in: stopping at `until` cut a session still running there, so a day's
// end and active time depended on the `--until` it was asked with (measured: one
// day read 8.9 h asked for alone and 18.6 h asked for with the two days after).
// It also gives the rules that look at what follows a span (ADR 0003, 0004) the
// away after it. `lookback` is long enough ahead for the same reason it is
// behind: a session is cut at 48 h and the absence that ends one is a
// session_gap, so any session begun before `until` ends inside it. With `until`
// at now there is nothing ahead to read and nothing changes. The read is always
// held to the present — also when `until` lies after now, as it does for
// `--until <today>` — so a sample stamped in the future (the clock was set back)
// cannot put a session's end ahead of now.
//
// Widening stops, uncapped, once a wider read finds nothing earlier: the stream
// then begins at the first sample ever recorded (or after a silence longer than
// the step), and no read can find a break before it. Without this a database
// whose first sample is activity — a fresh install — reported "awake without a
// break for 336h" on every call, after seven queries.
func samplesForWindow(st sampleQuerier, since, until time.Time,
	cfg config.Config, loc *time.Location) ([]activity.Sample, bool, error) {
	p := paramsOf(cfg)
	back := lookback(cfg)
	now := clockNow()
	end := until.Add(lookback(cfg))
	if end.After(now) {
		end = now
	}
	var earliest time.Time
	for first := true; ; first = false {
		samples, err := st.Query(since.Add(-back), end)
		if err != nil {
			return nil, false, err
		}
		capped, done := false, aggregate.StartsAfterABreak(samples, p, loc)
		if !done {
			head := earliestSample(samples)
			switch {
			case !first && head.Equal(earliest):
				done = true
			case back >= maxLookback:
				capped, done = true, true
			}
			earliest = head
		}
		if done {
			samples, err = withNeighbours(st, samples, since.Add(-back), end, now)
			return samples, capped, err
		}
		back += 48 * time.Hour
		if back > maxLookback {
			back = maxLookback
		}
	}
}

// withNeighbours adds the sample just before the read and the one just after
// it. A read covers a fixed span either side of the window; a recording gap
// longer than that — the machine off for a week — left a window inside the gap
// with no sample in reach, so nothing in it was counted, while the same day
// asked for with a wider window was counted as away. With its two neighbours
// the gap is an interval between two samples like any other, and the
// attribution turns it into away. Before the first sample ever and after the
// last there is no neighbour, and nothing is counted there.
//
// The work log does not move. The sample in front is added as a marker of
// where the record resumes from, in the away state whatever it recorded: its
// own state would be credited for up to max_gap, a sliver of activity at least
// 48 h before the window that `now` would show as the last session when
// nothing has been done since. As away it opens no session and leaves the first
// active segment — the instant sessions and their day-boundary cuts are
// measured from — where it was. The sample behind has no interval after it, so
// it adds no activity, only the time up to it.
func withNeighbours(st sampleQuerier, samples []activity.Sample, from, to, now time.Time) ([]activity.Sample, error) {
	before, ok, err := st.LastBefore(from)
	if err != nil {
		return nil, err
	}
	if ok {
		marker := activity.Sample{TS: before.TS, State: activity.Away}
		samples = append([]activity.Sample{marker}, samples...)
	}
	after, ok, err := st.FirstAfter(to)
	if err != nil {
		return nil, err
	}
	if ok && !after.TS.After(now) {
		samples = append(samples, after)
	}
	return samples, nil
}

// clockNow is the wall clock, replaceable in tests.
var clockNow = time.Now

// earliestSample is the timestamp of the oldest sample, or zero for none.
func earliestSample(samples []activity.Sample) time.Time {
	var out time.Time
	for _, s := range samples {
		if out.IsZero() || s.TS.Before(out) {
			out = s.TS
		}
	}
	return out
}

// noteIfCapped says so when the read stopped at `maxLookback` without finding a
// break, because the session boundaries then depend on where it stopped. On
// stderr: stdout may be a JSON document.
func noteIfCapped(w io.Writer, capped bool) {
	if !capped {
		return
	}
	fmt.Fprintf(w, "active-lens: this Mac has been awake without a break for longer than %s, "+
		"so the derivation starts from that point and may differ from a longer view\n",
		maxLookback)
}

// sampleQuerier is the part of the store this file needs, so the widening can
// be tested without a database.
type sampleQuerier interface {
	Query(since, until time.Time) ([]activity.Sample, error)
	LastBefore(t time.Time) (activity.Sample, bool, error)
	FirstAfter(t time.Time) (activity.Sample, bool, error)
}

func runNow(args []string) error {
	fs := flag.NewFlagSet("now", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer st.Close()

	loc := time.Local
	now := time.Now().In(loc)
	samples, capped, err := samplesForWindow(st, now, now, cfg, loc)
	if err != nil {
		return err
	}
	noteIfCapped(os.Stderr, capped)
	n := buildNow(samples, now, staleAfter(cfg), paramsOf(cfg), loc)

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(n)
	}
	printNowHuman(os.Stdout, n, lookback(cfg))
	return nil
}

// --- today / report -------------------------------------------------------

func runToday(args []string) error {
	fs := flag.NewFlagSet("today", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	loc := time.Local
	now := time.Now().In(loc)
	since := aggregate.LogicalDayStart(now, cfg.DayStartHour)
	return emitReport(os.Stdout, cfg, since, now, loc, *asJSON)
}

func runReport(args []string) error {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	sinceStr := fs.String("since", "", "start date YYYY-MM-DD (default: 7 days ago)")
	untilStr := fs.String("until", "", "end date YYYY-MM-DD, inclusive (default: today)")
	asJSON := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	loc := time.Local
	since, until, err := resolveRange(*sinceStr, *untilStr, loc, cfg.DayStartHour)
	if err != nil {
		return err
	}
	return emitReport(os.Stdout, cfg, since, until, loc, *asJSON)
}

func runTimeline(args []string) error {
	fs := flag.NewFlagSet("timeline", flag.ContinueOnError)
	sinceStr := fs.String("since", "", "start date YYYY-MM-DD (default: 7 days ago)")
	untilStr := fs.String("until", "", "end date YYYY-MM-DD, inclusive (default: today)")
	days := fs.Int("days", 0, "the last N logical days, ending now (overrides --since/--until)")
	asJSON := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	loc := time.Local

	var since, until time.Time
	if *days > 0 {
		since, until, err = resolveLastDays(*days, loc, cfg.DayStartHour)
	} else {
		since, until, err = resolveRange(*sinceStr, *untilStr, loc, cfg.DayStartHour)
	}
	if err != nil {
		return err
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer st.Close()
	// Read before the window so a session that began earlier is derived whole;
	// buildTimeline emits only the days inside [since, until].
	samples, capped, err := samplesForWindow(st, since, until, cfg, loc)
	if err != nil {
		return err
	}
	noteIfCapped(os.Stderr, capped)
	tl := buildTimeline(samples, since, until, paramsOf(cfg), loc)

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(tl)
	}
	printTimelineHuman(os.Stdout, tl, loc)
	if tl.SampleCount == 0 {
		fmt.Println("\nNo samples in this range. Is the daemon running? Try: active-lens status")
	}
	return nil
}

// emitReport queries the window, aggregates, and writes human or JSON output.
func emitReport(w io.Writer, cfg config.Config, since, until time.Time, loc *time.Location, asJSON bool) error {
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer st.Close()

	// The read `timeline` uses, so the two agree about every instant (ADR 0005).
	samples, capped, err := samplesForWindow(st, since, until, cfg, loc)
	if err != nil {
		return err
	}
	noteIfCapped(os.Stderr, capped)
	rep := buildReport(samples, since, until, paramsOf(cfg), loc)

	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(rep)
	}
	printReportHuman(w, rep)
	if hint := noSamplesHint(rep); hint != "" {
		fmt.Fprintln(w, "\n"+hint)
	}
	return nil
}

// noSamplesHint is the line under a report with no samples in its range. A
// range the record brackets is counted as away without a sample in it — the
// machine was asleep, or the daemon stopped — and "is the daemon running?"
// under a table of away time would be the wrong question.
func noSamplesHint(rep jsonReport) string {
	if rep.SampleCount > 0 {
		return ""
	}
	if rep.Total.OperatingSeconds+rep.Total.PresentSeconds+rep.Total.AwaySeconds == 0 {
		return "No samples in this range. Is the daemon running? Try: active-lens status"
	}
	return "No samples in this range: nothing was recorded during it (the machine was asleep, or the daemon was not running)."
}

// --- export ---------------------------------------------------------------

func runExport(args []string) error {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	format := fs.String("format", "csv", "output format: csv|json")
	sinceStr := fs.String("since", "", "start date YYYY-MM-DD (default: 7 days ago)")
	untilStr := fs.String("until", "", "end date YYYY-MM-DD, inclusive (default: today)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *format != "csv" && *format != "json" {
		return fmt.Errorf("--format must be csv or json, got %q", *format)
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	loc := time.Local
	since, until, err := resolveRange(*sinceStr, *untilStr, loc, cfg.DayStartHour)
	if err != nil {
		return err
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer st.Close()
	samples, err := st.Query(since, until)
	if err != nil {
		return err
	}
	if *format == "json" {
		return exportJSON(os.Stdout, samples)
	}
	return exportCSV(os.Stdout, samples)
}

func exportCSV(w io.Writer, samples []activity.Sample) error {
	cw := csv.NewWriter(w)
	defer cw.Flush()
	if err := cw.Write([]string{"unix", "iso8601", "state"}); err != nil {
		return err
	}
	for _, s := range samples {
		rec := []string{
			strconv.FormatInt(s.TS.Unix(), 10),
			s.TS.Format(time.RFC3339),
			string(s.State),
		}
		if err := cw.Write(rec); err != nil {
			return err
		}
	}
	return cw.Error()
}

func exportJSON(w io.Writer, samples []activity.Sample) error {
	type row struct {
		Unix  int64  `json:"unix"`
		ISO   string `json:"iso8601"`
		State string `json:"state"`
	}
	out := make([]row, 0, len(samples))
	for _, s := range samples {
		out = append(out, row{s.TS.Unix(), s.TS.Format(time.RFC3339), string(s.State)})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// --- status / doctor ------------------------------------------------------

func runStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	info, derr := platform.DaemonStatus()

	if *asJSON {
		return emitStatusJSON(os.Stdout, cfg, info, derr)
	}

	fmt.Printf("Daemon:   ")
	if derr != nil {
		fmt.Printf("unknown (%v)\n", derr)
	} else {
		state := "not installed"
		if info.Loaded {
			state = "loaded (running)"
		} else if info.ConfigPath != "" {
			if _, e := os.Stat(info.ConfigPath); e == nil {
				state = "installed, not loaded"
			}
		}
		fmt.Printf("%s [%s]\n", state, info.Label)
	}
	fmt.Printf("DB path:  %s\n", cfg.DBPath)
	fmt.Printf("Interval: %ds   Threshold: %.0fs   MaxGap: %ds\n",
		cfg.IntervalSeconds, cfg.ActiveThresholdSeconds, cfg.MaxGapSeconds)

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer st.Close()
	last, ok, err := st.Last()
	if err != nil {
		return err
	}
	if !ok {
		fmt.Println("Last:     (no samples yet)")
	} else {
		fmt.Printf("Last:     %s  %s (%s ago)\n",
			last.TS.In(time.Local).Format("2006-01-02 15:04:05"),
			last.State, time.Since(last.TS).Round(time.Second))
	}
	return nil
}

// emitStatusJSON writes the machine-readable status the GUI consumes.
func emitStatusJSON(w io.Writer, cfg config.Config, info platform.DaemonInfo, daemonErr error) error {
	cfgPath, _ := platform.ConfigFilePath()
	s := statusJSON{
		DaemonLoaded:     info.Loaded,
		DaemonLabel:      info.Label,
		ConfigPath:       cfgPath,
		DBPath:           cfg.DBPath,
		IntervalSeconds:  cfg.IntervalSeconds,
		ThresholdSeconds: cfg.ActiveThresholdSeconds,
		MaxGapSeconds:    cfg.MaxGapSeconds,
		DayStartHour:     cfg.DayStartHour,
		DayBoundary:      cfg.DayBoundary,
	}
	// "Could not ask launchd" is not "not loaded": say which, so a consumer does
	// not offer to enable recording that is already running.
	if daemonErr != nil {
		s.DaemonStatusError = daemonErr.Error()
	}
	if info.ConfigPath != "" {
		if _, e := os.Stat(info.ConfigPath); e == nil {
			s.DaemonInstalled = true
		}
	}
	if st, err := store.Open(cfg.DBPath); err == nil {
		defer st.Close()
		if last, ok, err := st.Last(); err == nil && ok {
			s.LastSampleUnix = last.TS.Unix()
			s.LastSampleState = string(last.State)
		}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(s)
}

func runDoctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfgPath, _ := platform.ConfigFilePath()
	dataDir, _ := platform.DataDir()
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	fmt.Printf("Config file: %s\n", cfgPath)
	if _, e := os.Stat(cfgPath); e == nil {
		fmt.Println("             (present)")
	} else {
		fmt.Println("             (absent — using defaults)")
	}
	fmt.Printf("Data dir:    %s\n", dataDir)
	fmt.Printf("DB path:     %s\n", cfg.DBPath)
	fmt.Printf("Interval:    %ds\nThreshold:   %.0fs\nMaxGap:      %ds\n",
		cfg.IntervalSeconds, cfg.ActiveThresholdSeconds, cfg.MaxGapSeconds)
	// These four decide how the work log reads, so surface what resolved.
	fmt.Printf("Break:       %dm\nSessionGap:  %dm\nDayStart:    %02d:00\n",
		cfg.BreakMinutes, cfg.SessionGapMinutes, cfg.DayStartHour)
	fmt.Printf("DayBoundary: %s (%s)\n", cfg.DayBoundary, dayBoundaryGloss(cfg.DayBoundary))
	fmt.Printf("IdleBeforeAway: %s\n", idleBeforeAwayGloss(cfg.IdleBeforeAwayMinutes))
	fmt.Printf("BriefVisit:  %s\n", briefVisitGloss(cfg.BriefVisitMinutes, cfg.BreakMinutes))

	fmt.Print("Signals:     ")
	snap, serr := signal.NewSampler().Snapshot()
	if serr != nil {
		fmt.Printf("FAILED (%v)\n", serr)
	} else {
		fmt.Printf("ok (idle=%.0fs displayAsleep=%v locked=%v)\n",
			snap.IdleSeconds, snap.DisplayAsleep, snap.Locked)
	}
	return nil
}

// --- install / uninstall --------------------------------------------------

func runInstall(args []string) error {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, err = filepath.Abs(exe)
	if err != nil {
		return err
	}
	info, err := platform.InstallDaemon(exe)
	if err != nil {
		return err
	}
	fmt.Printf("Installed LaunchAgent %s\n  config: %s\n  loaded: %v\n",
		info.Label, info.ConfigPath, info.Loaded)
	fmt.Println("The daemon now starts at login and samples in the background.")
	return nil
}

func runUninstall(args []string) error {
	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	info, err := platform.UninstallDaemon()
	if err != nil {
		return err
	}
	fmt.Printf("Removed LaunchAgent %s (%s)\n", info.Label, info.ConfigPath)
	return nil
}
