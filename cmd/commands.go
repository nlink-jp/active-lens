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
	}
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

// nowWindow is how far back `now` reads samples. A session is bounded below 48h
// by the backstop, and the absence that opened it is at most one session_gap
// longer, so 72h provably contains the whole now-session.
const nowWindow = 72 * time.Hour

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
	samples, err := st.Query(now.Add(-nowWindow), now)
	if err != nil {
		return err
	}
	n := buildNow(samples, now, staleAfter(cfg), paramsOf(cfg), loc)

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(n)
	}
	printNowHuman(os.Stdout, n)
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
	samples, err := st.Query(since, until)
	if err != nil {
		return err
	}
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

	samples, err := st.Query(since, until)
	if err != nil {
		return err
	}
	maxGap := time.Duration(cfg.MaxGapSeconds) * time.Second
	rep := buildReport(samples, since, until, maxGap, loc, cfg.DayStartHour)

	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(rep)
	}
	printReportHuman(w, rep)
	if rep.SampleCount == 0 {
		fmt.Fprintln(w, "\nNo samples in this range. Is the daemon running? Try: active-lens status")
	}
	return nil
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
func emitStatusJSON(w io.Writer, cfg config.Config, info platform.DaemonInfo, _ error) error {
	cfgPath, _ := platform.ConfigFilePath()
	s := statusJSON{
		DaemonLoaded:     info.Loaded,
		DaemonLabel:      info.Label,
		ConfigPath:       cfgPath,
		DBPath:           cfg.DBPath,
		IntervalSeconds:  cfg.IntervalSeconds,
		ThresholdSeconds: cfg.ActiveThresholdSeconds,
		MaxGapSeconds:    cfg.MaxGapSeconds,
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
	// These three decide how the work log reads, so surface what resolved.
	fmt.Printf("Break:       %dm\nSessionGap:  %dm\nDayStart:    %02d:00\n",
		cfg.BreakMinutes, cfg.SessionGapMinutes, cfg.DayStartHour)

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
