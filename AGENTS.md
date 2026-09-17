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
make verify-release  # gate: .notarized marker + freshness (run before upload)
```

Version is injected from `git describe` via `-ldflags -X main.version`.

## Layout

```
main.go                 version wiring -> cmd.Execute
cmd/                    dispatcher + subcommands
  cmd.go                Execute(): command routing + usage
  commands.go           daemon/now/today/timeline/report/export/status/doctor/install/uninstall
  report.go             PURE: buildReport, buildTimeline, buildNow, JSON + human formatting
core/
  signal/               cgo CoreGraphics bridge behind the Sampler interface
    signal_darwin.go     the cgo impl (idle/display/lock)
    signal_other.go      non-darwin stub (ErrUnsupported)
  activity/             State enum + pure Classify(snapshot, threshold)
  sampler/              resident daemon loop (injectable sampler/recorder/clock)
  store/                SQLite (modernc.org/sqlite, pure-Go) raw sample store
  aggregate/            PURE interval attribution -> Totals / ByDay / ByHourOfDay;
                        timeline.go = Segments -> Sessions -> per-logical-day
                        DayTimeline (work markers, breaks, blocks);
                        Params.DayBoundary picks where a session is cut
  config/               minimal hand-rolled TOML (no external dep)
  platform/             config/data paths + launchd LaunchAgent scheduler
```

## Design invariants / gotchas

- **Privacy is the point.** Only elapsed-idle (a number) and two presence
  booleans are read. Never add anything that captures key codes, coordinates,
  window titles, or app identity, and never add network access.
- **Raw samples are the source of truth.** Thresholds and the gap cap are applied
  at aggregation time, so history can be re-aggregated. Do not pre-bucket on write.
- **Sessionize before bucketing, never after.** `Sessions()` runs on the raw
  segment stream; only then is each session filed under a logical day. Deriving
  work markers *after* a day split is the bug ADR 0001 removes: it pins
  `work_start`/`work_end` to 00:00 and turns a night's sleep into a "break". See
  `docs/en/adr/0001-session-based-day-attribution.md`.
- **Two attribution rules, one code path.** `work.day_boundary` decides where
  `Sessions()` cuts: `session` (default) only at the second boundary after the
  start — the backstop below — and `strict` at every boundary, for a workplace
  whose day is defined for the user. Both go through `Params.cutAfter`; do not
  grow a second derivation. A cut end is marked `CarriedIn`/`CarriedOut` so a
  boundary is never mistaken for a real start or finish, in either mode. See
  `docs/en/adr/0002-configurable-day-boundary.md`.
- **`present` never times out.** `Classify` returns `present` for as long as the
  display is on and the machine unlocked, so a Mac held awake emits an activity
  run that never ends on its own. That is why `Sessions()` has a backstop: under
  `day_boundary = session` a session may cross at most one logical day boundary.
- **A now-session's `start` is never provisional.** At the live edge only `open`
  can change, once an absence passes `session_gap`. Nothing may move a boundary
  retroactively — the menu bar displays that start time. Under
  `day_boundary = strict` the day boundary opens a *new* session at a known
  instant; that is a new start, not a moved one.
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
GUI (`nlink-jp/active-lens-gui`), which bundles a signed copy of this CLI — a
change to the `--json` payloads is only visible in the GUI once that copy is
rebuilt. See `docs/ja/active-lens-rfp.ja.md` for the full plan.
