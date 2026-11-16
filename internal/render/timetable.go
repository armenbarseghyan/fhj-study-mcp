package render

import (
	"net/url"
	"strings"
	"time"

	"github.com/armenbarseghyan/fhj-study-mcp/internal/textfmt"
	"github.com/armenbarseghyan/fhj-study-mcp/internal/timetable"
)

// timetableSite is the timetable's search page, for links the user opens.
const timetableSite = "https://almaty.fh-joanneum.at/stundenplan/"

// TimetableView is what fhj_timetable shows.
type TimetableView struct {
	Query     string
	Groups    []string // the group filter, if any
	From, To  time.Time
	Events    []timetable.Event // already filtered
	Hidden    int               // classes of other groups left out
	Over      int               // classes earlier today that have ended
	FetchedAt time.Time
}

// Timetable renders fhj_timetable: classes by day.
func Timetable(v TimetableView, now time.Time) string {
	d := newDoc()
	d.line("## %s", tr("Classes · %s · %s – %s", v.Query, textfmt.Day(v.From), textfmt.Day(v.To.Add(-time.Nanosecond))))
	var notes []string
	if len(v.Groups) > 0 {
		notes = append(notes, tr("Only %s and classes for everyone.", strings.Join(v.Groups, ", ")))
	}
	if v.Hidden > 0 {
		notes = append(notes, tr("%d classes of other groups hidden.", v.Hidden))
	}
	if len(notes) > 0 {
		d.line("_%s_", strings.Join(notes, " "))
	}

	if len(v.Events) == 0 {
		d.blank()
		if v.Over > 0 || v.Hidden > 0 {
			d.line("No more classes in this period.")
			return d.finish(now, v.FetchedAt)
		}
		d.line("No classes found for %s in this period.", "“"+v.Query+"”")
		d.line("_The timetable answers an empty search like some failures do; if you expected classes, check %s._",
			link("the timetable", searchURL(v.Query)))
		return d.finish(now, v.FetchedAt)
	}

	day := ""
	for _, e := range v.Events {
		if k := textfmt.Day(e.Start); k != day {
			day = k
			d.blank()
			d.line("### %s", dayHeading(e.Start, now))
		}
		d.line("- %s", classLine(e))
	}
	d.blank()
	d.line("_Source: %s (data from Moses; may be incomplete). Room changes can also come as Moodle announcements._",
		link("FH JOANNEUM timetable", searchURL(v.Query)))
	return d.finish(now, v.FetchedAt)
}

func classLine(e timetable.Event) string {
	what := "**" + textfmt.Clock(e.Start) + "–" + textfmt.Clock(e.End) + " " + textfmt.ZoneName(e.Start) + "** " + e.Course
	if e.Kind != "" {
		what += " (" + e.Kind + ")"
	}
	parts := []string{what}
	if !e.Everyone() {
		parts = append(parts, e.Group)
	}
	for _, s := range []string{e.People, e.Room} {
		if s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, " · ")
}

func dayHeading(t, now time.Time) string {
	h := textfmt.Weekday(t) + " " + textfmt.Day(t)
	switch textfmt.Day(t) {
	case textfmt.Day(now):
		h += " · " + tr("today")
	case textfmt.Day(textfmt.DayStart(now, 1)):
		h += " · " + tr("tomorrow")
	}
	return h
}

func searchURL(q string) string { return timetableSite + "?q=" + url.QueryEscape(q) }
