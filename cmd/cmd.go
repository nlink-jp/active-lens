// Package cmd is the active-lens CLI: a tiny hand-rolled dispatcher over the
// core packages. Each subcommand is a run* function that returns an error;
// Execute maps errors to a non-zero exit.
package cmd

import (
	"fmt"
	"os"
)

const usage = `active-lens — record and visualize how long you actually use your Mac.

Usage:
  active-lens <command> [flags]

Commands:
  daemon                 Run the resident sampler (records to the local DB)
  now     [--json]       The session you are in right now: start, active, breaks
  today   [--json]       Show today's operating / present / away totals
  timeline [flags]       Work log: start / end / breaks per day (--since --until --days --json)
  report  [flags]        Aggregate a date range (--since --until --json)
  export  [flags]        Export raw samples (--format csv|json)
  status                 Show daemon state, DB path, and last sample
  doctor                 Diagnose config, signals, and daemon health
  install                Register the login-time LaunchAgent (auto-start)
  uninstall              Remove the LaunchAgent
  version                Print the version

Privacy: active-lens records only that input happened and the resulting activity
state — never keystrokes, mouse coordinates, or which app was used.
`

// Execute runs the CLI. version is injected at build time.
func Execute(version string) {
	args := os.Args[1:]
	if len(args) == 0 {
		fmt.Print(usage)
		return
	}
	cmd, rest := args[0], args[1:]

	var err error
	switch cmd {
	case "daemon":
		err = runDaemon(rest)
	case "now":
		err = runNow(rest)
	case "today":
		err = runToday(rest)
	case "timeline":
		err = runTimeline(rest)
	case "report":
		err = runReport(rest)
	case "export":
		err = runExport(rest)
	case "status":
		err = runStatus(rest)
	case "doctor":
		err = runDoctor(rest)
	case "install":
		err = runInstall(rest)
	case "uninstall":
		err = runUninstall(rest)
	case "version", "--version", "-v":
		fmt.Printf("active-lens %s\n", version)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "active-lens: %v\n", err)
		os.Exit(1)
	}
}
