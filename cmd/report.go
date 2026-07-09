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

// jsonDayTimeline is one day's spans plus the derived work-session markers.
type jsonDayTimeline struct {
	Date             string        `json:"date"`
	HasWork          bool          `json:"has_work"`
	WorkStartUnix    int64         `json:"work_start_unix"` // 0 when no work
	WorkEndUnix      int64         `json:"work_end_unix"`
	WorkStart        string        `json:"work_start"` // "HH:MM" or ""
	WorkEnd          string        `json:"work_end"`
	OperatingSeconds int64         `json:"operating_seconds"`
	PresentSeconds   int64         `json:"present_seconds"`
	ActiveSeconds    int64         `json:"active_seconds"` // operating + present
	SpanSeconds      int64         `json:"span_seconds"`   // work_end - work_start
	Segments         []jsonSegment `json:"segments"`
	Breaks           []jsonBreak   `json:"breaks"`
}

// jsonTimeline is the `timeline --json` payload the GUI renders.
type jsonTimeline struct {
	Since                 string            `json:"since"`
	Until                 string            `json:"until"`
	Timezone              string            `json:"timezone"`
	SampleCount           int               `json:"sample_count"`
	BreakThresholdSeconds int64             `json:"break_threshold_seconds"`
	Days                  []jsonDayTimeline `json:"days"`
}

// buildTimeline aggregates samples into per-day timelines. Pure. The day series
// is DENSE: every calendar day in [since, until] appears, with days that have no
// activity emitted as empty entries (has_work=false) so the caller can render a
// continuous calendar rather than only the days that happened to have data.
func buildTimeline(samples []activity.Sample, since, until time.Time, maxGap, breakThreshold time.Duration, loc *time.Location) jsonTimeline {
	days := aggregate.Timeline(samples, maxGap, breakThreshold, loc)
	byDate := make(map[string]aggregate.DayTimeline, len(days))
	for _, d := range days {
		byDate[d.Date] = d
	}

	dates := calendarDates(since, until, loc)
	tl := jsonTimeline{
		Since:                 since.In(loc).Format("2006-01-02"),
		Until:                 until.In(loc).Format("2006-01-02"),
		Timezone:              loc.String(),
		SampleCount:           len(samples),
		BreakThresholdSeconds: int64(breakThreshold.Seconds()),
		Days:                  make([]jsonDayTimeline, 0, len(dates)),
	}
	for _, date := range dates {
		if d, ok := byDate[date]; ok {
			tl.Days = append(tl.Days, toJSONDayTimeline(d, loc))
		} else {
			tl.Days = append(tl.Days, emptyJSONDayTimeline(date))
		}
	}
	return tl
}

// toJSONDayTimeline converts an aggregated day into its wire form.
func toJSONDayTimeline(d aggregate.DayTimeline, loc *time.Location) jsonDayTimeline {
	hm := func(t time.Time) string { return t.In(loc).Format("15:04") }
	jd := jsonDayTimeline{
		Date:             d.Date,
		HasWork:          d.HasWork,
		OperatingSeconds: int64(d.OperatingSeconds),
		PresentSeconds:   int64(d.PresentSeconds),
		ActiveSeconds:    int64(d.ActiveSeconds()),
		SpanSeconds:      int64(d.SpanSeconds()),
		Segments:         []jsonSegment{},
		Breaks:           []jsonBreak{},
	}
	if d.HasWork {
		jd.WorkStartUnix = d.WorkStart.Unix()
		jd.WorkEndUnix = d.WorkEnd.Unix()
		jd.WorkStart = hm(d.WorkStart)
		jd.WorkEnd = hm(d.WorkEnd)
	}
	for _, s := range d.Segments {
		jd.Segments = append(jd.Segments, jsonSegment{
			StartUnix: s.Start.Unix(), EndUnix: s.End.Unix(),
			Start: hm(s.Start), End: hm(s.End), State: string(s.State),
		})
	}
	for _, b := range d.Breaks {
		jd.Breaks = append(jd.Breaks, jsonBreak{
			StartUnix: b.Start.Unix(), EndUnix: b.End.Unix(),
			Start: hm(b.Start), End: hm(b.End), Seconds: int64(b.Duration().Seconds()),
		})
	}
	return jd
}

// emptyJSONDayTimeline is a placeholder for a calendar day with no activity.
func emptyJSONDayTimeline(date string) jsonDayTimeline {
	return jsonDayTimeline{Date: date, HasWork: false, Segments: []jsonSegment{}, Breaks: []jsonBreak{}}
}

// calendarDates lists every "YYYY-MM-DD" from since's day through the last day in
// the range (until is an exclusive upper bound; its last-covered day is
// until-1s), inclusive, in loc.
func calendarDates(since, until time.Time, loc *time.Location) []string {
	start := startOfDay(since.In(loc))
	end := startOfDay(until.Add(-time.Second).In(loc))
	var out []string
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		out = append(out, d.Format("2006-01-02"))
	}
	return out
}

// printTimelineHuman writes a compact per-day work log.
func printTimelineHuman(w io.Writer, tl jsonTimeline) {
	fmt.Fprintf(w, "Timezone: %s    Range: %s → %s    (%d samples)\n",
		tl.Timezone, tl.Since, tl.Until, tl.SampleCount)
	for _, d := range tl.Days {
		if !d.HasWork {
			fmt.Fprintf(w, "\n%s   (no work recorded)\n", d.Date)
			continue
		}
		fmt.Fprintf(w, "\n%s   %s → %s   active %s",
			d.Date, d.WorkStart, d.WorkEnd, formatSeconds(d.ActiveSeconds))
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
// Pure: no I/O, deterministic for given inputs.
func buildReport(samples []activity.Sample, since, until time.Time, maxGap time.Duration, loc *time.Location) jsonReport {
	total := aggregate.Range(samples, maxGap, loc)
	days := aggregate.ByDay(samples, maxGap, loc)
	hod := aggregate.ByHourOfDay(samples, maxGap, loc)

	rep := jsonReport{
		Since:       since.In(loc).Format("2006-01-02"),
		Until:       until.In(loc).Format("2006-01-02"),
		Timezone:    loc.String(),
		SampleCount: len(samples),
		Total:       toJSONTotals(total),
		Days:        make([]jsonDay, 0, len(days)),
		HourOfDay:   make([]jsonTotals, 24),
	}
	for _, d := range days {
		rep.Days = append(rep.Days, jsonDay{Date: d.Date, Totals: toJSONTotals(d.Totals)})
	}
	for h := 0; h < 24; h++ {
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
// Sub-minute values show seconds so short sessions and a just-started `today`
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

// startOfDay returns local midnight of t's date.
func startOfDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

// resolveRange turns optional YYYY-MM-DD flags into a [since, until] window in
// loc. Empty since defaults to 6 days before today (a 7-day window); empty until
// defaults to now. An explicit until is inclusive (extended to end-of-day).
func resolveRange(sinceStr, untilStr string, loc *time.Location) (since, until time.Time, err error) {
	now := time.Now().In(loc)
	today := startOfDay(now)

	if sinceStr == "" {
		since = today.AddDate(0, 0, -6)
	} else {
		d, e := time.ParseInLocation("2006-01-02", sinceStr, loc)
		if e != nil {
			return since, until, fmt.Errorf("--since: %w", e)
		}
		since = startOfDay(d)
	}

	if untilStr == "" {
		until = now
	} else {
		d, e := time.ParseInLocation("2006-01-02", untilStr, loc)
		if e != nil {
			return since, until, fmt.Errorf("--until: %w", e)
		}
		until = startOfDay(d).AddDate(0, 0, 1) // inclusive end-of-day
	}

	if until.Before(since) {
		return since, until, fmt.Errorf("--until (%s) is before --since (%s)",
			until.Format("2006-01-02"), since.Format("2006-01-02"))
	}
	return since, until, nil
}
