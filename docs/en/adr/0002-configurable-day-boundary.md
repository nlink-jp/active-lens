# ADR 0002 — Configurable day boundary for session attribution

**Status:** Accepted
**Date:** 2026-09-17
**Targets:** the `timeline` / `now` derivation (`core/aggregate`, `core/config`, `cmd`)
**Amends:** ADR 0001 §2.3 (attribution), §2.4 (backstop), §5 (accepted divergence)
**Companion:** `active-lens-gui` ADR 0002 — the two must ship together.

---

## 1. Context

ADR 0001 files a session, whole, under the logical day it started in, and never
cuts one at a day boundary. For a personal log that is right: an evening that
runs to 01:00 is that evening's work, and an all-nighter belongs to the night it
began.

It is wrong when the boundary is not the user's own. Where the working day is
defined by someone else — a company whose day starts at 05:00 — the boundary is
a rule about *which day work counts against*, not a heuristic for where a log
reads best. Under ADR 0001, work that runs from 22:00 through 09:00 is filed
entirely under the previous day: the hours after 05:00 are merged into a day
that, by the employer's rule, had already ended.

Only part of the tool has this problem:

| path | attributes | boundary honoured |
|---|---|---|
| `report`, `today` | each **second**, to its logical day (ADR 0001 §2.6) | yes |
| `timeline` | each **session**, to the day it started in (§2.3) | no |
| `now` | the day figure of the session's start day (§2.7) | no |

`timeline` and `now` are what the GUI renders, so the merge is what the user
actually sees. ADR 0001 §5 named this divergence and accepted it. This ADR does
not overturn that judgement — it makes the choice explicit, because the right
answer depends on whose boundary it is.

## 2. Decision

Add `work.day_boundary`, an enum over the attribution rule. Like every other
derivation knob it is applied at aggregation time, so switching it re-derives
the whole recorded history with no migration.

### 2.1 `session` — the default, unchanged

ADR 0001 §2.3 and §2.4 verbatim: a session is filed whole under the logical day
it started in, and the only cut is the backstop at the second boundary.

### 2.2 `strict` — the boundary ends the session

Every logical day boundary ends the session in progress and opens a new one at
that instant. Each piece is filed under its own logical day.

Two consequences follow directly:

- `timeline`'s day totals become equal to `report`'s day totals. The divergence
  of ADR 0001 §5 exists only in `session` mode.
- A session can no longer outlive the logical day it is filed under, so the §2.4
  backstop can never fire. The backstop is kept rather than made conditional: it
  is the same mechanism with a different limit, and one code path is cheaper than
  two. (A logical day is 24h except across a daylight-saving change, where the
  boundary is a wall-clock hour and the day is 23 or 25 — see §5.)

### 2.3 The now-session follows the same rule

Under `strict` the menu bar's heading resets at the boundary: at 05:00, work in
progress becomes a new session and the figure returns to `0s`. This is not a
side effect to be papered over — it is the point. In a workplace with a defined
day, the number worth glancing at is the one that will be counted against today.

`now`'s `day` figure remains the logical day of the current session's start. Under
`strict` that session cannot straddle a boundary, so the figure is always the day
the session itself belongs to. It is not necessarily the day `now` falls in: after
a night's sleep, before any new activity, the current session is still yesterday's
last one, exactly as under ADR 0001.

### 2.4 Cuts are labelled, not hidden: `carried_in` / `carried_out`

ADR 0001 §6 rejected "`day_start_hour` alone" because a cut becomes a fake
`work_start`. That objection stands, and `strict` does produce a `work_start` of
exactly 05:00 on a day whose work began the evening before. The answer is not to
avoid the cut but to say so.

Every session carries two booleans:

- `carried_in` — the session begins at the boundary it was cut at, i.e. work was
  already in progress when the day turned over. Its `Start` is a cut, not a start.
- `carried_out` — the session ends at a boundary cut, i.e. work continued past it.

`carried_in` requires the session to begin *exactly* at that boundary. If the
user was away at 05:00 and resumed at 05:20, the day begins on real activity and
neither flag is set — the cut left no artefact to label.

A day inherits the flags of its first and last session. Both flags are also set
in `session` mode when the §2.4 backstop cuts, so the default mode gets the same
honesty about its one artificial boundary.

## 3. Configuration

```toml
[work]
day_boundary = "session"   # or "strict"
```

Validation: exactly one of the two spellings. Anything else is a config error —
no silent fallback, because a typo that quietly keeps the old attribution is
precisely the failure this option exists to prevent.

**Why the default stays `session`.** The behaviour is unchanged for anyone not
asking for the other one, every already-recorded all-nighter keeps reading the
way it reads today, and the personal case — the one the tool was built for — is
the one ADR 0001 got right. A workplace boundary is a deployment fact, and
deployment facts belong in config.

## 4. Wire format

Both projects are pre-1.0 and the GUI bundles its own signed CLI, so the payload
changes without a compatibility shim.

- `timeline --json`: root gains `day_boundary`; every day and every session gain
  `carried_in` and `carried_out`.
- `now --json`: the session gains `carried_in`, so the menu bar can explain a `0s`
  heading ("the day turned over at 05:00") instead of looking like it lost the
  session. There is no `carried_out` here: a session the boundary cut is never the
  current one, since the work continued into the session opened at that instant.
- `status --json`: gains `day_start_hour` and `day_boundary`.
- `timeline` (human): the header states the mode when it is not the default — the
  default rule is not news, and a line printed every run is a line nobody reads;
  `doctor` answers it on demand. A carried day appends
  `· continues from previous day` / `· continues into next day` to its work-log
  line. Plain words, no glyphs — the line is already `→` and `·` dense.

## 5. Consequences

**`strict` makes two ledgers agree.** `timeline` and `report` stop disagreeing
about all-nighters. That is the whole point, and it costs the property ADR 0001
valued: a night's work is no longer one object.

**The menu bar resets mid-work.** Under `strict`, at 05:00, with hands on the
keyboard. `carried_in` is what lets the GUI say why.

**A three-minute day is a real day.** Work that runs to 05:03 produces a day with
three minutes of work, starting exactly at the boundary, `carried_in = true`.
That is what a strict boundary means.

**Raw samples are untouched.** Switching modes re-derives everything already
recorded; there is nothing to migrate and nothing to lose by trying it.

**Daylight saving bends the day, as it must.** The boundary is a wall-clock hour,
so across a DST change the logical day is 23 or 25 hours long and a `strict`
session may run to 25h. Seconds are still conserved and both ledgers still agree;
only the "24h" in the sentences above is approximate. Where the configured hour
does not exist on a spring-forward day, Go resolves it to the nearest real
instant, which puts that one boundary an hour off. Japan has no DST; this is a
caveat for other zones, not a defect being papered over.

**Reading a window means reading before it.** A session that began before the
first instant of a `--days N` range must be derived whole, or the range's oldest
day reports a cut as a real start. The timeline path therefore queries the store
from `since − (48h + session_gap)` — the span that provably contains any session
touching that instant — and emits only the days inside the range.

## 6. Alternatives rejected

**A grace period** (don't cut unless more than N minutes fall past the
boundary). It reintroduces a fudge factor into a rule whose entire value is that
it is not ours to fudge. A three-minute day is honest; a three-minute day
silently merged into yesterday is the original defect at a smaller scale.

**Keep sessions whole, cut only the day totals.** The menu bar would not reset
mid-work, but `now` and `timeline` would need two different derivations of the
same history, and one session would appear in two days' ledgers. One rule applied
everywhere is worth the reset.

**Make `strict` the default.** Nobody is asking for it as a default, and it
changes how every already-recorded all-nighter reads. Opt-in.

**A `--day-boundary` CLI flag.** Every derivation knob here is config-only, and a
flag for one knob invites a flag per knob. Editing `config.toml` and re-running
re-derives from the raw samples, which is the same experiment with no new
surface.

## 7. Test plan

Table-driven in `core/aggregate`, alongside the ADR 0001 cases.

- `strict`: a session crossing the boundary is cut there; the halves file under
  their own logical days; their totals sum to the uncut session's.
- `strict` and `session`: the cut also happens when the boundary falls *between*
  two segments — a state change or a `max_gap` split landing exactly on the hour —
  and the limit advances afterwards, so no later boundary is missed either. This
  is the case that a `seg.Start < boundary < seg.End` test alone silently skips.
- The oldest day of a window keeps its `carried_in`: the derivation reads the
  stream from before the range it displays, and `sample_count` still counts only
  the samples inside the range.
- `strict`: the head's `End` is the boundary with `carried_out`, the tail's
  `Start` is the boundary with `carried_in`.
- `strict`: away across the boundary sets neither flag; each day still begins and
  ends on real activity.
- `strict`: `timeline` day totals equal `report` day totals over the same window
  — ADR 0001 §5's divergence test, inverted.
- `session` (default): every ADR 0001 case still passes; the backstop cut now
  sets `carried_out` / `carried_in`.
- config: both spellings parse; any other value is an error; unset is `session`.
- `now` under `strict`: the session starts at the boundary, `active_seconds`
  restarts, `carried_in` is true, and `day.date` is the logical day that session
  belongs to.
