# Changelog

All notable changes to active-lens are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/), and the project adheres to
[Semantic Versioning](https://semver.org/).

## [0.2.1] - 2026-07-12

### Changed

- **`LICENSE` is now bundled** in the release archive alongside `README.md`,
  per `nlink-jp/.github` CONVENTIONS.md §Release Archive Standard.
- **darwin code-signature identifier** is now the canonical `active-lens`
  (previously the build-time `active-lens-darwin-arm64`).

Packaging-only release; no change to the binary's behaviour.

## [0.2.0] - 2026-07-10

### Changed

- **Work days are now derived from sessions, not from calendar midnight.** A
  *session* is an unbroken stretch of work, ended by an away span of at least
  `work.session_gap_minutes` (default 4h). A session is never split at midnight;
  it is filed whole under the **logical day** it started in, which begins at
  `work.day_start_hour` (default 04:00). See
  [ADR 0001](docs/en/adr/0001-session-based-day-attribution.md).
- `report`'s day buckets and `today` follow the logical day. `hour_of_day` keeps
  wall-clock hours.
- **BREAKING (`timeline --json`)**: each day gains `day_start_unix`, `sessions`
  and `blocks`; the payload root gains `session_gap_seconds` and
  `day_start_hour`. `segments` now covers only the spans inside a day's sessions,
  so overnight sleep is no longer emitted. `work_end` may fall on the next
  calendar day.

### Added

- `active-lens now [--json]` — the session you are in right now: start, end,
  active split, breaks, `open` / `paused`, and the logical day's total. A
  session's start never changes; only `open` flips, once an absence passes the
  session gap.
- `active-lens timeline --days N` — resolves the last N logical days, so a
  consumer never has to reimplement the day boundary.
- Config `work.session_gap_minutes` (default 240) and `work.day_start_hour`
  (default 4; `0` for calendar days). A session gap at or below `break_minutes`
  is rejected, since every break would otherwise end its own session.

### Fixed

- Work that ran past midnight reported the next day's `work_start` as `00:00`,
  and the previous day's `work_end` as `00:00`.
- A night's sleep between two days' work was counted as a **break**, corrupting
  `span_seconds`, the break count, and the break totals — 6h 27m of sleep in
  real recorded data.

## [0.1.0] - 2026-07-09

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

[Unreleased]: https://github.com/nlink-jp/active-lens/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/nlink-jp/active-lens/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/nlink-jp/active-lens/releases/tag/v0.1.0
