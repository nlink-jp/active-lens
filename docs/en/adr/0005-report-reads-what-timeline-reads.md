# ADR 0005 — `report` / `today` total from the read `timeline` uses

**Status:** Accepted
**Date:** 2026-11-11
**Targets:** the read and the totals of `report` / `today` (`cmd`, `core/aggregate`, `core/store`)
**Amends:** ADR 0003 §3.3 and ADR 0004 §5 (both accepted, as "the edge residual", a disagreement this removes)

---

## 1. Context

ADR 0003 and 0004 added rules that decide a span's state by what surrounds it.
`timeline` / `now` derive their segments from a read that reaches past the
window on both sides (`samplesForWindow`); `report` / `today` read only the
samples inside the window they are given. At the start and end of a window the
two families could therefore disagree about the same instant.

- A brief visit in the first `break_minutes` of the window: `report` / `today`
  do not see the away before it and count it as activity (at most `brief_visit`,
  2 minutes by default).
- A long no-input stretch straddling the start: it looks short, so it is counted
  as away (at most `idle_before_away`, 10 minutes by default).
- An idle or a visit running into the end: the away after it is not seen, so it
  stays activity.

Both ADRs accepted this as a residual, on the ground that padding the read and
clipping to the window changes the output even with the rules off. The owner's
decision (2026-11-11): **fix this disagreement too.** That the output changes is
accepted.

`report` / `today` also had another property. Totals counted only "from the first
sample inside the window to the last". A sleep straddling the start (asleep at
01:00, awake at 08:00 — the hours 04:00 to 08:00 of that day) was counted as no
state at all, and the day totalled less than 24 hours.

## 2. Decision

**`report` / `today` derive their segments from the read `timeline` uses
(`samplesForWindow`), cut them to the window `[since, until)`, and total that.**

### 2.1 Promises

- **For every instant in the window, the state `report` / `today` count is the
  state `aggregate.Segments` derives from the sample stream `timeline` reads for
  the same `--since` / `--until`.** The two families apply one function to one
  read. Wherever `timeline` emits that instant in `segments`, the states agree.
  (`timeline`'s `segments` are per session, so an away outside every session is
  not in them, and the continuation of a session begun the day before is under
  the day before. How time is filed to a day — `report` each second to its own
  logical day, `timeline` each session to the day it began — is ADR 0001 / 0002's
  design, and this ADR does not change it.)
- **For a window between the record's first sample and its last, the totals
  equal the window's length.** However long the record was interrupted (a week
  with the machine off), that time is counted as away. Only the time before the
  first sample ever, and after the last, is not counted.
- **A day's figures do not depend on the `--since` / `--until` it was asked
  with.**
- Raw samples are untouched.

### 2.2 What changes (whatever the rules are set to)

With `idle_before_away_minutes = 0` and `brief_visit_minutes = 0` the output of
`report` / `today` still changes, in these ways.

- A sleep or a break in the record straddling the start of the window is counted
  as away from the start (it used to be from the first sample inside).
- The state of the sample just before the window is counted from the start to
  the next sample (at most `max_gap`).
- For a past window, the time from the last sample inside to the end of the
  window is counted too.
- A day spent wholly asleep (no sample in it) appears in `days` as a day of away
  (it used to be absent from the array).
- `sample_count` counts the samples in `[since, until)` (it used to include a
  sample exactly at `until`, which was then counted by both of two adjacent
  windows; it now counts as `timeline` does).

ADR 0003 §2.1's and ADR 0004 §2.1's "with 0 the output is what it was before
this ADR" still holds for the derivation of segments (`Segments`). For the output
of `report` / `today`, this ADR changes the points above.

### 2.3 Out of scope

- How time is filed to a day (per second / per session).
- `now`, `timeline`, `status`, `export`.
- The GUI.

## 3. Detailed design

### 3.1 Clipping (`core/aggregate`)

```go
// Clip cuts a segment list down to [since, until). Pure.
func Clip(segs []Segment, since, until time.Time) []Segment
```

- For each segment, `start = max(seg.Start, since)` and
  `end = min(seg.End, until)`. Those with `start < end` are returned with their
  state, in order. The input is not modified.
- When `until` is not after `since`, the result is empty.

### 3.2 Totals from a segment list (`core/aggregate`)

- `walkSegments` takes a segment list instead of samples
  (`walkSegments(segs, loc, fn)`).
- Functions that total a segment list are added: `RangeOf(segs, loc)`,
  `ByDayOf(segs, loc, dayStartHour)`, `ByHourOfDayOf(segs, loc)`.
- The existing `Range` / `ByDay` / `ByHourOfDay` (taking samples) stay as thin
  wrappers that derive `Segments` and call those. Their results are unchanged.

### 3.3 The read includes one sample on each side (`core/store`, `cmd`)

`samplesForWindow` reads a fixed span either side of the window (48 hours +
`session_gap` behind it; in front, widened from there until a break is found, and
stopped when widening finds nothing earlier). When the record is interrupted for
longer than that, a window inside the interruption has no sample in what was
read, and nothing in it is counted — while the same day, asked for with a window
reaching an end of the interruption, is counted as away. That breaks the second
and third promises of §2.1.

- `store` gains two queries: `LastBefore(t)` (the last sample before `t`) and
  `FirstAfter(t)` (the first sample after `t`). Each answers "none" when there is
  none. One-row queries on the existing index (`idx_samples_ts`).
- Once its read `[a, b]` is settled, `samplesForWindow` adds `LastBefore(a)` and
  `FirstAfter(b)` in front of and behind the samples read. `FirstAfter(b)` is not
  added when it lies after now.
- **The sample in front is added as a marker of where the record resumes from,
  in the away state.** Added with its own state, that state is counted for up to
  `max_gap`, a sliver of activity at least 48 hours before the window; when
  nothing has been done since, `now` shows that sliver as the last session
  (found by a test during implementation). As away it opens no session and
  leaves the first active segment — the instant sessions and their day-boundary
  cuts are measured from — where it was. What it miscounts as away is at most
  `max_gap`, outside any window's clip.
- An interruption becomes away as the interval between two samples, by
  `rawSegments`' existing `max_gap` handling. Before the record's first sample
  and after its last there is no sample to add, and nothing is counted.
- **The output of `timeline` / `now` does not change.** The sample in front is an
  away marker and adds no activity. The one behind has no interval after it, so
  it adds no activity either, only the time up to it.
- The read is **always** held to now. It was not when `until` lay after now
  (`--until <today>` makes `until` the start of tomorrow), and samples stamped in
  the future were read.

### 3.4 Commands (`cmd`)

- `emitReport` (the path `report` and `today` share) uses
  `samplesForWindow(st, since, until, cfg, loc)` in place of
  `st.Query(since, until)`. When the read hits its cap without finding a break it
  says so on stderr through `noteIfCapped`, as `timeline` does. That line spoke of
  "session boundaries"; it is reworded to fit `report` / `today` too ("the
  derivation starts from that point").
- `buildReport(samples, since, until, p, loc)` makes
  `segs := aggregate.Clip(aggregate.Segments(samples, p, loc), since, until)` and
  totals it with `RangeOf` / `ByDayOf` / `ByHourOfDayOf`.
- `sample_count` is `countInRange(samples, since, until)` (`[since, until)`, the
  function `timeline` uses).
- The hint under the human-readable form, "No samples in this range. Is the
  daemon running?", does not fit a range with no samples but with away counted
  (time asleep). That sentence is kept for a total of zero; a range with no
  samples but with away says that nothing was recorded during it (the machine
  was asleep, or the daemon was not running).
- The shape of the output (the JSON fields) does not change.

### 3.5 Errors

A failure of `LastBefore` / `FirstAfter` is the command's error, like a failure
of the existing query. There is no other new failure path.

## 4. Development plan

| step | what | done when |
|---|---|---|
| 1 | `Clip`, `RangeOf` / `ByDayOf` / `ByHourOfDayOf`; the existing functions become wrappers | the unit tests in §6 pass and `core/aggregate`'s existing tests pass with their expectations unchanged |
| 2 | the two `store` queries, and `samplesForWindow`'s neighbours and its hold at now (§3.3) | the "interruption" and "unchanged" tests in §6 pass |
| 3 | `emitReport` / `buildReport` as §3.4 says | the command-level tests in §6 pass; `cmd`'s existing `buildReport` tests, whose samples lie inside the window, pass with their expectations unchanged |
| 4 | the relevant entries leave "Limitations" in the README (en + ja); CHANGELOG, AGENTS.md, and a "resolved by ADR 0005" note at ADR 0003 §3.3 and ADR 0004 §5 | the documents match the behaviour |
| 5 | check against the real record | for each past logical day, `report`'s operating + present equals the `segments` of a `timeline` read from two days earlier (including a continuation filed under the day before), cut to that day and summed; a past day between the record's first and last sample totals 24 hours; `timeline` / `now` output equals what it was before |
| 6 | one independent verification before release (conformance to this ADR; separately, any defect noticed) | its findings are triaged and the defects fixed |

## 5. Consequences

- **`report` / `today` and `timeline` no longer disagree at the edges of a
  window.**
- **`report` / `today` can show more away.** A sleep straddling the start of the
  window is counted from the start (§2.2). On a machine whose record is
  continuous (set never to sleep) almost nothing changes: on the record this was
  checked against (125 days), operating and present were identical and away rose
  by 6 seconds.
- **A past day totals 24 hours** (23 / 25 on the day the clocks change).
- **`report` / `today` read more.** 48 hours + `session_gap` either side of the
  window, and further back until a break is found (14 days at most).
- `report` / `today`, too, print one line on stderr when the read hits its cap
  without a break.
- The GUI does not call `report` / `today` (it uses `now`, `status` and
  `timeline` only). It is not affected.
- `report` / `today` go from one query to three or four (the neighbours
  included).

## 6. Test plan

The judges are expected values against hand-built sample streams, and agreement
with "the `Segments` of the whole stream, cut to the window and summed".

**Clipping (unit)**
- A segment inside the window, one straddling the start, one straddling the end,
  one covering the window, one outside it.
- `until <= since`: empty. The caller's slice is not rewritten.

**Wrappers (unit)**
- `Range(samples, p, loc)` equals `RangeOf(Segments(samples, p, loc), loc)` (and
  likewise `ByDay`, `ByHourOfDay`).

**The edges of a window (command level, through `samplesForWindow` and `buildReport`)**
- A 30-second visit three minutes into the logical day, long absences around it:
  the day's `report` counts none of it as operating.
- Twelve minutes of no input straddling the start, then away: the part inside the
  window stays present.
- A 30-second visit in the last five minutes, then a long absence: not counted.
- The window ends on 9 min 30 s of no input and the away begins right after it:
  that idle is away.
- In each, `report`'s totals equal "derived from the whole and cut".

**Coverage**
- A sleep (a break in the samples) straddling the start: away from the start, and
  the totals equal the window's length.
- The record's first sample lies part-way through the window: nothing is counted
  before it.
- `until` is now and the last sample a little earlier: nothing is counted after
  it.

**Interruptions (§3.3)**
- The record is interrupted for a week. One day inside it, asked for alone:
  24 hours, all away.
- That day asked for alone and asked for inside a window reaching an end of the
  interruption: the same figures.
- The day the record resumes: away from the start of the window to the first
  sample.
- A day before the record's first sample, time after its last: nothing counted.
- `until` after now (`--until <today>`) with samples stamped in the future: not
  read.

**Unchanged**
- With the neighbours added, each day's entry in `timeline` and the output of
  `now` equal what they are without (for a stream with an interruption, one with
  an anchor, one without, one that begins in activity).
- `sample_count` is the number of samples in `[since, until)` and excludes one
  exactly at `until`.
- The existing `buildReport` tests, whose samples lie inside the window, pass
  with their expectations unchanged.

## 7. Alternatives rejected

- **Pad the read by a fixed margin (the rules' limits plus a little).** The
  margin needed varies with the rules' settings, and the read differs from
  `timeline`'s, so "the same instant, the same state" cannot be guaranteed by
  construction. Using `timeline`'s read, the guarantee follows directly from
  passing one function the same samples.
- **Accept the residual.** ADR 0003 / 0004's judgement; the owner overturned it.
- **Switch the rules off at the edges of `report` / `today`.** The edges would
  mix states with and without the rules, agreeing with neither.
- **Add the sample in front with its own state.** It opens a sliver of a session
  that `now` can pick up (§3.3).
