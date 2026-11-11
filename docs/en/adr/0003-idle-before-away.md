# ADR 0003 — No input just before an absence counts as the absence

**Status:** Accepted
**Date:** 2026-11-11
**Targets:** segment derivation (`core/aggregate`), configuration (`core/config`), every aggregating command (`cmd`)
**Amends:** RFP §7, "the present/away boundary depends on the display-sleep setting" (resolves the documented limitation)

---

## 1. Context

A state is decided by two signals. Thirty seconds without input is *present*; the
display going off or the screen locking is *away*. Leaving the desk does not turn
the display off, so **the time between getting up and the display sleeping is
counted as active time**. RFP §7 records this as a known limitation.

Measured (one Mac, 125 days of raw samples, display sleep at 10 minutes, checked
with `pmset -g`):

| measured | result |
|---|---|
| a present directly followed by an away | 735 times, 121.2 h |
| of which the present lasted 570 s or 585 s (± a few seconds) | 618 times, 99.6 h |
| of which longer than 10 minutes | 53 times, 19.5 h |
| of which shorter than 9 minutes | 64 times, 2.3 h |

570 / 585 s is "display sleep 600 s − input threshold 30 s", plus the jitter of a
15-second sampling interval: the mark of **the display-sleep timer turning the
display off**. A present longer than 10 minutes means something (a video, a call)
held the display awake.

For the 618 of nine to ten minutes, the away that followed lasted:

| following away | count |
|---|---|
| under 1 minute | 68 |
| 1 minute or more | 550 |

The 68 where the display came back within a minute read naturally as someone who
was there (reading), saw the display go off, and woke it.

## 2. Decision

**A present segment is counted as away when all three hold.**

1. The segment directly after it is away (no hole between them).
2. It is at most `idle_before_away` long (default 10 minutes).
3. The away after it has lasted at least 60 seconds.

The meaning: input stopped, the display went off, and it did not come back — so
the absence began when input stopped.

- Condition 2 leaves a long no-input stretch in which a video or a call held the
  display awake (53 times, 19.5 h measured) as present. A machine whose display
  sleeps sooner than 10 minutes leaves a shorter mark, which the same limit
  catches. A machine set to sleep later needs the setting raised to match.
- Condition 3 leaves the case of someone waking the display again (68 times
  measured) as present. Sixty seconds is a constant, not a setting.

Raw samples are untouched. The rule is applied at aggregation time, so recorded
history re-derives under it.

### 2.1 Promises

- **Every command derives the same segment list from the same sample stream.**
  `report` / `today` (per-second totals) and `timeline` / `now` (session
  derivation) compute from the list one function returns. At the edges of what
  was read, the rule sees only what was read (the residual of §3.3).
- The rule moves time in one direction only: present → away. Operating is never
  changed, and away never becomes present or operating.
- With `idle_before_away_minutes = 0` the rule does nothing and the output is
  what it was before this ADR.

### 2.2 Out of scope

- Recording the display-sleep setting, or why an away began (timer, lock, lid),
  at sample time. It changes the schema, and history does not have it.
- Reading the current setting from `pmset` at aggregation time. The current
  setting is not the setting in force when the history was recorded.
- Telling "reading" from "left the desk" within ten minutes of no input. There
  is no signal for it.
- Changing the GUI. Segments become away; no existing wire field changes (§5).

## 3. Detailed design

### 3.1 Rewriting the segments (`core/aggregate`)

```go
// reattributeIdleBeforeAway turns qualifying present segments into away and
// re-merges adjacent same-state segments. Pure.
func reattributeIdleBeforeAway(segs []Segment, limit, confirm time.Duration) []Segment
```

- Input: the merged segment list `Segments` builds today (in time order; not
  assumed free of holes).
- Steps: for each `i`, when `segs[i].State == Present`, `segs[i+1].State == Away`,
  `segs[i].End.Equal(segs[i+1].Start)`, `segs[i].Duration() <= limit` and
  `segs[i+1].Duration() >= confirm`, set `segs[i].State = Away`. Every decision
  is taken on the list as given (one rewrite never enables the next). Then merge
  adjacent, contiguous, same-state segments.
- `limit <= 0` returns the input unchanged.
- `confirm` is the constant `idleConfirm = 60 * time.Second`.
- A present at the end of the list (nothing after it) is left alone.

away → present → away (a display that lit with no input) also becomes away when
it qualifies, and merges with the aways on either side into one.

### 3.2 One segment list for both aggregation paths

Today `Segments` (timeline / now) and `walkSegments` (report / today) each carry
their own copy of the same attribution.

- `Params` gains `IdleBeforeAway time.Duration`.
- `Segments` takes `Params` instead of `maxGap` (`Segments(samples, p, loc)`) and
  passes its merged list through `reattributeIdleBeforeAway`. Callers
  (`Sessions`, tests) follow.
- The list as recorded stays available inside the package as
  `rawSegments(samples, maxGap, loc)`.
- `walkSegments` drops its own attribution and feeds `fn` the result of
  `Segments`, split at hour boundaries. `Range` / `ByDay` / `ByHourOfDay` take
  `Params` instead of `maxGap`.
- **`StartsAfterABreak` judges on `rawSegments` (before the rewrite).** The head
  of a read truncates the present it lands in, so judged on the rewritten list a
  present that is really long can be counted as away and fuse with the away
  after it into a session-ending absence that does not exist. The rewrite only
  ever lengthens an away, so an absence found in the raw list is always still
  there afterwards; the reverse only ever reads further back.

### 3.3 The edges of a read — left alone

> **Superseded by [ADR 0005](0005-report-reads-what-timeline-reads.md)**, which
> has `report` / `today` total from the read `timeline` uses. The residual
> described here no longer exists; the section is kept as the record of why it
> was first accepted.

A segment is judged by what surrounds it, so the result can differ at the edges
of what was read.

- A present that straddles the start of the read looks shorter than it is. A
  no-input stretch really longer than the limit (a video, say) still has its
  visible part counted as away if that part is within the limit and an away
  follows.
- An away just past the end of the read is not seen. A present running into the
  end stays present.

**Neither the read window nor how totals are taken changes.** Padding the read
and clipping to the window would change the output even with the setting at 0
(today only the span between the first and last sample inside the window is
counted), and the padding would vary with the setting — so it is not done (§8).
The two readings differ only when the change from no input to away falls exactly
across an edge of the read (for `today` and `report`, the logical day boundary,
04:00 by default), and by at most the limit (10 minutes) each time. Accepted as
a residual.

When `until` is now, the present before an away in progress stays present until
that away reaches 60 seconds (§6).

### 3.4 Configuration (`core/config`)

```toml
[work]
# When input stops and the display then goes off (or the screen locks), the
# no-input stretch before it — up to this many minutes — counts as away.
# Match it to the display-sleep timeout. 0 turns it off.
# idle_before_away_minutes = 10
```

- A non-negative integer. A negative or non-integer value is an error (worded
  like the existing keys).
- `doctor` prints the resolved value.

### 3.5 Errors

No new failure path. Only the validation of one setting is added.

## 4. Development plan

| step | what | done when |
|---|---|---|
| 1 | add `reattributeIdleBeforeAway` | the rule's unit tests in §7 pass |
| 2 | put `Segments` / `walkSegments` on one list; thread `Params` through | the existing tests pass with `IdleBeforeAway = 0`, not one expectation changed |
| 3 | the setting, `doctor`, the `timeline --json` field | the configuration tests in §7 pass |
| 4 | default to 10; README (en + ja), `config.example.toml`, CHANGELOG, AGENTS.md, a note at RFP §7 | the documents match the behaviour |
| 5 | check against the real record | the numbers in §1 (how many, how long) are reproduced by the implementation's output |
| 6 | one independent verification before release, asking only about conformance to this ADR | its findings are triaged |

Step 2 is kept apart from the default (step 4) so that the existing tests show
the unification changed nothing before the rule is switched on.

## 5. Wire format

No existing field changes. `timeline --json` gains one value in its header,
beside `break_threshold_seconds`.

```json
{ "break_threshold_seconds": 600, "session_gap_seconds": 14400, "idle_before_away_seconds": 600 }
```

In `segments`, a re-counted stretch appears as `away`. It carries no mark.

## 6. Consequences

- **The numbers change, history included.** Measured: present drops by 90.9 h
  (21%), active time (operating + present) by 8.5%, across 613 re-counted
  stretches.
- **There are more breaks.** About ten minutes of no input now joins the absence,
  so an away that used to fold into work for being under ten minutes becomes a
  break (169 measured). The real absence was over ten minutes.
- **Breaks begin, and the working day ends, about ten minutes earlier.**
- **A session in progress has its end and its active time move back after you
  leave.** `now` re-counts the idle once the display has been off for 60
  seconds: the session's `end` returns to the moment input stopped, and active
  time drops by up to ten minutes.
- **A session with no input can be withdrawn.** After a long absence, a display
  that lights with no input (present) and goes off again is a session of its own
  today. Under this ADR, 60 seconds after it goes off that present becomes away
  and the session never was; until then `now` shows the new session. **The
  invariant in AGENTS.md — a now-session's `start` is never provisional, no
  boundary moves retroactively — is amended for these two cases** (the `start` of
  a session that had input still never moves).
- **A session can split.** A re-counted idle merges with the aways around it, and
  if together they exceed `session_gap` the session ends there (3 h away → 5 min
  of no input → 2 h away, for example).
- **The edge residual** is as §3.3 says.
- **Missed on one side.** Locking the screen yourself and leaving within ten
  minutes of the last input turns that no-input stretch — possibly time spent
  reading — into absence. Measured, the stretches under 9 minutes were 64 times,
  2.3 h. Accepted.
- **Left alone on the other.** Leaving within ten minutes of a video ending, and
  the like, cannot be told apart.
- **A GUI that bundles this CLI shows the old numbers until it is rebuilt and
  released after the CLI** (CONVENTIONS, Release Checklist 11).

The measurements are one machine's record with display sleep at 10 minutes. The
distribution on a machine set otherwise was not measured.

## 7. Test plan

Every judge is an expected value against a hand-built sample stream.

**The rule (unit)**
- present 9.5 min → away 5 min: the present becomes away, merged into one.
- present 9.5 min → away 30 s: unchanged.
- present 12 min → away 1 h: unchanged.
- present exactly 10 min / away exactly 60 s: changed (both bounds inclusive).
- operating → away: unchanged. present → operating: unchanged.
- away → present 3 min → away 5 min: one away.
- a present and an away with a hole between them: unchanged.
- a trailing present: unchanged. `limit = 0`: the input.

**The paths agree**
- For one sample stream, `Range`'s totals equal `Segments` summed by state.
- `ByDay`'s days sum to `Range`.

**The anchor (`StartsAfterABreak`)**
- The stream opens on 5 minutes of present (really the tail of a 3-hour idle) →
  an away 3 minutes short of `session_gap` → operating: returns false (a
  combination that returns true on the rewritten list).
- The existing `TestSamplesForWindowDoNotDependOnWhereTheReadBegins` passes.

**Sessions**
- 5 h away → present 3 min → 1 h away → operating: the session begins on the
  operating, not on the present.
- operating → present 8 min → away 20 min: the session's work ends where the
  operating ends; the break is 28 minutes.

**Configuration**
- The default is 10. `0` is off. A negative or non-numeric value is an error (a
  quoted number reads as a number, as with the existing keys).

**Compatibility**
- With `IdleBeforeAway = 0` the existing tests pass with their expectations
  unchanged. The read window and how totals are taken do not change, so neither
  does any command's output.

## 8. Alternatives rejected

- **Count every present before an away as absence, with no length limit.** It
  would turn locking the screen after a video or a call (53 times, 19.5 h
  measured) into absence.
- **Ignore how long the following away lasted.** It would turn the 68 times
  (about 11 h) someone woke the display again into absence.
- **Record the display-sleep setting at sample time / read it at aggregation
  time.** As §2.2 says.
- **Pad the read at its edges and clip to the window before totalling.** It
  removes the edge disagreement, but changes `report` / `today` totals even with
  the setting at 0 (the fractions at both ends of the window, a sleep straddling
  `since`). The padding needed behind is the limit + 60 s + `max_gap`, so the
  read window would vary with the setting. Heavy machinery for a rare slip of at
  most ten minutes.
- **Fix it in the GUI.** Aggregation is the engine's job, and `report` and the
  GUI would disagree.
- **Show "long no input" as a third state.** Measured, about half of it was this
  ADR's phenomenon, the rest was small, and the classification was sensitive to
  detail.
