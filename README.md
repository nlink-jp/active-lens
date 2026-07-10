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
active-lens now      [--json]      The session you are in right now
active-lens today    [--json]      Today's operating / present / away totals
active-lens timeline [flags]       Work log: start / end / breaks per day
active-lens report   [flags]       Aggregate a date range
active-lens export   [flags]       Export raw samples (--format csv|json)
active-lens status   [--json]      Daemon state, DB path, last sample
active-lens doctor                 Diagnose config, signals, daemon health
active-lens install | uninstall    Register / remove the login LaunchAgent
active-lens version
```

### Sessions and logical days

A **session** is one unbroken stretch of work. It ends when you are away for at
least `work.session_gap_minutes` (default 4 hours) — a night's sleep, an
afternoon out. Shorter absences stay inside the session: those of at least
`work.break_minutes` (default 10) are **breaks**, and anything shorter folds into
continuous work. Both *operating* and *present* count as "at the machine".

A session is never split at midnight. It is filed, whole, under the **logical
day** it started in — the day that begins at `work.day_start_hour` (default
04:00) rather than at 00:00. So an evening that runs to 00:59 belongs to the
evening, and the following morning starts when you actually sit down, not at
00:00. Sleeping between two sessions is not a break, and neither is a five-hour
gap between a morning and an evening session.

### now (the current session)

```sh
active-lens now
active-lens now --json      # for the GUI's menu bar
```

```
Session  20:44 → 00:59   (open)
  active     3h 53m   (operating 3h 01m, present 52m)
  breaks     2 · 22m
               21:51–22:01 (10m)
               22:30–22:41 (11m)
  today      3h 53m   (2026-07-09)

Currently operating · recording
```

The session is `open` while your last activity is less than a session gap old,
and `paused` when it is open but you are away right now. Thirty minutes into an
absence nothing can yet say whether it is a break or the end of the day, so the
session stays open; once the absence passes the gap it closes. A session's
**start never changes** — only `open` flips.

### timeline (work log)

Reconstructs, per logical day, **when** you were at the machine — the derived
start, breaks, and end of each work session — from the raw samples.

```sh
active-lens timeline                                   # last 7 days
active-lens timeline --days 30                         # last 30 logical days
active-lens timeline --since 2026-07-01 --until 2026-07-08
active-lens timeline --json                            # for the GUI
```

```
2026-07-09   20:44 → 00:59 (+1d)   active 3h 53m   · 2 break(s) 22m
    break 21:51–22:01 (10m)
    break 22:30–22:41 (11m)

2026-07-10   07:26 → 10:42   active 2h 40m
```

`(+1d)` marks a session that ended after midnight. The `--json` output includes
each day's colored spans (for a timeline view), its `sessions` and `blocks`, and
the derived `work_start` / `work_end` / `breaks`. Prefer `--days N` over
computing a `--since` date yourself: it resolves the range against the logical
day, boundary included.

### report / today

```sh
active-lens today
active-lens report --since 2026-07-01 --until 2026-07-08
active-lens report --json           # machine-readable, for the GUI
```

`--until` is inclusive. With no flags, `report` covers the last 7 days. Day
buckets are logical days, so `today` at 01:00 still reports the evening you are
in the middle of. The hour-of-day heatmap keeps real wall-clock hours.

Note that `report` attributes each *second* to the logical day it falls in, while
`timeline` attributes a whole *session* to the day it started in. The two agree
except when a session runs through `day_start_hour` — an all-nighter counts
entirely on its starting day in `timeline`, and splits across two in `report`.
That is the difference between a work log and a totals ledger.

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
| `work.break_minutes` | `10` | Min away span counted as a break inside a session |
| `work.session_gap_minutes` | `240` | Away span that ends a session; must exceed `break_minutes` |
| `work.day_start_hour` | `4` | Local hour a logical day begins; `0` for calendar days |
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
