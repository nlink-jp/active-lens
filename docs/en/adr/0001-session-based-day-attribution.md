# ADR 0001 — Session-based work-day attribution

**Status:** Proposed (awaiting confirmation)
**Date:** 2026-07-10
**Targets:** the `timeline` / `report` / `today` analysis path (`core/aggregate`, `core/config`, `cmd`)
**Companion:** `active-lens-gui` ADR 0001 (rendering + hover) — the two must ship together.

---

## 1. Context

`aggregate.Timeline` currently runs in this order:

1. `Segments()` collapses the sample stream into contiguous state spans.
2. `splitByDay()` cuts every span at **local calendar midnight**.
3. `deriveDay()` looks for the first and last *active* span **within each day bucket**.

Because derivation happens *after* bucketing, the work-session markers can never
escape the `00:00 – 24:00` window of a calendar day. Real recorded data from
2026-07-09/10 (work ran from 20:44 through 00:59, then sleep, then a 07:26 start):

```
2026-07-09   20:44 → 00:00   active 2h 54m   · 2 break(s) 22m
2026-07-10   00:00 → 09:29   active 3h 03m   · 1 break(s) 6h 27m
    break 00:59–07:26 (6h 27m)
```

Three distinct defects, one root cause:

1. **`work_start` is 00:00** on 07-10 — an artefact of the cut, not a real start.
2. **`work_end` is 00:00** on 07-09 — the real end was 00:59 the next morning.
3. **Overnight sleep (6h 27m) is counted as a break.** The away span sits between
   the day bucket's first and last active moment, so it satisfies the break rule.
   This is the worst of the three: it corrupts `span_seconds`, the break count,
   and the break totals.

A fourth consideration constrains the fix. `activity.Classify` returns `present`
whenever the display is on and the machine is unlocked, *regardless of how long
the user has been idle*. A Mac held awake (`caffeinate`, display-sleep disabled)
therefore emits `present` indefinitely. Any purely data-driven session rule must
tolerate a run of activity that never terminates on its own.

## 2. Decision

Derive sessions **before** any day bucketing, and bucket sessions rather than
segments.

### 2.1 Session

A **session** is a maximal run of segments that contains at least one active
(`operating` or `present`) segment, delimited by away spans of at least
`session_gap`.

- `Session.Start` = the first active segment's start.
- `Session.End` = the last active segment's end.
- Leading and trailing away spans are trimmed — a session begins and ends on
  activity, never on absence.
- The delimiting away span belongs to no session.

### 2.2 Logical day

Logical day `D` is the half-open interval `[D 04:00, D+1 04:00)`, parameterised
by `day_start_hour` (default `4`). `logical_date(t) = date(t − day_start_hour)`.
Setting `day_start_hour = 0` makes the logical day identical to the calendar day.
It does *not* restore the pre-change output: sessionization is unconditional, so
a midnight-crossing session stays whole and is still filed under the day it
started in. Only the boundary used for filing moves.

### 2.3 Attribution

A session is filed under `logical_date(Session.Start)`, **whole and unsplit**.
The evening-into-the-small-hours case crosses *zero* logical boundaries, so it
survives intact on the day it started.

### 2.4 Backstop

A session may cross **at most one** logical day boundary. On reaching a second
boundary it is force-closed there, and a new session opens at that instant.

This exists solely for the never-sleeping-display case of §1. It bounds any
session below 48 hours, so a runaway `present` run is chopped into day-sized
pieces instead of producing an unbounded column. It never fires on an
all-nighter (20:00 → 09:00 crosses exactly one boundary and stays intact).

### 2.5 Day markers

For each logical day, over the sessions filed under it:

- `work_start` = first session's start; `work_end` = last session's end.
- `breaks` = the union of the sessions' internal breaks — away spans inside a
  session of at least `break_minutes`. Because a session-delimiting away is at
  least `session_gap`, overnight sleep can no longer appear here.
- **Gaps between sessions on the same day are not breaks.** A five-hour absence
  in the middle of a day ends one session and starts another; calling it a
  "break" would be as wrong as calling sleep one.
- `operating_seconds` / `present_seconds` sum the day's sessions' segments.

### 2.6 `report` and `today`

`report --by-day` and `today` bucket on the logical day as well, so a query at
01:00 attributes the running work to the day it belongs to rather than to a
freshly-started calendar day. `ByHourOfDay` stays on wall-clock hours — a
time-of-day heatmap must keep real clock hours on its axis.

Segments are already split at hour boundaries by `emitSplit`, and
`day_start_hour` is an integer hour, so no segment straddles a logical boundary.
Bucketing only needs its key changed from `date()` to `logical_date()`.

### 2.7 The live edge: the now-session

Once sessions are first-class, "today" stops being the right unit for the
menu bar. The question a glance at the menu bar asks is *"this stretch of work —
when did it start, how long have I been at it?"*, and that is a session, not a
day. Until now the two coincided, because each calendar day derived exactly one
work-session envelope; `PopoverView.workSession(_ d: TimelineDay)` names a
session and is handed a day. That coincidence is what this ADR removes.

The **now-session** is the session containing the most recent active moment. It
is **open** while `now − session.End < session_gap`, and **paused** while it is
open and the current state is `away`.

At the live edge a session's closure cannot be known immediately: 30 minutes into
an absence, no rule can say whether this is a break or the end of the day. The
uncertainty is confined to a single bit, and never corrupts a boundary:

- A session's `Start` is never provisional.
- An open session's `End` is, by definition, its last active moment.
- Elapsed time only ever makes `open` flip `true → false`. No boundary moves
  retroactively, so the displayed start time can never turn out to have been a
  lie.

A new `now` subcommand serves this unit; `today` stays as the human-facing
logical-day totals. Introducing `now` rather than reinterpreting `today` keeps
each command answering exactly one question.

## 3. Configuration

```toml
[work]
break_minutes       = 10   # existing
session_gap_minutes = 240  # new: away ≥ this ends a session
day_start_hour      = 4    # new: 0–23; 0 = calendar days
```

Validation: `0 ≤ day_start_hour ≤ 23`, and `session_gap_minutes > break_minutes`
(otherwise every break would also terminate its session).

## 4. Wire format

Both projects are pre-1.0 and the GUI bundles its own signed CLI, so the payloads
change without a compatibility shim.

### 4.1 `timeline --json`

Added to the payload root: `session_gap_seconds` and `day_start_hour`.

Added to each day: `day_start_unix` (the instant the logical day begins — the
origin the GUI measures chart offsets from), `sessions[]`, and `blocks[]`.

`blocks[]` tiles each session for hit-testing:
`{kind: "work"|"break", start_unix, end_unix, start, end, seconds, operating_seconds, present_seconds}`.
It exists because hover targeting needs a coarse unit; see the GUI ADR.

`segments[]` now carries only the segments *inside* a day's sessions. Overnight
sleep is no longer drawn as an away bar, because it is no longer part of any
day's work. `work_start` / `work_end` remain wall-clock `"HH:MM"` strings; a
consumer detects a boundary crossing by comparing `work_end_unix` against
`day_start_unix + 86400`.

A `--days N` flag resolves the range against the *logical* today. Without it a
consumer would have to reimplement the boundary rule just to pick a `--since`
date — precisely the derivation `CLAUDE.md` reserves for the engine. It replaces
the GUI's own `calendarSince`.

### 4.2 `now --json`

```json
{
  "state": "operating",
  "recording": true,
  "session": {
    "open": true,
    "paused": false,
    "start_unix": 1783..., "start": "20:44",
    "end_unix": 1783...,   "end": "00:59",
    "active_seconds": 10440,
    "operating_seconds": 9180,
    "present_seconds": 1260,
    "breaks": [ { "start": "21:51", "end": "22:01", "seconds": 600 } ]
  },
  "day": { "date": "2026-07-09", "active_seconds": 18720 }
}
```

`session` is null when no sample has ever been recorded. `day` carries the
logical day the session is filed under, so the popover can show a secondary
"Today" figure without a second query.

## 5. Consequences

**Accepted divergence.** `report`'s per-day totals attribute each *second* to the
logical day it falls in, while `timeline`'s per-day totals attribute each
*session* to the day it started in. The two agree except when a session crosses a
logical boundary — an all-nighter running through 04:00 counts entirely on the
day it began in `timeline`, and splits across two days in `report`. This is the
intended difference between a work log and a totals ledger, not an inconsistency
to paper over.

**All-nighters are filed on the night they began.** Work from 20:00 to 09:00 is
one session on the starting day; the following morning shows no work of its own.

**Pathological worst case.** A session that never self-terminates is cut at its
second logical boundary, so it can reach ~47h59m and the intervening logical day
shows no work at all. This only occurs when the display never sleeps and the
machine is never locked for two days.

**The menu-bar headline resets each session.** With the now-session as the
headline unit, waking the Mac at 07:26 after a night's sleep shows `0s`, not the
hours accumulated since midnight. This is the point of the change — the number
answers "how long this stretch" — but it is a visible behaviour change on the
most-glanced surface. The logical-day total moves to a secondary row.

**An open session's `end` is a moving target, its `start` is not.** A consumer
polling `now` sees `end` advance with activity and `open` flip to `false` once
the absence passes `session_gap`. It never sees `start` change.

**Raw samples are untouched.** Thresholds and boundaries are applied at
aggregation time, so the entire recorded history re-derives under the new rules
with no migration.

**Chart columns may exceed 24 hours.** Handled in the GUI ADR.

## 6. Alternatives rejected

**`day_start_hour` alone.** Two lines of change, and it does fix all three
symptoms of §1. But the boundary is a fixed wall-clock instant: an all-nighter
that runs to 05:00 is cut at 04:00 and reproduces symptom 1 exactly, four hours
later. The parameter is also uncomfortably sensitive — 04:00 versus 05:00 decides
the outcome precisely on the nights the user cares about.

**Sessionization alone.** No arbitrary clock boundary, but two failures. It
cannot bound the never-sleeping-display session of §1. And filing by the
*calendar* date of the session start puts a session that begins at 01:00 on the
following day, which is the same misfiling this ADR set out to remove.

## 7. Test plan

Table-driven, in `core/aggregate`:

- A session crossing calendar midnight stays whole and keeps its real start/end.
- Overnight sleep terminates a session and never appears in `breaks`.
- A short away (< `break_minutes`) folds into continuous work; one between the
  two thresholds becomes a break; one ≥ `session_gap` splits the session.
- Two sessions on one logical day: the inter-session gap is not a break.
- An all-nighter through 04:00 crosses one boundary and stays whole.
- A session reaching a second boundary is cut there; the tail opens a new session.
- A session starting at 01:00 files under the previous logical day.
- `day_start_hour = 0` files a session under its start's calendar date, and still
  keeps a midnight-crossing session whole.
- `session_gap_minutes ≤ break_minutes` is a config error.

For the now-session (`cmd`, with an injected clock):

- Active right now → the session is open and not paused.
- Away for less than `session_gap` → open and paused; `end` holds at the last
  active moment and `active_seconds` stops growing.
- Away for at least `session_gap` → closed; `start` is unchanged from what an
  earlier call reported.
- No samples at all → `session` is null.
- `--days N` resolves `since` against the logical today, not the calendar today.
