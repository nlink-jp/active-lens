# ADR 0004 — A touch between two absences is not work

**Status:** Accepted
**Date:** 2026-11-11
**Targets:** segment derivation (`core/aggregate`), configuration (`core/config`), every aggregating command (`cmd`)
**Builds on:** ADR 0003 (it works on the same segment list, after 0003's rewrite)

---

## 1. Context

The engine has a shortest away that counts as a break (`break_minutes`) but no
shortest activity that counts as work. A touch in the middle of an absence is
activity, and it changes the shape of the work log.

A real case (confirmed in the raw samples): input stopped at 12:39, there were
30 seconds of input at 15:36, and the next input was at 21:26. Because of those
30 seconds, 12:39–15:36 became "a break inside the session" and the session's
end moved to 15:36. Without them the session ends at 12:39 and the absence runs
unbroken to 21:26.

Measured (one Mac, 125 days, on the segment list after ADR 0003). The lengths of
the 679 runs of activity that had an away of ten minutes or more on both sides:

| length | count |
|---|---|
| up to 30 s | 8 |
| 31–60 s | 13 |
| 61–90 s | 21 |
| 91–120 s | 15 |
| 2–3 min | 22 |
| 3–5 min | 37 |
| 5–10 min | 52 |

**The distribution has no gap.** Nothing in the data says "below here it is only
a touch", so the limit is a judgement. The owner's decision is **2 minutes**
(2026-11-11): the loss per occurrence is bounded by the limit itself, it reaches
the sessions that consisted of 75 to 105 seconds alone (4 measured), and nothing
supports a line at 3 minutes or more.

## 2. Decision

**A run of activity is counted as away, whole, when all three hold.**

A run of activity is a maximal sequence of contiguous non-away segments
(operating, present).

1. Its length, from its first segment's start to its last one's end, is at most
   `brief_visit` (default 2 minutes).
2. The segment directly before it is away and lasts at least `break_minutes`.
3. The segment directly after it is away and lasts at least `break_minutes`.

The meaning: a touch between one break and the next means the break went on.

The decision is taken on the segment list after ADR 0003's rewrite. When someone
comes to the desk, touches the machine briefly and leaves, the no-input stretch
after the touch is already away under 0003, so the run is as long as the machine
was actually touched.

### 2.1 Promises

- ADR 0003 §2.1's "every command derives the same segment list from the same
  sample stream" is kept: this rule, too, works inside `Segments`.
- **This rule turns operating into away.** ADR 0003 moved only present; this ADR
  also moves the operating inside a short run — at most `brief_visit` each time.
- With `brief_visit_minutes = 0` the rule does nothing and the output is what it
  was before this ADR.
- `report` / `today` keep their read window and how they take totals (as in ADR
  0003 §3.3; the edge residual remains in the same form, §5).

### 2.2 Out of scope

- Varying the limit by what the activity was (its share of operating, say).
- Short activity with a break on one side only. Only "a break on both sides" is
  handled.
- Changing the GUI.

## 3. Detailed design

### 3.1 Rewriting the segments (`core/aggregate`)

```go
// dropBriefVisits turns a run of activity of at most brief, with an away of at
// least breakThreshold on both sides, into away, and re-merges adjacent
// same-state segments. Pure.
func dropBriefVisits(segs []Segment, brief, breakThreshold time.Duration) []Segment
```

- Input: the list `reattributeIdleBeforeAway` returned.
- Steps: walk the list; a run `[i, j)` is the longest sequence of non-away
  segments joined by `End.Equal(next.Start)`. Set every segment of `[i, j)` to
  away when all of these hold:
  - `i > 0`, `segs[i-1].State == Away`, `segs[i-1].End.Equal(segs[i].Start)`,
    `segs[i-1].Duration() >= breakThreshold`
  - `j < len(segs)`, `segs[j].State == Away`, `segs[j-1].End.Equal(segs[j].Start)`,
    `segs[j].Duration() >= breakThreshold`
  - `segs[j-1].End.Sub(segs[i].Start) <= brief`
- Every decision is taken on the list as given (one rewrite never enables the
  next). Then merge.
- `brief <= 0` or `breakThreshold <= 0` returns the input unchanged. With no
  break length there is no "between two breaks" to judge (a zero taken literally
  would let an away of any length qualify).
- A run at the head of the list (nothing before it) and one at the tail (nothing
  after it) are left alone.

### 3.2 Wiring

- `Params` gains `BriefVisit time.Duration`.
- `Segments(samples, p, loc)` returns
  `dropBriefVisits(reattributeIdleBeforeAway(rawSegments(...), p.IdleBeforeAway, idleConfirm), p.BriefVisit, p.BreakThreshold)`.
- `StartsAfterABreak` judges on `rawSegments`, as ADR 0003 decided (unchanged).
  Its anchor — an away of at least `session_gap` — is at least `session_gap`
  long in its visible part alone, hence at least `break_minutes`, so even
  truncated by the head of a read it gives the first run's "away before" test
  the answer the whole stream gives. Every later run lies inside the read with
  both its neighbours.

### 3.3 `timeline` reads a session running at the end of the window to its end (a fix to an existing defect)

Verifying this ADR turned up a defect that predates it. `timeline --until <a
past day>` read only up to `until` (the end of that logical day), so **a session
still running there came out cut at `until`**. The same day's end and active time
changed with the `--until` it was asked with (measured: one day's active time was
8.9 h asked for alone and 18.6 h asked for with the two days after). That
contradicts ADR 0001's "a session is filed, whole, under the day it started in".

This ADR's rule and ADR 0003's both look at "the away after", so at that same
edge a visit or an idle near the end of the window was counted or not depending
on what was asked.

- `samplesForWindow` reads `[since − back, until + lookback(cfg)]`.
  `lookback(cfg)` (48 hours + `session_gap`) covers the longest a session can run
  (the day-boundary cut) plus the absence that ends it — the same value as the
  first step of the backward read. A session begun before `until` always ends
  inside it.
- `buildTimeline` still emits only the logical days in `[since, until]`, so a
  session begun after `until` is not in the output. `sample_count` still counts
  `[since, until)`.
- With `until` at now (the GUI's `--days N`, `now`) there is nothing ahead to
  read and nothing changes.
- `report` / `today` are per-second totals and have no sessions. Their read is
  unchanged (the residual in §5).

### 3.4 Configuration (`core/config`)

```toml
[work]
# Activity of at most this many minutes between two breaks is not counted as
# work (the break is taken to have gone on). 0 turns it off.
# brief_visit_minutes = 2
```

- An integer from 0 to the existing `maxMinutes`. Anything else is an error.
- `doctor` prints the resolved value.
- `timeline --json` gains `brief_visit_seconds` in its header, beside
  `idle_before_away_seconds`.

### 3.5 Errors

No new failure path. Only the validation of one setting is added.

## 4. Development plan

| step | what | done when |
|---|---|---|
| 1 | add `dropBriefVisits` and wire it into `Segments` (with `BriefVisit = 0`) | the rule's unit tests in §6 pass and the existing tests pass with their expectations unchanged |
| 2 | `timeline`'s forward read (§3.3) | the "end of the window" tests in §6 pass |
| 3 | the setting, `doctor`, the `timeline --json` field; default to 2 | the configuration and command-level tests in §6 pass |
| 4 | README (en + ja), `config.example.toml`, CHANGELOG, AGENTS.md | the documents match the behaviour |
| 5 | check against the real record | the numbers in §1 and §5 (how many, how many seconds) are reproduced by the implementation's output, the case in §1 comes out as expected, and the day measured in §3.3 reads the same whatever `--until` is |
| 6 | one independent verification before release, asking only about conformance to this ADR | its findings are triaged |

## 5. Consequences

Measured (125 days, limit 2 minutes):

- **57 runs, 4,301 seconds (about 72 minutes) of activity stop being work** —
  about 0.1% of active time.
- **Four sessions that stood alone disappear** (activity of at most 2 minutes
  with an absence of `session_gap` or more on both sides).
- **Absences join and a session ends in two places** (where the aways before and
  after a run together exceed `session_gap`; the case in §1 is one).
- **There are fewer breaks.** Two breaks and the short activity between them
  become one break.
- **A session in progress changes after the fact.** When someone comes to the
  desk, touches the machine briefly and leaves, `now` first shows the session
  resumed (or a new one). It is withdrawn when (a) ADR 0003 has re-counted the
  idle after the touch as away (60 seconds after the display goes off), leaving
  at most 2 minutes of activity, and (b) the absence since input stopped has
  reached `break_minutes` — normally in one step, together with 0003's re-count.
  Until then the display includes the touch plus the idle (up to
  `idle_before_away`) as active time. On a machine whose display sleeps later
  than `idle_before_away`, or with `idle_before_away_minutes = 0`, the idle stays
  in the run, the run exceeds 2 minutes, and nothing is withdrawn. When a session
  made only of a visit is withdrawn, `now`'s `day` (date and active time) returns
  to the previous session's day; under `strict`, the `carried_out` / `carried_in`
  a visit straddling the boundary had set revert with it. The proviso ADR 0003
  added to AGENTS.md's "a now-session's `start` is never provisional" gains: "a
  session made only of activity of at most 2 minutes is withdrawn, and an open
  session's `end` can move back to before a brief visit". The `start` of a
  session with more than 2 minutes of activity does not move.
- **Real short work goes too.** Work done in 2 minutes or less between two breaks
  (a reply, an approval) is not counted. As §1 says, nothing in the data
  separates it, and the loss is at most 2 minutes each time. Accepted.
- **The edge residual (`report` / `today`).** These read only the window they are
  given, so a brief visit in the first `break_minutes` of a logical day (04:00 to
  04:10 by default) has no visible away before it and is counted as activity;
  `timeline` / `now` read from before and do not count it. The disagreement is at
  most `brief_visit` (2 minutes) each time. The same goes for a visit in the last
  `break_minutes` of the window. Accepted as a residual for ADR 0003 §3.3's
  reason: padding the read and clipping would change the output even with the
  rule off.
- **`timeline --until <a past day>` changes (§3.3).** A session that ran past
  that day comes out whole instead of cut. This is a fix.
- **A GUI that bundles this CLI shows the old numbers until it is rebuilt and
  released.**

The measurements are one machine's record.

## 6. Test plan

Every judge is an expected value against a hand-built segment list or sample
stream.

**The rule (unit)**
- away 30 min → operating 30 s → away 30 min: one away.
- away 30 min → operating 60 s + present 60 s (contiguous, exactly 2 min) → away
  30 min: away (the limit is inclusive).
- the same with 2 min 1 s of activity: unchanged.
- the away before / the away after at 9 min 59 s: unchanged in both cases. At
  exactly 10 min: changed.
- a run at the head of the list, a run at the tail: unchanged.
- a hole between the activity and the away: unchanged.
- two short runs in a row (away 30 min → 30 s → away 5 min → 30 s → away 30 min):
  neither changes (the away between them is no break; one pass, and no decision
  uses the result of another).
- `brief = 0`: the input. `breakThreshold = 0`: the input. The caller's slice is
  not rewritten.

**Order with 0003**
- away 30 min → operating 30 s → present 9 min 30 s → away 30 min (as samples):
  0003 turns the present into away, and this rule turns the remaining 30 seconds
  into away. All away.

**Sessions**
- The shape of the case in §1 (work → 3 h away → 30 s → 6 h away → work): two
  sessions, the first ending where its work ends and holding no break. With the
  rule off the first session runs to the 30 seconds and holds a 3-hour break.
- 9 h away → 75 s → 12 h away → work: the 75 seconds are not a session.

**The paths agree**
- `Range`'s totals equal `Segments` summed by state (rule on and off).

**The anchor**
- The existing `TestSamplesForWindowDoNotDependOnWhereTheReadBegins` passes.
- A stream with a short run right after the anchor gives the same sessions read
  from the middle of the anchor as read whole.

**The end of the window (§3.3)**
- A session running across a logical day boundary: `timeline` asked for that day
  alone and `timeline` asked through the next day agree on that day's end, active
  time and breaks.
- A 30-second visit five minutes before the boundary, followed by a long
  absence: not counted, whichever is asked.
- A session begun after `until` does not appear. `sample_count` is unchanged.

**Configuration**
- The default is 2. `0` is off. A negative value, a non-numeric one and one past
  the upper bound are errors.

**Compatibility**
- With `BriefVisit = 0` the existing tests pass with their expectations
  unchanged.

## 7. Alternatives rejected

- **A limit of 1 minute.** 21 runs, about 15 minutes. The sessions of 75 to 105
  seconds alone remain.
- **A limit of 3 minutes or more.** 79 runs and about 131 minutes at 3; 116 and
  about 277 minutes at 5. More short work removed, and nothing to justify the
  line.
- **Keep the activity and ignore it only when deciding where a session ends.**
  It multiplies sessions of 30 seconds standing alone.
- **Add no rule.** Records like the one in §1 remain.
- **Hide it in the GUI.** `report` and the GUI would disagree.
