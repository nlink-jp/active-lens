package cmd

import (
	"fmt"
	"io"
	"time"

	"github.com/nlink-jp/active-lens/core/activity"
	"github.com/nlink-jp/active-lens/core/aggregate"
)

// jsonTotals is the wire form of aggregate.Totals (integer seconds).
type jsonTotals struct {
	OperatingSeconds int64 `json:"operating_seconds"`
	PresentSeconds   int64 `json:"present_seconds"`
	AwaySeconds      int64 `json:"away_seconds"`
}

type jsonDay struct {
	Date   string     `json:"date"`
	Totals jsonTotals `json:"totals"`
}

// jsonReport is the full report payload the GUI consumes via `--json`.
type jsonReport struct {
	Since       string       `json:"since"`
	Until       string       `json:"until"`
	Timezone    string       `json:"timezone"`
	SampleCount int          `json:"sample_count"`
	Total       jsonTotals   `json:"total"`
	Days        []jsonDay    `json:"days"`
	HourOfDay   []jsonTotals `json:"hour_of_day"` // exactly 24 entries, index = local hour
}

// jsonSegment is one contiguous state span in a day's timeline.
type jsonSegment struct {
	StartUnix int64  `json:"start_unix"`
	EndUnix   int64  `json:"end_unix"`
	Start     string `json:"start"` // "HH:MM" local
	End       string `json:"end"`
	State     string `json:"state"`
}

// jsonBreak is an away span counted as a work break.
type jsonBreak struct {
	StartUnix int64  `json:"start_unix"`
	EndUnix   int64  `json:"end_unix"`
	Start     string `json:"start"`
	End       string `json:"end"`
	Seconds   int64  `json:"seconds"`
}

// jsonBlock is a session's coarse structure — the unit a pointer can hit. Raw
// segments alternate every few minutes and are sub-pixel on a day column.
type jsonBlock struct {
	Kind             string `json:"kind"` // "work" | "break"
	StartUnix        int64  `json:"start_unix"`
	EndUnix          int64  `json:"end_unix"`
	Start            string `json:"start"`
	End              string `json:"end"`
	Seconds          int64  `json:"seconds"`
	OperatingSeconds int64  `json:"operating_seconds"` // 0 for a break block
	PresentSeconds   int64  `json:"present_seconds"`
}

// jsonSession is one unbroken stretch of work, never split at a day boundary.
type jsonSession struct {
	StartUnix        int64       `json:"start_unix"`
	EndUnix          int64       `json:"end_unix"`
	Start            string      `json:"start"`
	End              string      `json:"end"`
	OperatingSeconds int64       `json:"operating_seconds"`
	PresentSeconds   int64       `json:"present_seconds"`
	ActiveSeconds    int64       `json:"active_seconds"`
	Breaks           []jsonBreak `json:"breaks"`
}

// jsonDayTimeline is one logical day's spans plus the derived work markers.
type jsonDayTimeline struct {
	Date             string        `json:"date"`
	DayStartUnix     int64         `json:"day_start_unix"` // origin for chart offsets
	HasWork          bool          `json:"has_work"`
	WorkStartUnix    int64         `json:"work_start_unix"` // 0 when no work
	WorkEndUnix      int64         `json:"work_end_unix"`   // may exceed day_start+86400
	WorkStart        string        `json:"work_start"`      // "HH:MM" or ""
	WorkEnd          string        `json:"work_end"`
	OperatingSeconds int64         `json:"operating_seconds"`
	PresentSeconds   int64         `json:"present_seconds"`
	ActiveSeconds    int64         `json:"active_seconds"` // operating + present
	SpanSeconds      int64         `json:"span_seconds"`   // work_end - work_start
	Sessions         []jsonSession `json:"sessions"`
	Segments         []jsonSegment `json:"segments"`
	Blocks           []jsonBlock   `json:"blocks"`
	Breaks           []jsonBreak   `json:"breaks"`
}

// jsonTimeline is the `timeline --json` payload the GUI renders.
type jsonTimeline struct {
	Since                 string            `json:"since"`
	Until                 string            `json:"until"`
	Timezone              string            `json:"timezone"`
	SampleCount           int               `json:"sample_count"`
	BreakThresholdSeconds int64             `json:"break_threshold_seconds"`
	SessionGapSeconds     int64             `json:"session_gap_seconds"`
	DayStartHour          int               `json:"day_start_hour"`
	Days                  []jsonDayTimeline `json:"days"`
}

// buildTimeline aggregates samples into per-logical-day timelines. Pure. The day
// series is DENSE: every logical day in [since, until] appears, with days that
// have no activity emitted as empty entries (has_work=false) so the caller can
// render a continuous calendar rather than only the days that happened to have
// data.
func buildTimeline(samples []activity.Sample, since, until time.Time, p aggregate.Params, loc *time.Location) jsonTimeline {
	days := aggregate.Timeline(samples, p, loc)
	byDate := make(map[string]aggregate.DayTimeline, len(days))
	for _, d := range days {
		byDate[d.Date] = d
	}

	dates := logicalDates(since, until, loc, p.DayStartHour)
	tl := jsonTimeline{
		Since:                 aggregate.LogicalDate(since, p.DayStartHour),
		Until:                 aggregate.LogicalDate(until.Add(-time.Second), p.DayStartHour),
		Timezone:              loc.String(),
		SampleCount:           len(samples),
		BreakThresholdSeconds: int64(p.BreakThreshold.Seconds()),
		SessionGapSeconds:     int64(p.SessionGap.Seconds()),
		DayStartHour:          p.DayStartHour,
		Days:                  make([]jsonDayTimeline, 0, len(dates)),
	}
	for _, date := range dates {
		if d, ok := byDate[date]; ok {
			tl.Days = append(tl.Days, toJSONDayTimeline(d, loc))
		} else {
			tl.Days = append(tl.Days, emptyJSONDayTimeline(date, loc, p.DayStartHour))
		}
	}
	return tl
}

// hm renders a wall-clock "HH:MM" in loc.
func hm(t time.Time, loc *time.Location) string { return t.In(loc).Format("15:04") }

func toJSONBreaks(bs []aggregate.WorkBreak, loc *time.Location) []jsonBreak {
	out := make([]jsonBreak, 0, len(bs))
	for _, b := range bs {
		out = append(out, jsonBreak{
			StartUnix: b.Start.Unix(), EndUnix: b.End.Unix(),
			Start: hm(b.Start, loc), End: hm(b.End, loc), Seconds: int64(b.Duration().Seconds()),
		})
	}
	return out
}

// toJSONDayTimeline converts an aggregated logical day into its wire form.
func toJSONDayTimeline(d aggregate.DayTimeline, loc *time.Location) jsonDayTimeline {
	jd := jsonDayTimeline{
		Date:             d.Date,
		DayStartUnix:     d.DayStart.Unix(),
		HasWork:          d.HasWork,
		OperatingSeconds: int64(d.OperatingSeconds),
		PresentSeconds:   int64(d.PresentSeconds),
		ActiveSeconds:    int64(d.ActiveSeconds()),
		SpanSeconds:      int64(d.SpanSeconds()),
		Sessions:         []jsonSession{},
		Segments:         []jsonSegment{},
		Blocks:           []jsonBlock{},
		Breaks:           toJSONBreaks(d.Breaks, loc),
	}
	if d.HasWork {
		jd.WorkStartUnix = d.WorkStart.Unix()
		jd.WorkEndUnix = d.WorkEnd.Unix()
		jd.WorkStart = hm(d.WorkStart, loc)
		jd.WorkEnd = hm(d.WorkEnd, loc)
	}
	for _, s := range d.Sessions {
		jd.Sessions = append(jd.Sessions, jsonSession{
			StartUnix: s.Start.Unix(), EndUnix: s.End.Unix(),
			Start: hm(s.Start, loc), End: hm(s.End, loc),
			OperatingSeconds: int64(s.OperatingSeconds),
			PresentSeconds:   int64(s.PresentSeconds),
			ActiveSeconds:    int64(s.ActiveSeconds()),
			Breaks:           toJSONBreaks(s.Breaks, loc),
		})
	}
	for _, s := range d.Segments {
		jd.Segments = append(jd.Segments, jsonSegment{
			StartUnix: s.Start.Unix(), EndUnix: s.End.Unix(),
			Start: hm(s.Start, loc), End: hm(s.End, loc), State: string(s.State),
		})
	}
	for _, b := range d.Blocks {
		jd.Blocks = append(jd.Blocks, jsonBlock{
			Kind:      string(b.Kind),
			StartUnix: b.Start.Unix(), EndUnix: b.End.Unix(),
			Start: hm(b.Start, loc), End: hm(b.End, loc),
			Seconds:          int64(b.Duration().Seconds()),
			OperatingSeconds: int64(b.OperatingSeconds),
			PresentSeconds:   int64(b.PresentSeconds),
		})
	}
	return jd
}

// emptyJSONDayTimeline is a placeholder for a logical day with no activity.
func emptyJSONDayTimeline(date string, loc *time.Location, dayStartHour int) jsonDayTimeline {
	return jsonDayTimeline{
		Date:         date,
		DayStartUnix: dayStartFor(date, loc, dayStartHour).Unix(),
		HasWork:      false,
		Sessions:     []jsonSession{},
		Segments:     []jsonSegment{},
		Blocks:       []jsonBlock{},
		Breaks:       []jsonBreak{},
	}
}

// dayStartFor is the instant the logical day named by date begins. date is
// always a well-formed "YYYY-MM-DD" produced by logicalDates.
func dayStartFor(date string, loc *time.Location, dayStartHour int) time.Time {
	d, err := time.ParseInLocation("2006-01-02", date, loc)
	if err != nil {
		return time.Time{}
	}
	y, m, dd := d.Date()
	return time.Date(y, m, dd, dayStartHour, 0, 0, 0, loc)
}

// logicalDates lists every logical day from since's day through until's last
// covered day (until is an exclusive upper bound), inclusive, in loc.
func logicalDates(since, until time.Time, loc *time.Location, dayStartHour int) []string {
	start := aggregate.LogicalDayStart(since.In(loc), dayStartHour)
	end := aggregate.LogicalDayStart(until.Add(-time.Second).In(loc), dayStartHour)
	var out []string
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		out = append(out, d.Format("2006-01-02"))
	}
	return out
}

// printTimelineHuman writes a compact per-day work log. loc turns the wire's
// unix stamps back into wall-clock dates, so "22:00 → 01:00" can say whose 01:00.
func printTimelineHuman(w io.Writer, tl jsonTimeline, loc *time.Location) {
	fmt.Fprintf(w, "Timezone: %s    Range: %s → %s    (%d samples)\n",
		tl.Timezone, tl.Since, tl.Until, tl.SampleCount)
	fmt.Fprintf(w, "Day starts at %02d:00; a session ends after %s away.\n",
		tl.DayStartHour, formatSeconds(tl.SessionGapSeconds))
	for _, d := range tl.Days {
		if !d.HasWork {
			fmt.Fprintf(w, "\n%s   (no work recorded)\n", d.Date)
			continue
		}
		// A session that ran past midnight ends on the following calendar date.
		next := ""
		if time.Unix(d.WorkEndUnix, 0).In(loc).Format("2006-01-02") != d.Date {
			next = " (+1d)"
		}
		fmt.Fprintf(w, "\n%s   %s → %s%s   active %s",
			d.Date, d.WorkStart, d.WorkEnd, next, formatSeconds(d.ActiveSeconds))
		if n := len(d.Sessions); n > 1 {
			fmt.Fprintf(w, "   · %d sessions", n)
		}
		if len(d.Breaks) > 0 {
			var total int64
			for _, b := range d.Breaks {
				total += b.Seconds
			}
			fmt.Fprintf(w, "   · %d break(s) %s", len(d.Breaks), formatSeconds(total))
		}
		fmt.Fprintln(w)
		for _, b := range d.Breaks {
			fmt.Fprintf(w, "    break %s–%s (%s)\n", b.Start, b.End, formatSeconds(b.Seconds))
		}
	}
}

// --- now ------------------------------------------------------------------

// jsonNowSession is the session containing the most recent active moment.
type jsonNowSession struct {
	Open             bool        `json:"open"`   // now - end < session_gap
	Paused           bool        `json:"paused"` // open, but not active right now
	StartUnix        int64       `json:"start_unix"`
	EndUnix          int64       `json:"end_unix"`
	Start            string      `json:"start"`
	End              string      `json:"end"`
	ActiveSeconds    int64       `json:"active_seconds"`
	OperatingSeconds int64       `json:"operating_seconds"`
	PresentSeconds   int64       `json:"present_seconds"`
	Breaks           []jsonBreak `json:"breaks"`
}

// jsonNowDay is the logical day the now-session is filed under.
type jsonNowDay struct {
	Date          string `json:"date"`
	ActiveSeconds int64  `json:"active_seconds"`
}

// jsonNow is the `now --json` payload: what the menu bar asks for.
type jsonNow struct {
	State     string          `json:"state"`     // "" when nothing recorded
	Recording bool            `json:"recording"` // a sample landed recently
	Session   *jsonNowSession `json:"session"`   // null when nothing recorded
	Day       jsonNowDay      `json:"day"`
}

// buildNow derives the now-session from the sample window. Pure: `now` and the
// staleness cutoff are injected so the derivation is fully testable.
//
// A session's Start is never provisional and an open session's End is by
// definition its last active moment, so elapsed time only ever flips `open`
// from true to false. No boundary moves retroactively.
func buildNow(samples []activity.Sample, now time.Time, staleAfter time.Duration, p aggregate.Params, loc *time.Location) jsonNow {
	out := jsonNow{Day: jsonNowDay{Date: aggregate.LogicalDate(now.In(loc), p.DayStartHour)}}

	var last activity.Sample
	for _, s := range samples {
		if s.TS.After(last.TS) {
			last = s
		}
	}
	if !last.TS.IsZero() {
		out.State = string(last.State)
		out.Recording = now.Sub(last.TS) <= staleAfter
	}

	sessions := aggregate.Sessions(samples, p, loc)
	if len(sessions) == 0 {
		return out
	}
	cur := sessions[len(sessions)-1]

	open := now.Sub(cur.End) < p.SessionGap
	activeNow := out.Recording && (last.State == activity.Operating || last.State == activity.Present)
	out.Session = &jsonNowSession{
		Open:             open,
		Paused:           open && !activeNow,
		StartUnix:        cur.Start.Unix(),
		EndUnix:          cur.End.Unix(),
		Start:            hm(cur.Start, loc),
		End:              hm(cur.End, loc),
		ActiveSeconds:    int64(cur.ActiveSeconds()),
		OperatingSeconds: int64(cur.OperatingSeconds),
		PresentSeconds:   int64(cur.PresentSeconds),
		Breaks:           toJSONBreaks(cur.Breaks, loc),
	}

	// The day figure is the logical day the session is filed under — not the day
	// "now" falls in, which differ during an all-nighter.
	date := aggregate.LogicalDate(cur.Start, p.DayStartHour)
	out.Day.Date = date
	for _, s := range sessions {
		if aggregate.LogicalDate(s.Start, p.DayStartHour) == date {
			out.Day.ActiveSeconds += int64(s.ActiveSeconds())
		}
	}
	return out
}

// printNowHuman writes the now-session as one short block.
func printNowHuman(w io.Writer, n jsonNow) {
	if n.Session == nil {
		if n.State == "" {
			fmt.Fprintln(w, "No activity recorded yet.")
			fmt.Fprintln(w, "\nIs the daemon running? Try: active-lens status")
		} else {
			fmt.Fprintf(w, "No work session in the last %dh (currently %s).\n",
				int(nowWindow.Hours()), n.State)
		}
		return
	}
	s := n.Session
	state := "closed"
	switch {
	case s.Open && s.Paused:
		state = "paused"
	case s.Open:
		state = "open"
	}
	fmt.Fprintf(w, "Session  %s → %s   (%s)\n", s.Start, s.End, state)
	fmt.Fprintf(w, "  active     %s   (operating %s, present %s)\n",
		formatSeconds(s.ActiveSeconds), formatSeconds(s.OperatingSeconds), formatSeconds(s.PresentSeconds))
	if len(s.Breaks) > 0 {
		var total int64
		for _, b := range s.Breaks {
			total += b.Seconds
		}
		fmt.Fprintf(w, "  breaks     %d · %s\n", len(s.Breaks), formatSeconds(total))
		for _, b := range s.Breaks {
			fmt.Fprintf(w, "               %s–%s (%s)\n", b.Start, b.End, formatSeconds(b.Seconds))
		}
	}
	fmt.Fprintf(w, "  today      %s   (%s)\n", formatSeconds(n.Day.ActiveSeconds), n.Day.Date)
	switch {
	case n.Recording:
		fmt.Fprintf(w, "\nCurrently %s · recording\n", n.State)
	case n.State != "":
		fmt.Fprintf(w, "\nNot recording · last sample was %s\n", n.State)
	}
}

// --- status / report ------------------------------------------------------

// statusJSON is the machine-readable form of `status`, consumed by the GUI.
type statusJSON struct {
	DaemonInstalled  bool    `json:"daemon_installed"`
	DaemonLoaded     bool    `json:"daemon_loaded"`
	DaemonLabel      string  `json:"daemon_label"`
	ConfigPath       string  `json:"config_path"`
	DBPath           string  `json:"db_path"`
	IntervalSeconds  int     `json:"interval_seconds"`
	ThresholdSeconds float64 `json:"threshold_seconds"`
	MaxGapSeconds    int     `json:"max_gap_seconds"`
	LastSampleUnix   int64   `json:"last_sample_unix"`  // 0 when no samples yet
	LastSampleState  string  `json:"last_sample_state"` // "" when no samples yet
}

func toJSONTotals(t aggregate.Totals) jsonTotals {
	return jsonTotals{
		OperatingSeconds: int64(t.Operating.Seconds()),
		PresentSeconds:   int64(t.Present.Seconds()),
		AwaySeconds:      int64(t.Away.Seconds()),
	}
}

// buildReport aggregates samples over [since, until] into the report payload.
// Pure: no I/O, deterministic for given inputs. Day buckets are logical days.
func buildReport(samples []activity.Sample, since, until time.Time, maxGap time.Duration, loc *time.Location, dayStartHour int) jsonReport {
	total := aggregate.Range(samples, maxGap, loc)
	days := aggregate.ByDay(samples, maxGap, loc, dayStartHour)
	hod := aggregate.ByHourOfDay(samples, maxGap, loc)

	rep := jsonReport{
		Since:       aggregate.LogicalDate(since.In(loc), dayStartHour),
		Until:       aggregate.LogicalDate(until.Add(-time.Second).In(loc), dayStartHour),
		Timezone:    loc.String(),
		SampleCount: len(samples),
		Total:       toJSONTotals(total),
		Days:        make([]jsonDay, 0, len(days)),
		HourOfDay:   make([]jsonTotals, 24),
	}
	for _, d := range days {
		rep.Days = append(rep.Days, jsonDay{Date: d.Date, Totals: toJSONTotals(d.Totals)})
	}
	for h := range 24 {
		rep.HourOfDay[h] = toJSONTotals(hod[h])
	}
	return rep
}

// printReportHuman writes a compact human-readable summary.
func printReportHuman(w io.Writer, rep jsonReport) {
	fmt.Fprintf(w, "Timezone: %s    Range: %s → %s    (%d samples)\n\n",
		rep.Timezone, rep.Since, rep.Until, rep.SampleCount)
	fmt.Fprintf(w, "Total    %s\n", formatTotalsLine(rep.Total))
	if len(rep.Days) > 0 {
		fmt.Fprintln(w, "\nBy day")
		for _, d := range rep.Days {
			fmt.Fprintf(w, "  %s   %s\n", d.Date, formatTotalsLine(d.Totals))
		}
	}
}

// formatTotalsLine renders one totals row as "operating Xh Ym  present ...  away ...".
func formatTotalsLine(t jsonTotals) string {
	return fmt.Sprintf("operating %s   present %s   away %s",
		formatSeconds(t.OperatingSeconds),
		formatSeconds(t.PresentSeconds),
		formatSeconds(t.AwaySeconds))
}

// formatSeconds renders a second count at a resolution that stays legible at any
// scale: seconds under a minute, whole minutes under an hour, else "Xh YYm".
// Sub-minute values show seconds so short sessions and a just-started session
// don't collapse to "0m".
func formatSeconds(sec int64) string {
	if sec < 60 {
		return fmt.Sprintf("%ds", sec)
	}
	mins := (sec + 30) / 60 // round to nearest minute
	if mins < 60 {
		return fmt.Sprintf("%dm", mins)
	}
	return fmt.Sprintf("%dh %02dm", mins/60, mins%60)
}

// resolveRange turns optional YYYY-MM-DD flags into a [since, until] window in
// loc, aligned to logical days. Empty since defaults to 6 logical days before
// today (a 7-day window); empty until defaults to now. An explicit until is
// inclusive (extended to the end of that logical day).
func resolveRange(sinceStr, untilStr string, loc *time.Location, dayStartHour int) (since, until time.Time, err error) {
	now := time.Now().In(loc)
	today := aggregate.LogicalDayStart(now, dayStartHour)

	if sinceStr == "" {
		since = today.AddDate(0, 0, -6)
	} else {
		d, e := time.ParseInLocation("2006-01-02", sinceStr, loc)
		if e != nil {
			return since, until, fmt.Errorf("--since: %w", e)
		}
		since = dayStartFor(d.Format("2006-01-02"), loc, dayStartHour)
	}

	if untilStr == "" {
		until = now
	} else {
		d, e := time.ParseInLocation("2006-01-02", untilStr, loc)
		if e != nil {
			return since, until, fmt.Errorf("--until: %w", e)
		}
		until = dayStartFor(d.Format("2006-01-02"), loc, dayStartHour).AddDate(0, 0, 1)
	}

	if until.Before(since) {
		return since, until, fmt.Errorf("--until (%s) is before --since (%s)",
			until.Format("2006-01-02"), since.Format("2006-01-02"))
	}
	return since, until, nil
}

// resolveLastDays turns --days N into a [since, until] window ending now and
// spanning N logical days, so a consumer never has to reimplement the boundary.
func resolveLastDays(n int, loc *time.Location, dayStartHour int) (since, until time.Time, err error) {
	if n <= 0 {
		return since, until, fmt.Errorf("--days: want positive integer, got %d", n)
	}
	now := time.Now().In(loc)
	return aggregate.LogicalDayStart(now, dayStartHour).AddDate(0, 0, -(n - 1)), now, nil
}
