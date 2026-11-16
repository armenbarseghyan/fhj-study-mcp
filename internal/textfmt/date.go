// Package textfmt formats times and text for human-readable tool output.
// Times are shown in one display zone (Zone), labelled with its abbreviation.
package textfmt

import (
	"time"
	_ "time/tzdata" // embed the zone database: the binary must not depend on the host's
)

// Vienna is the zone of the FH JOANNEUM Moodle site and the default display zone.
var Vienna = mustLoad("Europe/Vienna")

// Zone is the time zone every date is shown in and calendar days are counted
// in. It defaults to Vienna; the server sets the user's zone at start-up
// (SetZone), so someone abroad sees deadlines in local time.
// It must not change while tools run.
var Zone = Vienna

// SetZone sets the display zone; nil keeps the current one.
func SetZone(loc *time.Location) {
	if loc != nil {
		Zone = loc
	}
}

func mustLoad(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

var weekdays = [...]string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}

// Date formats t as "2026-10-07 17:15 CEST (Wednesday)" in the display zone.
// The zone abbreviation is always shown: a bare "23:59" would be read as the
// reader's local time.
func Date(t time.Time) string {
	v := t.In(Zone)
	return v.Format("2006-01-02 15:04 MST") + " (" + P().Sprintf(weekdays[v.Weekday()]) + ")"
}

// Day formats t as "2026-10-07" in the display zone.
func Day(t time.Time) string { return t.In(Zone).Format("2006-01-02") }

// DayStart returns midnight (display zone) of t's calendar day shifted by offset days.
// It is DST-safe: DayStart(t, 1) is the next calendar midnight even when that
// day has 23 or 25 hours.
func DayStart(t time.Time, offset int) time.Time {
	v := t.In(Zone)
	return time.Date(v.Year(), v.Month(), v.Day()+offset, 0, 0, 0, 0, Zone)
}

// Relative describes t relative to now using calendar days of the display zone:
// "in 40 min", "today, in 3 h", "tomorrow", "in 4 days",
// "just now", "15 min ago", "today, 3 h ago", "yesterday", "5 days ago".
func Relative(t, now time.Time) string {
	p := P()
	d := t.Sub(now)
	days := civilDay(t) - civilDay(now)
	if d >= 0 {
		switch {
		case d < time.Minute:
			return p.Sprintf("now")
		case d < time.Hour:
			return p.Sprintf("in %d min", int(d.Minutes()))
		case days == 0:
			return p.Sprintf("today, in %d h", int(d.Hours()))
		case days == 1:
			return p.Sprintf("tomorrow")
		default:
			return p.Sprintf("in %d days", days)
		}
	}
	d, days = -d, -days
	switch {
	case d < time.Minute:
		return p.Sprintf("just now")
	case d < time.Hour:
		return p.Sprintf("%d min ago", int(d.Minutes()))
	case days == 0:
		return p.Sprintf("today, %d h ago", int(d.Hours()))
	case days == 1:
		return p.Sprintf("yesterday")
	default:
		return p.Sprintf("%d days ago", days)
	}
}

// civilDay numbers calendar days of the display zone so that differences count midnights.
func civilDay(t time.Time) int {
	y, m, d := t.In(Zone).Date()
	return int(time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Unix() / 86400)
}

// Weekday returns t's weekday in the display zone: "Monday".
func Weekday(t time.Time) string { return P().Sprintf(weekdays[t.In(Zone).Weekday()]) }

// Clock formats t as "17:15" in the display zone.
func Clock(t time.Time) string { return t.In(Zone).Format("15:04") }

// ZoneName is t's zone abbreviation in the display zone: "CET", "+05".
func ZoneName(t time.Time) string { return t.In(Zone).Format("MST") }
