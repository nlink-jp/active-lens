# CLAUDE.md — active-lens

Organization rules: https://github.com/nlink-jp/.github/blob/main/CONVENTIONS.md
Workspace rules also apply (see the parent `nlink-jp/CLAUDE.md`).

## What this is

A macOS tool that records **how long you actually operate your Mac** and
visualizes it. It records only that input happened and the resulting activity
state — never keystrokes, mouse coordinates, or which app was used.

Two-layer, like `claude-usage-lens` / `claude-usage-lens-gui`:

- **CLI (`active-lens`, this repo)** — Go + cgo engine: samples, stores, aggregates.
- **GUI (menu bar, Swift, separate repo)** — thin front over `--json` (Phase 2).

## Build & test

- **`make build`** → `dist/active-lens` (never `go build` directly).
- `make test` / `go test ./...` — must pass before committing.
- `make vet` — darwin (cgo) + linux stub compile check.
- **cgo is required** and the tool is **darwin/arm64 only**. `CGO_ENABLED=1` is
  set by the Makefile. Only the CoreGraphics signal bridge uses cgo; SQLite is
  pure-Go (modernc.org/sqlite).

## Architecture

- `core/signal` — cgo CoreGraphics bridge (idle seconds, display power, lock).
  Isolated behind the `Sampler` interface; `signal_other.go` is a non-darwin stub.
- `core/activity` — the 3 states (operating/present/away) and the pure
  `Classify` function.
- `core/sampler` — the resident daemon loop (injectable sampler/recorder/clock).
- `core/store` — SQLite persistence of raw `(ts, state)` samples.
- `core/aggregate` — pure attribution of sample intervals to states, split
  across day / hour-of-day buckets. Gap beyond `max_gap` → away (system asleep).
- `core/config` — minimal hand-rolled TOML (no external dep).
- `core/platform` — config/data paths + launchd LaunchAgent (resident daemon:
  `RunAtLoad`+`KeepAlive`, no `StartInterval`).
- `cmd` — dispatcher + subcommands.

## Key design points

- **Privacy**: only elapsed-idle time and two presence booleans are ever read;
  nothing about *what* was done is stored. No network access, no permissions.
- **Raw samples are the source of truth**; thresholds/gap are applied at
  aggregation time, so history can be re-aggregated under new settings.
- **Resident daemon** (not `StartInterval`) because the cadence is sub-minute.
- Docs: `docs/{en,ja}` mirror; README.md and README.ja.md kept in sync.
