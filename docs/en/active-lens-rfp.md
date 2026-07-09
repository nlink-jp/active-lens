# RFP: active-lens

> Generated: 2026-07-09
> Status: Draft

## 1. Problem Statement

I want to look back on how much I actually operated my Mac on a given day —
without ever recording *what* I typed or where I moved the mouse (privacy first).
Only the *fact* that I was operating it, and for how long, needs to be known.
Beyond raw input time (operating), I also want to distinguish time spent looking
at the screen without input (present) and time away or asleep (away).

The target user is the developer themselves (personal use). It runs entirely
locally and sends nothing off the machine.

## 2. Functional Specification

### Commands / API Surface

A Go + cgo CLI engine (`active-lens`) does the measuring, storing, and
aggregating; a Swift/SwiftUI menu-bar GUI is a thin front-end that shells out to
`--json` for visualization — the same two-layer split as
`claude-usage-lens` / `claude-usage-lens-gui`.

| Command | Role |
|---------|------|
| `active-lens daemon` | Resident sampling; appends the 3 states to SQLite |
| `active-lens today [--json]` | Today's operating/present/away summary |
| `active-lens report --since <d> --until <d> [--json]` | Range aggregation (primary interface the GUI calls) |
| `active-lens export --format csv\|json [--since --until]` | Export raw samples / aggregates |
| `active-lens install` / `uninstall` | Register/remove the launchd LaunchAgent (auto-start at login) |
| `active-lens status` | Show daemon state, DB path, last sample time |
| `active-lens doctor` | Diagnose resolved config, permissions, signal health |

### Measurement model (3-state classification)

Each sample instant is classified into 3 states from 2 signals:

| State | Condition |
|-------|-----------|
| **operating** | awake + display on + idle seconds < threshold |
| **present** | awake + display on + idle seconds >= threshold |
| **away** | display off / locked / system asleep |

Signals used (all CoreGraphics C APIs via cgo, no special permission):

- Idle seconds: `CGEventSourceSecondsSinceLastEventType`
- Display power: `CGDisplayIsAsleep(CGMainDisplayID())`
- Lock: `CGSessionCopyCurrentDictionary()` → `CGSSessionScreenIsLocked`
- System sleep: the daemon freezes and samples stop → attributed to *away* at
  aggregation time via the inter-sample gap

### Aggregation model

The daemon records only raw samples `(timestamp, state)`. Range aggregation is a
downstream pure function, so thresholds and the gap cap can be changed and
re-aggregated later.

- For a consecutive pair `a → b`, interval `delta = b.ts - a.ts`
- `effective = min(delta, maxGap)` is attributed to `a.state` (maxGap default =
  interval × 3)
- The excess `delta - effective` is treated as "daemon stopped = system asleep"
  and attributed to *away*

Every second between the first and last sample is thus fully allocated to
operating/present/away.

### Input / Output

- Input: only OS input-idle / display / lock state, polled sensor-style. No user
  text or file input.
- Output: SQLite (raw samples) is the source of truth; aggregates/exports via
  `--json` / CSV. The GUI renders `report --json` with Swift Charts (daily
  stacked bars, hour-of-day heatmap, "today's operating time" in the menu bar).

### Configuration

`config.toml` in the OS config dir (all keys optional, defaults apply):

- `[sampling] interval_seconds` (default 15)
- `[sampling] active_threshold_seconds` (default 30)
- `[sampling] max_gap_seconds` (default = interval × 3)
- `[storage] db_path` (default = data dir; point it at an iCloud Drive / Dropbox
  folder for loose cross-device sync)

### External Dependencies

None. No network access. No external services or credentials.

## 3. Design Decisions

- **CLI = Go + cgo**: util-series standard. CoreGraphics via cgo (image-forge
  precedent). SQLite is pure-Go modernc.org/sqlite; cgo is confined to the
  CoreGraphics bridge only.
- **GUI = Swift/SwiftUI**: native menu-bar residency, Swift Charts, Developer ID
  signing + notarization; rides the same ops as `claude-usage-lens-gui`.
- **Residency**: `claude-usage-lens` uses launchd `StartInterval` to re-launch
  per tick, but this tool samples every 15 s, so a resident daemon
  (`RunAtLoad`+`KeepAlive`, no `StartInterval`) is used to avoid per-tick
  process/cgo init cost.
- **Family placement**: a sibling in the `-lens` family. `claude-usage-lens`
  looks at "Claude usage"; `active-lens` looks at "Mac operating time" — only the
  data source differs; visualization and distribution conventions are shared.
- **Testability**: signal acquisition is isolated behind a `Sampler` interface;
  state classification and range aggregation are pure functions, verifiable with
  a fake and no real hardware.

### Out of scope (explicit)

1. Recording key content / mouse coordinates (permanently out of scope; the
   tool's core privacy principle)
2. Per-app / per-window breakdown (out of scope; consistent with "fact and time
   only")
3. Cloud sync / multi-device merge (not built; limited to placing the DB in a
   sync folder)
4. Non-macOS (darwin/arm64 only)

## 4. Development Plan

### Phase 1: Core (CLI engine)

Independently reviewable.

- cgo acquisition of the 3 signals (idle / display / lock); non-darwin stub
- `daemon` resident sampling loop → append raw samples to SQLite
- Pure functions for state classification and range aggregation + tests
- `today` / `report --json` / `export` / `status` / `doctor`
- `install` / `uninstall` (launchd LaunchAgent)
- Config: sampling interval, threshold, max_gap, DB path (config.toml)
- Complete via `go test ./...` + CLI on-device E2E (daemon records → report
  aggregates)

### Phase 2: Features (GUI)

Independently reviewable.

- Swift/SwiftUI menu-bar residency, bundled signed CLI, thin front over `--json`
- Swift Charts: daily stacked bars (operating/present/away) + hour-of-day heatmap
- "Today's operating time" in the menu bar
- One-click daemon enablement (launchd) from the GUI; interval/threshold/DB-path
  settings UI

### Phase 3: Release (polish)

- README.md / README.ja.md / docs{en,ja} / CHANGELOG / AGENTS.md
- Signing + notarization, util-series submodule registration, org profile entry,
  green `check-org.sh`

Each phase is independently reviewable. Phase 1 is completed as a standalone CLI
(daemon records → report aggregates) and reviewed before Phase 2 GUI begins.

## 5. Required API Scopes / Permissions

**None.** The idle-polling approach needs no Accessibility / Input Monitoring
permission. No external services, credentials, or network access (fully local).

## 6. Series Placement

Series: util-series

Reason: a local measure/aggregate/visualize utility that rides the same
"Go CLI engine + Swift GUI, Developer ID signed + notarized" operations as
`claude-usage-lens`.

## 7. External Platform Constraints

- CoreGraphics idle/display/lock APIs (no permission, but behavior is
  macOS-dependent)
- Auto-start via launchd LaunchAgent
- The present/away boundary depends on the "display sleep timeout" setting: for
  the first few minutes after leaving, before the display auto-sleeps, time is
  temporarily misclassified as "present" (documented limitation)
- Being input-based, "watching a video with no input" is *present*; after full
  inactivity and display-off it is *away*. This is a valid definition of
  "operating time" but is documented explicitly
- Placing SQLite in an iCloud/Dropbox folder assumes a single writer (only one
  device's daemon). Concurrent writes from multiple devices are not supported
- darwin/arm64 only

---

## Discussion Log

- From the requirement "only the fact and duration of operating are needed," an
  idle-time polling approach that does not hook keys/mouse was proposed. Needing
  no Accessibility/Input Monitoring permission matched the requirement well.
- At the user's request, "time present but not operating" and "operating vs
  watching distinction" were added → extended to a 3-state model
  (operating/present/away) combining display-power and lock signals.
- The realization approach was set to follow `claude-usage-lens-gui` (CLI engine
  + thin Swift GUI).
- The residency owner is the CLI (`daemon`), auto-started via launchd. Because of
  the 15 s cadence, a resident daemon is used instead of per-tick `StartInterval`.
- Out of scope confirmed: key content/coordinates, per-app breakdown, cloud sync
  feature, non-macOS. Cloud sync is limited to "the DB can live in a sync folder."
- The user granted approval to proceed autonomously.
