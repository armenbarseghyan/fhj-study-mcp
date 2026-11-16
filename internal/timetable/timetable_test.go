package timetable

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/armenbarseghyan/fhj-study-mcp/internal/textfmt"
)

func vie(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", s, textfmt.Vienna)
	if err != nil {
		panic(err)
	}
	return t
}

func TestParse(t *testing.T) {
	t.Parallel()
	tests := []struct {
		title, class                      string
		course, kind, people, group, room string
	}{
		{
			title:  "Databases and Query Languages (-126350) (Integrierte Lehrveranstaltung (IL)), Probe Paul, Standardgruppe, MS-Teams (DAT 2026)",
			class:  "timetable_group_Standardgruppe",
			course: "Databases and Query Languages", kind: "IL", people: "Probe Paul", group: "Standardgruppe", room: "MS-Teams",
		},
		{
			title:  "Programming and Data Processing (-126367) (Übung (UE)), Beispiel Erika, Gruppe 2, ., ES30i ES30i.EG.002 (DAT 2026)",
			class:  "timetable_group_Gruppe 2",
			course: "Programming and Data Processing", kind: "UE", people: "Beispiel Erika", group: "Gruppe 2", room: "ES30i.EG.002",
		},
		{
			title:  "Sondertermin DAT (1.Semester) (-121650) (Sonderlehrveranstaltung (SO)), Muster Max / Probe Paul, Standardgruppe, ., ES30i ES30i.EG.004 (DAT 2026)",
			class:  "timetable_group_Standardgruppe",
			course: "Sondertermin DAT (1.Semester)", kind: "SO", people: "Muster Max / Probe Paul", group: "Standardgruppe", room: "ES30i.EG.004",
		},
		{
			// Exams have no course number: the head is kept as the course.
			title:  "Prüfungstermin: M25.0807108 26W 1,5SSt IL Refresher on Unix Shells and LaTeX  bei Muster, -, ., ES30i ES30i.EG.053 (DAT 2026)",
			class:  "timetable_group_",
			course: "Prüfungstermin: M25.0807108 26W 1,5SSt IL Refresher on Unix Shells and LaTeX bei Muster", room: "ES30i.EG.053",
		},
		{
			title:  "Data, Ethics and Society (-126399) (Vorlesung (VO)), Probe Paul, Standardgruppe, Online (DAT 2026)",
			class:  "timetable_group_Standardgruppe",
			course: "Data, Ethics and Society", kind: "VO", people: "Probe Paul", group: "Standardgruppe", room: "Online",
		},
		{title: "Just a title", class: "", course: "Just a title"},
	}
	for _, tt := range tests {
		ev, err := parse(rawEvent{ID: "x", Title: tt.title, ClassName: tt.class, Start: "2026-10-05T17:15:00", End: "2026-10-05T19:45:00"})
		if err != nil {
			t.Fatal(err)
		}
		if ev.Course != tt.course || ev.Kind != tt.kind || ev.People != tt.people || ev.Group != tt.group || ev.Room != tt.room {
			t.Errorf("%q:\n got course=%q kind=%q people=%q group=%q room=%q\nwant course=%q kind=%q people=%q group=%q room=%q",
				tt.title, ev.Course, ev.Kind, ev.People, ev.Group, ev.Room, tt.course, tt.kind, tt.people, tt.group, tt.room)
		}
		if !ev.Start.Equal(vie("2026-10-05 17:15")) || !ev.End.Equal(vie("2026-10-05 19:45")) {
			t.Errorf("times are Vienna wall clock: %v – %v", ev.Start, ev.End)
		}
	}
}

const twoEvents = `[
 {"id":"a","title":"B (-1) (Übung (UE)), X, Gruppe 1, R1 (DAT 2026)","start":"2026-10-06T10:00:00","end":"2026-10-06T11:00:00","className":"timetable_group_Gruppe 1"},
 {"id":"b","title":"A (-2) (Vorlesung (VO)), Y, Standardgruppe, R2 (DAT 2026)","start":"2026-10-05T10:00:00","end":"2026-10-05T11:00:00","className":"timetable_group_Standardgruppe"},
 {"id":"c","title":"broken","start":"yesterday","end":"","className":""},
 {"id":"d","title":"C (-3) (Vorlesung (VO)), Z, Standardgruppe, R3 (DAT 2026)","start":"2026-10-20T10:00:00","end":"2026-10-20T11:00:00","className":"timetable_group_Standardgruppe"}
]`

func TestEvents(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	var gotQuery, gotStart, gotEnd string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		gotQuery, gotStart, gotEnd = r.URL.Query().Get("q"), r.URL.Query().Get("start"), r.URL.Query().Get("end")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(twoEvents))
	}))
	defer ts.Close()
	c := New(Options{URL: ts.URL})
	from, to := vie("2026-10-05 00:00"), vie("2026-10-12 00:00")

	evs, _, err := c.Events(context.Background(), " dat26 ", from, to, false)
	if err != nil {
		t.Fatal(err)
	}
	if gotQuery != "dat26" || gotStart != "2026-10-05" || gotEnd != "2026-10-11" {
		t.Errorf("request q=%q start=%q end=%q (end is inclusive)", gotQuery, gotStart, gotEnd)
	}
	// sorted, the malformed entry skipped, the one outside the window dropped
	if len(evs) != 2 || evs[0].ID != "b" || evs[1].ID != "a" {
		t.Fatalf("events = %+v", evs)
	}
	if _, _, err := c.Events(context.Background(), "dat26", from, to, false); err != nil || calls.Load() != 1 {
		t.Errorf("second call must come from the cache: calls=%d err=%v", calls.Load(), err)
	}
	if _, _, err := c.Events(context.Background(), "dat26", from, to, true); err != nil || calls.Load() != 2 {
		t.Errorf("refresh must refetch: calls=%d err=%v", calls.Load(), err)
	}
	if _, _, err := c.Events(context.Background(), "  ", from, to, false); !errors.Is(err, ErrNoQuery) {
		t.Errorf("empty query: %v", err)
	}
}

// The timetable's days are Vienna days: a user east of Vienna still gets the
// classes of the late Vienna evening that falls into their today.
func TestEvents_ViennaDays(t *testing.T) {
	almaty, err := time.LoadLocation("Asia/Almaty")
	if err != nil {
		t.Fatal(err)
	}
	var gotStart, gotEnd string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotStart, gotEnd = r.URL.Query().Get("start"), r.URL.Query().Get("end")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
	}))
	defer ts.Close()
	from := time.Date(2026, 11, 17, 0, 0, 0, 0, almaty) // 16.11 20:00 in Vienna
	to := time.Date(2026, 11, 18, 0, 0, 0, 0, almaty)   // 17.11 20:00 in Vienna
	if _, _, err := New(Options{URL: ts.URL}).Events(context.Background(), "dat26", from, to, false); err != nil {
		t.Fatal(err)
	}
	if gotStart != "2026-11-16" || gotEnd != "2026-11-17" {
		t.Errorf("start=%s end=%s, want the Vienna days 2026-11-16..2026-11-17", gotStart, gotEnd)
	}
}

// An empty answer is not cached: an outage can look the same.
func TestEvents_EmptyNotCached(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()
	c := New(Options{URL: ts.URL})
	for range 2 {
		if evs, _, err := c.Events(context.Background(), "dat26", vie("2026-10-05 00:00"), vie("2026-10-12 00:00"), false); err != nil || len(evs) != 0 {
			t.Fatalf("evs=%v err=%v", evs, err)
		}
	}
	if calls.Load() != 2 {
		t.Errorf("empty answer was cached: %d calls", calls.Load())
	}
}

func TestEvents_Responses(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		status int
		ctype  string
		body   string
		empty  bool // no events, no error
	}{
		{"search without results", http.StatusInternalServerError, "application/json", "", true},
		{"server error page", http.StatusInternalServerError, "text/html", "<h1>Error</h1>", false},
		{"500 with a JSON body", http.StatusInternalServerError, "application/json", `{"error":1}`, false},
		{"unavailable", http.StatusServiceUnavailable, "text/html", "", false},
		{"not JSON", http.StatusOK, "text/html", "<html>maintenance</html>", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tt.ctype)
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer ts.Close()
			evs, _, err := New(Options{URL: ts.URL}).Events(context.Background(), "dat26", vie("2026-10-05 00:00"), vie("2026-10-12 00:00"), false)
			if tt.empty {
				if err != nil || len(evs) != 0 {
					t.Errorf("want no events and no error, got %v %v", evs, err)
				}
				return
			}
			if !errors.Is(err, ErrUnavailable) {
				t.Errorf("want ErrUnavailable, got %v", err)
			}
		})
	}
}
