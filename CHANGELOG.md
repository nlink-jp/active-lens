# Changelog

All notable changes to active-lens are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/), and the project adheres to
[Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added — Phase 1 (CLI engine)

- Content-free activity sampling on macOS via CoreGraphics (cgo): seconds since
  last input, display power, screen lock — no keystrokes, coordinates, or app
  identity are ever read.
- Three-state classification: **operating** / **present** / **away**.
- Resident sampling daemon (`active-lens daemon`) writing raw `(ts, state)`
  samples to a pure-Go SQLite database.
- Pure interval-attribution aggregation, split across day and hour-of-day
  buckets; gaps beyond `max_gap` are credited to *away* (system asleep).
- **Work-log timeline** (`timeline`): reconstructs per-day contiguous state
  spans and derives the work session — start, end, and breaks (away spans of at
  least `work.break_minutes`, default 10; operating and present both count as
  "at the machine"). Dense day series (every calendar day in range, empty days
  included) with a `--json` payload for the GUI.
- Commands: `today`, `timeline`, `report` (`--since`/`--until`/`--json`),
  `export` (`--format csv|json`), `status` (with `--json` for the GUI),
  `doctor`, `install`, `uninstall`, `version`.
- Config `work.break_minutes` (default 10) for the timeline break threshold.
- launchd LaunchAgent integration for login-time auto-start (resident daemon:
  `RunAtLoad`+`KeepAlive`, no `StartInterval`).
- `config.toml` for sampling interval, active threshold, gap cap, and DB path
  (minimal hand-rolled parser, no external dependency).

[Unreleased]: https://github.com/nlink-jp/active-lens/commits/main
