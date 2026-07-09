# AGENTS.md — active-lens

## What it is

macOS CLI that records how long you actually operate your Mac and visualizes it,
recording only that input happened (never *what*). Idle/display/lock signals are
polled from CoreGraphics; each moment is classified operating / present / away.
Sibling GUI (Swift menu bar) is a separate Phase-2 repo. **darwin/arm64 only.**

## Build / test / run

```sh
make build     # -> dist/active-lens  (NEVER `go build` directly; CGO_ENABLED=1)
make test      # go test ./...
make vet       # go vet (darwin/cgo) + CGO_ENABLED=0 GOOS=linux vet (stub check)
make package   # zip + notarize the darwin/arm64 release asset
```

Version is injected from `git describe` via `-ldflags -X main.version`.

## Layout

```
main.go                 version wiring -> cmd.Execute
cmd/                    dispatcher + subcommands
  cmd.go                Execute(): command routing + usage
  commands.go           daemon/today/timeline/report/export/status/doctor/install/uninstall
  report.go             PURE: buildReport, buildTimeline, JSON + human formatting
core/
  signal/               cgo CoreGraphics bridge behind the Sampler interface
    signal_darwin.go     the cgo impl (idle/display/lock)
    signal_other.go      non-darwin stub (ErrUnsupported)
  activity/             State enum + pure Classify(snapshot, threshold)
  sampler/              resident daemon loop (injectable sampler/recorder/clock)
  store/                SQLite (modernc.org/sqlite, pure-Go) raw sample store
  aggregate/            PURE interval attribution -> Totals / ByDay / ByHourOfDay;
                        timeline.go = contiguous state Segments + per-day work
                        session (start/end/breaks)
  config/               minimal hand-rolled TOML (no external dep)
  platform/             config/data paths + launchd LaunchAgent scheduler
```

## Design invariants / gotchas

- **Privacy is the point.** Only elapsed-idle (a number) and two presence
  booleans are read. Never add anything that captures key codes, coordinates,
  window titles, or app identity, and never add network access.
- **Raw samples are the source of truth.** Thresholds and the gap cap are applied
  at aggregation time, so history can be re-aggregated. Do not pre-bucket on write.
- **cgo is confined to `core/signal`.** SQLite is pure-Go on purpose; keep it that
  way so the rest of the tree builds/vets without a C toolchain. The `_other.go`
  stubs exist so `GOOS=linux` vet stays green.
- **Resident daemon, not `StartInterval`.** The cadence is sub-minute; launchd
  keeps one long-lived process alive (`RunAtLoad`+`KeepAlive`). System sleep
  freezes it, producing a sample gap that aggregation attributes to *away*.
- **`away` wins classification** — a locked or display-off machine is away even
  with a fresh idle counter. Verified live: display sleep → `displayAsleep=true`
  → away.
- Store files are owner-only (dir 0700, db 0600); the DB holds personal timestamps.

## Status

Phase 1 (CLI engine) complete: `make build`/`make test`/`make vet` green,
end-to-end verified (daemon records → report/export). Phase 2 is the Swift menu-bar
GUI. See `docs/ja/active-lens-rfp.ja.md` for the full plan.
