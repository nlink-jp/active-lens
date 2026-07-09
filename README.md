# active-lens

Record how long you actually operate your Mac — and visualize it — **without
recording anything about *what* you do**.

active-lens polls the OS for content-free activity signals (seconds since last
input, display power, screen lock) and buckets each day into three states:

- **operating** — awake, display on, input within the threshold
- **present** — awake, display on, but no recent input (watching / reading)
- **away** — display off, locked, or the system asleep

It never sees keystrokes, mouse coordinates, or which app is in front. It needs
no Accessibility or Input Monitoring permission, and it makes no network access.

This is the CLI engine. A menu-bar GUI (Swift) that visualizes the data is a
sibling project, mirroring `claude-usage-lens` / `claude-usage-lens-gui`.

> **Platform:** macOS on Apple Silicon (darwin/arm64) only.

## How it works

A resident daemon samples the activity state every _interval_ seconds and appends
a raw `(timestamp, state)` row to a local SQLite database. Reports are computed
from those raw samples, so you can change the threshold or gap cap later and
re-aggregate history.

Time between two samples is credited to the earlier sample's state, capped at
`max_gap`. A gap larger than that means the daemon was not running (the system
was asleep), so the excess is credited to **away**. Every second between the
first and last sample is thus accounted for.

## Install

```sh
make build            # -> dist/active-lens  (never `go build` directly)
cp dist/active-lens ~/bin/active-lens   # or anywhere on PATH
```

Enable background sampling at login:

```sh
active-lens install   # registers a launchd LaunchAgent that runs `daemon`
```

`install` points the LaunchAgent at the current binary path, so install from the
location you intend to keep it.

## Usage

```
active-lens daemon                 Run the resident sampler (usually via launchd)
active-lens today    [--json]      Today's operating / present / away totals
active-lens timeline [flags]       Work log: start / end / breaks per day
active-lens report   [flags]       Aggregate a date range
active-lens export   [flags]       Export raw samples (--format csv|json)
active-lens status   [--json]      Daemon state, DB path, last sample
active-lens doctor                 Diagnose config, signals, daemon health
active-lens install | uninstall    Register / remove the login LaunchAgent
active-lens version
```

### timeline (work log)

Reconstructs, per day, **when** you were at the machine — the derived start,
breaks, and end of your work session — from the raw samples.

```sh
active-lens timeline                                   # last 7 days
active-lens timeline --since 2026-07-01 --until 2026-07-08
active-lens timeline --json                            # for the GUI
```

```
2026-07-09   09:32 → 18:47   active 6h 40m   · 2 break(s) 1h 05m
    break 12:05–12:58 (53m)
    break 15:30–15:42 (12m)
```

A **break** is an away span of at least `work.break_minutes` (default 10) between
the day's first and last active moment; shorter away gaps fold into continuous
work. Both *operating* and *present* count as "at the machine". The `--json`
output includes each day's colored spans (for a timeline view) plus the derived
`work_start` / `work_end` / `breaks`.

### report / today

```sh
active-lens today
active-lens report --since 2026-07-01 --until 2026-07-08
active-lens report --json           # machine-readable, for the GUI
```

`--until` is inclusive. With no flags, `report` covers the last 7 days.

Example:

```
Timezone: Asia/Tokyo    Range: 2026-07-02 → 2026-07-09    (41210 samples)

Total    operating 38h 12m   present 9h 05m   away 120h 43m

By day
  2026-07-09   operating 6h 10m   present 1h 20m   away 16h 30m
  ...
```

### export

```sh
active-lens export --format csv  --since 2026-07-01 > activity.csv
active-lens export --format json --since 2026-07-01 > activity.json
```

## Configuration

Optional `config.toml` in `~/Library/Application Support/active-lens/`
(see [`config.example.toml`](config.example.toml)). All keys are optional:

| Key | Default | Meaning |
|-----|---------|---------|
| `sampling.interval_seconds` | `15` | How often the daemon samples |
| `sampling.active_threshold_seconds` | `30` | Idle cutoff for operating vs present |
| `sampling.max_gap_seconds` | `interval × 3` | Gap cap; excess counts as away (sleep) |
| `work.break_minutes` | `10` | Min away span counted as a break in the timeline |
| `storage.db_path` | data dir | Where samples live; point at iCloud/Dropbox for loose sync |

Run `active-lens doctor` to see what resolved.

## Privacy

active-lens reads only:

- seconds since the last input event (a single number),
- whether the display is asleep (a boolean),
- whether the screen is locked (a boolean).

No key codes, no coordinates, no window/app titles, no content of any kind is
read or stored. The database holds only timestamps and one of three state
labels. Nothing leaves your machine.

## Limitations

- Being input-based, watching a video with no input counts as **present**; after
  full inactivity and the display sleeping it becomes **away**.
- Just after you leave, before the display auto-sleeps, time is briefly
  classified as **present**. This depends on your Energy Saver display-sleep
  setting.
- Placing the DB in an iCloud/Dropbox folder assumes a single writer (one
  machine's daemon). Multi-device concurrent writes are not supported.

## Development

```sh
make test    # go test ./...
make vet     # darwin (cgo) + linux stub compile check
```

cgo is required (the CoreGraphics signal bridge); SQLite is pure-Go
(modernc.org/sqlite), so cgo is confined to that one package.

## License

MIT — see [LICENSE](LICENSE).
