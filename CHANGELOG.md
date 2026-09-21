# Changelog

All notable changes to active-lens are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/), and the project adheres to
[Semantic Versioning](https://semver.org/).

## [Unreleased]

### Fixed

- **On a Mac kept awake for more than two days, sessions could be reported a
  whole day later than they were.** The derivation read a fixed 48 hours before
  the window, which holds one session but not a chain of them: each is cut at a
  day boundary and the next begins there, so a read that started mid-chain cut
  its first session a day late and shifted every session after it. The read now
  widens until it reaches a real break (a gap of at least `session_gap`), up to
  14 days, and says on stderr when that cap is reached.

## [0.3.0] - 2026-09-17

### Added

- **`work.day_boundary`** chooses what a logical day boundary does to a session
  running across it. `"session"` (default, unchanged) files the session whole
  under the day it started in; `"strict"` ends it at the boundary, so each day is
  credited exactly the work that fell inside it — the rule for a workplace whose
  working day is defined for you. See
  [ADR 0002](docs/en/adr/0002-configurable-day-boundary.md).
- **`carried_in` / `carried_out`** on every session and day in
  `timeline --json`, and `carried_in` on `now --json`'s session: whether that end
  is a boundary cut rather than a real start or finish. Set in `"session"` mode
  too, for the backstop cut at the second boundary. The human `timeline` prints
  `continues from previous day` / `continues into next day`, and states the
  `day_boundary` rule in its header when it is not the default.
- `timeline --json` publishes `day_boundary`; `status --json` publishes
  `day_start_hour` and `day_boundary`; `doctor` prints the mode and what it means.

### Fixed

- **A day boundary landing exactly between two segments was never applied.** The
  cut was only looked for *inside* a segment, so a state change — or a `max_gap`
  split — falling exactly on the hour slipped through, and the missed boundary
  stayed the session's limit, which is never reached again. Under the default
  rule this let ADR 0001's backstop miss its cut entirely: a Mac held awake could
  produce one session of unbounded length instead of one bounded at two logical
  days. Whether it happened at all depended on the phase of the daemon's sampling
  tick against the hour, so it failed silently and intermittently.
- **`timeline` now reads the sample stream from before the window it displays**
  (`since − (48h + session_gap)`), so the oldest day of a `--days N` range is
  derived from the whole session it belongs to. `sample_count` still counts only
  the samples inside the range.

### Changed

- Nothing else, unless you set `work.day_boundary = "strict"`. The default keeps
  ADR 0001's attribution, so existing history reads exactly as before. Both modes
  derive from the same raw samples: switching re-reads all recorded history,
  with no migration.

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

[Unreleased]: https://github.com/nlink-jp/active-lens/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/nlink-jp/active-lens/compare/v0.2.1...v0.3.0
[0.2.0]: https://github.com/nlink-jp/active-lens/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/nlink-jp/active-lens/releases/tag/v0.1.0
