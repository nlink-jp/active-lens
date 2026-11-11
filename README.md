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

**Leaving the desk.** When input stops and the display then goes off (or the
screen locks) and stays off for a minute, the no-input stretch before it — up to
`work.idle_before_away_minutes` (default 10) — is counted as part of the absence:
you had already left when the display timer fired. A longer no-input stretch (a
video or a call holding the display awake) stays *present*, and so does one after
which the display came back within a minute. Match the setting to your
display-sleep timeout; `0` turns it off. See
[ADR 0003](docs/en/adr/0003-idle-before-away.md).

**A touch in the middle of an absence.** Activity of at most
`work.brief_visit_minutes` (default 2) that has an away of at least
`break_minutes` on both sides is not counted as work: the break went on. Without
this, thirty seconds at the keyboard three hours after you left turns those three
hours into a "break" and moves the end of the session to that touch. `0` counts
every touch as work. See [ADR 0004](docs/en/adr/0004-brief-visit.md).

A session is never split at midnight. It is filed, whole, under the **logical
day** it started in — the day that begins at `work.day_start_hour` (default
04:00) rather than at 00:00. So an evening that runs to 00:59 belongs to the
evening, and the following morning starts when you actually sit down, not at
00:00. Sleeping between two sessions is not a break, and neither is a five-hour
gap between a morning and an evening session.

### When the day boundary is not yours (`work.day_boundary`)

The rule above is right for a personal log: work that runs from 22:00 to 09:00
is one night's work, filed under the night it began. It is wrong where the
working day is defined by someone else — an employer whose day starts at 05:00 —
because the hours after 05:00 are then merged into a day that had already ended.

`work.day_boundary` chooses between the two readings:

| Value | A session that runs through the boundary |
|-------|------------------------------------------|
| `session` (default) | Stays whole, filed under the day it started in |
| `strict` | Is cut at the boundary; each day is credited exactly the work that fell inside it |

Under `strict` the work log and the totals ledger agree day for day, the menu
bar's figure restarts at the boundary, and a day whose work was already under
way when the boundary passed is marked `continues from previous day` (`carried_in`
in the JSON). That mark matters: such a day's `work_start` is the boundary
itself, not a time anyone sat down at.

Both modes read the same recorded samples — thresholds and boundaries are
applied when the log is derived, so switching the setting re-reads all of your
history and nothing is migrated or lost. See
[ADR 0002](docs/en/adr/0002-configurable-day-boundary.md).

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

`(+1d)` marks a session that ended after midnight. Under `strict` the header says
so — the default rule is not announced, since it is what every log has always
been derived under. Also under `strict`, a day whose work crossed
the boundary is marked `continues into next day` / `continues from previous day`
so a cut is never mistaken for a punctual start or finish. The `--json` output
includes each day's colored spans (for a timeline view), its `sessions` and
`blocks`, the derived `work_start` / `work_end` / `breaks`, and the
`carried_in` / `carried_out` flags. Prefer `--days N` over computing a `--since`
date yourself: it resolves the range against the logical day, boundary included.

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
That is the difference between a work log and a totals ledger. Setting
`work.day_boundary = "strict"` removes the difference by cutting the session at
the boundary too.

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
| `work.idle_before_away_minutes` | `10` | No input up to this long before the display goes off counts as away; match your display-sleep timeout, `0` to turn off |
| `work.brief_visit_minutes` | `2` | Activity up to this long between two breaks is not work; `0` to turn off |
| `work.day_start_hour` | `4` | Local hour a logical day begins; `0` for calendar days |
| `work.day_boundary` | `"session"` | `"session"`: a session stays on the day it started. `"strict"`: the boundary cuts it, so each day gets only its own hours |
| `storage.db_path` | data dir | Where samples live; point at iCloud/Dropbox for loose sync |

Run `active-lens doctor` to see what resolved. A key the tool does not know is an error, so a
misspelt setting cannot silently leave the default in force.

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
- Just after you leave, before the display auto-sleeps, the samples are
  recorded as **present**. Aggregation counts that stretch as away once the
  display has been off for a minute (`work.idle_before_away_minutes`), so an
  open session's end and active time move back by up to that long after you
  leave. If your display sleeps later than the setting, the stretch stays
  present.
- Work done in two minutes or less between two breaks — a quick reply — is not
  counted (`work.brief_visit_minutes`); nothing in the signals separates it from
  a touch in passing.
- `report` / `today` read only the window they are given, so at the very start
  and end of that window (its first and last ten minutes) they can differ from
  `timeline` by one such touch (at most two minutes) or one no-input stretch (at
  most ten). Day boundaries inside a multi-day report are not affected.
- No input for under ten minutes cannot be told apart from having left: locking
  the screen and walking off right after reading counts that reading as absence.
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
