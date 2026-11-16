package study

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/armenbarseghyan/fhj-study-mcp/internal/moodle"
	"github.com/armenbarseghyan/fhj-study-mcp/internal/textfmt"
)

// OverdueLookback is how far back unsubmitted assignments are reported.
const OverdueLookback = 7 * 24 * time.Hour

// DeadlinesResult answers moodle_deadlines.
type DeadlinesResult struct {
	Days      int
	Until     time.Time // exclusive: midnight (display zone) after the last day
	Upcoming  []Deadline
	Overdue   []Deadline // unsubmitted assignments due in the last 7 days
	Hidden    int        // assignments Moodle reported as not accessible
	Warnings  []string   // e.g. "calendar unavailable"
	Errors    []CourseError
	FetchedAt time.Time
}

// Deadlines merges calendar action events with assignment due dates for the
// next `days` calendar days (until the end of the last day in the display zone),
// adds the submission state of every assignment, and optionally reports
// unsubmitted assignments that are overdue by up to 7 days.
func (s *Service) Deadlines(ctx context.Context, days int, includeOverdue, refresh bool) (DeadlinesResult, error) {
	now := s.now()
	res := DeadlinesResult{Days: days, Until: textfmt.DayStart(now, days+1)}
	var stamp oldest

	all, at, err := s.allCourses(ctx, refresh)
	if err != nil {
		return res, err
	}
	stamp.add(at)
	courses := active(all)
	refs := refsByID(all)

	// Day-aligned bounds keep the cache key stable within a day.
	from := textfmt.DayStart(now.Add(-OverdueLookback), 0)
	var (
		events     []moodle.Event
		assigns    Assignments
		eventsErr  error
		assignsErr error
	)
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		f, err := s.src.Events(gctx, from, res.Until, refresh)
		if err != nil {
			if moodle.IsFatal(err) {
				return err
			}
			eventsErr = err
			return nil
		}
		events = f.Value
		stamp.add(f.At)
		return nil
	})
	g.Go(func() error {
		f, err := s.src.Assignments(gctx, ids(courses), refresh)
		if err != nil {
			if moodle.IsFatal(err) {
				return err
			}
			assignsErr = err
			return nil
		}
		assigns = f.Value
		stamp.add(f.At)
		return nil
	})
	if err := g.Wait(); err != nil {
		return res, err
	}
	if eventsErr != nil && assignsErr != nil {
		return res, errors.Join(eventsErr, assignsErr)
	}
	if eventsErr != nil {
		res.Warnings = append(res.Warnings, textfmt.P().Sprintf("calendar unavailable: %s", eventsErr.Error()))
	}
	if assignsErr != nil {
		res.Warnings = append(res.Warnings, textfmt.P().Sprintf("assignment list unavailable, submission status unknown: %s", assignsErr.Error()))
	}
	for _, w := range assigns.Warnings {
		if w.Item == "module" || w.Item == "course" {
			res.Hidden++
		}
	}

	merged := MergeDeadlines(events, assigns.Courses, refs, s.base())
	lookbackStart := now.Add(-OverdueLookback)
	candidates := slices.DeleteFunc(merged, func(d Deadline) bool {
		return d.Due.Before(lookbackStart) || !d.Due.Before(res.Until)
	})

	if err := s.resolveInstances(ctx, candidates, refresh, &stamp); err != nil {
		return res, err
	}

	// Submission state for every assignment and quiz candidate whose activity
	// id is known; the others keep SubmissionUnknown.
	type state struct {
		status Submission
		ext    time.Time
		at     time.Time
	}
	var idx []int
	for i, d := range candidates {
		if hasStatus(d.Key.Module) && d.Instance != 0 {
			idx = append(idx, i)
		}
	}
	states, errs, err := fanOut(ctx, idx, func(ctx context.Context, i int) (state, error) {
		d := candidates[i]
		if d.Key.Module == "quiz" {
			f, err := s.src.QuizAttempts(ctx, d.Instance, refresh)
			return state{status: QuizState(f.Value), at: f.At}, err
		}
		f, err := s.src.SubmissionStatus(ctx, d.Instance, refresh)
		if err != nil {
			return state{}, err
		}
		st, ext := SubmissionState(f.Value)
		return state{status: st, ext: ext, at: f.At}, nil
	})
	if err != nil {
		return res, err
	}
	for j, i := range idx {
		if errs[j] != nil {
			candidates[i].Status = SubmissionUnknown
			s.log.Warn("submission status", "module", candidates[i].Key.Module, "instance", candidates[i].Instance, "err", errs[j])
			continue
		}
		stamp.add(states[j].at)
		candidates[i].Status = states[j].status
		if !states[j].ext.IsZero() {
			candidates[i].Due = states[j].ext // an extension overrides every other date
		}
	}

	for _, d := range candidates {
		switch {
		case !d.Due.Before(now) && d.Due.Before(res.Until):
			res.Upcoming = append(res.Upcoming, d)
		case includeOverdue && d.Due.Before(now) && !d.Due.Before(lookbackStart) &&
			d.Key.Module == "assign" && !d.Status.Done() && d.Status != SubmissionUnknown:
			res.Overdue = append(res.Overdue, d)
		}
	}
	sortDeadlines(res.Upcoming)
	sortDeadlines(res.Overdue)
	res.FetchedAt = stamp.t
	return res, nil
}

func hasStatus(module string) bool { return module == "assign" || module == "quiz" }

// resolveInstances fills in the activity id of assignment and quiz deadlines
// that came from the calendar alone (an event carries only the cmid), using
// the course contents, which map cmid to instance. A course whose contents
// cannot be read leaves its deadlines with Instance 0: their status is then
// unknown, never looked up under a wrong id.
func (s *Service) resolveInstances(ctx context.Context, ds []Deadline, refresh bool, stamp *oldest) error {
	var courseIDs []int
	for _, d := range ds {
		if hasStatus(d.Key.Module) && d.Instance == 0 && d.Course.ID != 0 && !slices.Contains(courseIDs, d.Course.ID) {
			courseIDs = append(courseIDs, d.Course.ID)
		}
	}
	if len(courseIDs) == 0 {
		return nil
	}
	contents, errs, err := fanOut(ctx, courseIDs, func(ctx context.Context, id int) (Fetched[[]moodle.Section], error) {
		return s.src.Contents(ctx, id, refresh)
	})
	if err != nil {
		return err
	}
	instance := map[DeadlineKey]int{}
	for i, f := range contents {
		if errs[i] != nil {
			s.log.Warn("course contents", "course", courseIDs[i], "err", errs[i])
			continue
		}
		stamp.add(f.At)
		for _, sec := range f.Value {
			for _, m := range sec.Modules {
				instance[DeadlineKey{Module: m.ModName, CMID: m.ID}] = m.Instance
			}
		}
	}
	for i := range ds {
		if ds[i].Instance == 0 {
			ds[i].Instance = instance[ds[i].Key]
		}
	}
	return nil
}

// MergeDeadlines combines calendar action events and assignment due dates,
// deduplicating by (module, cmid).
//
// Rules: the calendar's time wins over the assignment's duedate (it includes
// user and group overrides); the assignment's name is preferred over the
// localised event name ("… ist fällig"); assignments without an event are
// kept (the event disappears once submitted); assignments without a due date
// are dropped; grading deadlines (teacher events) are ignored.
func MergeDeadlines(events []moodle.Event, courses []moodle.CourseAssignments, refs map[int]CourseRef, base string) []Deadline {
	byKey := map[DeadlineKey]*Deadline{}
	rank := map[DeadlineKey]int{} // dueRank of the date an entry carries
	var order []DeadlineKey
	add := func(d *Deadline) {
		byKey[d.Key] = d
		order = append(order, d.Key)
	}
	ref := func(id int, fallback string) CourseRef {
		if r, ok := refs[id]; ok {
			return r
		}
		if fallback == "" {
			fallback = "course " + itoa(id)
		}
		return CourseRef{ID: id, Label: fallback}
	}

	for _, c := range courses {
		for _, a := range c.Assignments {
			due := a.DueDate.Time()
			if due.IsZero() {
				continue
			}
			courseID := a.Course
			if courseID == 0 {
				courseID = c.ID
			}
			rank[DeadlineKey{Module: "assign", CMID: a.CMID}] = rankAssignment
			add(&Deadline{
				Key:      DeadlineKey{Module: "assign", CMID: a.CMID},
				Instance: a.ID,
				Course:   ref(courseID, c.FullName),
				Title:    strings.TrimSpace(a.Name),
				URL:      moduleURL(base, "assign", a.CMID),
				Due:      due,
				Status:   SubmissionUnknown,
				Sources:  FromAssignments,
			})
		}
	}

	for _, e := range events {
		if e.ModuleName == "" || e.Instance == 0 || e.EventType == "gradingdue" {
			continue
		}
		// An event's "instance" is the course-module id, not the activity id
		// (Moodle's event exporter sets it from the cm).
		key := DeadlineKey{Module: e.ModuleName, CMID: e.Instance}
		r := dueRank(e.EventType)
		if r < 0 {
			continue // an opening date is not a deadline
		}
		due := e.TimeSort.Time()
		if d, ok := byKey[key]; ok {
			d.Sources |= FromCalendar
			if !due.IsZero() && r > rank[key] {
				d.Due, rank[key] = due, r
			}
			if d.URL == "" {
				d.URL = e.URL
			}
			continue
		}
		title := strings.TrimSpace(e.ActivityName)
		if title == "" {
			title = strings.TrimSpace(e.Name)
		}
		var courseID int
		var courseName string
		if e.Course != nil {
			courseID, courseName = e.Course.ID, e.Course.FullName
		}
		status := SubmissionNotApplicable
		if e.ModuleName == "assign" || e.ModuleName == "quiz" {
			status = SubmissionUnknown
		}
		rank[key] = r
		add(&Deadline{
			Key:     key,
			Course:  ref(courseID, courseName),
			Title:   title,
			URL:     e.URL,
			Due:     due,
			Status:  status,
			Sources: FromCalendar,
		})
	}

	out := make([]Deadline, 0, len(order))
	for _, k := range order {
		out = append(out, *byKey[k])
	}
	sortDeadlines(out)
	return out
}

// The calendar has several events per activity (a quiz opens and closes; a
// completion date may be expected). dueRank orders them by how well they state
// the deadline; an entry keeps the date of its highest-ranked source.
const rankAssignment = 1 // mod_assign duedate, without user/group overrides

func dueRank(eventType string) int {
	switch eventType {
	case "due", "close":
		return 2 // includes overrides
	case "open":
		return -1
	default: // "expectcompletionon" and other dates
		return 1
	}
}

func sortDeadlines(ds []Deadline) {
	slices.SortStableFunc(ds, func(a, b Deadline) int {
		if c := a.Due.Compare(b.Due); c != 0 {
			return c
		}
		if c := strings.Compare(a.Course.Label, b.Course.Label); c != 0 {
			return c
		}
		return strings.Compare(a.Title, b.Title)
	})
}

// QuizState maps the user's quiz attempts to a Submission: a finished attempt
// counts as done, an open one (in progress, or overdue and still to be
// submitted) as in progress; no attempts or only abandoned ones as not done.
func QuizState(attempts []moodle.QuizAttempt) Submission {
	st := SubmissionNotSubmitted
	for _, a := range attempts {
		switch a.State {
		case moodle.QuizFinished:
			return SubmissionSubmitted
		case moodle.QuizInProgress, moodle.QuizOverdue:
			st = SubmissionInProgress
		}
	}
	return st
}

// SubmissionState maps mod_assign_get_submission_status to a Submission and
// returns the user's extension due date, if any. Team submissions take
// precedence when present.
func SubmissionState(st *moodle.SubmissionStatus) (Submission, time.Time) {
	if st == nil || st.LastAttempt == nil {
		return SubmissionUnknown, time.Time{}
	}
	la := st.LastAttempt
	ext := la.ExtensionDueDate.Time()
	sub := la.Submission
	if la.TeamSubmission != nil {
		sub = la.TeamSubmission
	}
	if sub == nil {
		return SubmissionNotSubmitted, ext
	}
	switch sub.Status {
	case moodle.SubmissionSubmitted:
		if bool(la.Graded) || la.GradingStatus == "graded" {
			return SubmissionGraded, ext
		}
		return SubmissionSubmitted, ext
	case moodle.SubmissionDraft:
		return SubmissionDraft, ext
	case moodle.SubmissionReopened:
		return SubmissionReopened, ext
	case moodle.SubmissionNew, "":
		return SubmissionNotSubmitted, ext
	default:
		return SubmissionUnknown, ext
	}
}
