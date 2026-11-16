// Package timetable reads the FH JOANNEUM class timetable ("Almaty", the
// timetable search for all FH locations, fed from Moses) through its public
// JSON interface. It needs no login and never sees the Moodle token.
package timetable

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/armenbarseghyan/fhj-study-mcp/internal/cache"
	"github.com/armenbarseghyan/fhj-study-mcp/internal/textfmt"
)

// URL is the timetable's documented JSON interface.
const URL = "https://almaty.fh-joanneum.at/stundenplan/json.php"

// TTL matches the X-PUBLISHED-TTL the timetable announces for its iCal feed.
const TTL = time.Hour

const (
	maxBody   = 8 << 20
	userAgent = "fhj-study-mcp (+https://github.com/armenbarseghyan/fhj-study-mcp)"
)

// Sentinel errors.
var (
	ErrNoQuery     = errors.New("timetable: no search set")
	ErrUnavailable = errors.New("timetable: not available")

	// errNoResults keeps an empty answer out of the cache.
	errNoResults = errors.New("timetable: no results")
)

// Event is one class, exam or other appointment.
type Event struct {
	ID     string
	Title  string // as the timetable shows it
	Course string // "Databases and Query Languages"; the title's first part when it has no course number
	Kind   string // "IL", "UE", "VO", ... when the title carries one
	People string // lecturers and notes between course and group, "" if none
	Group  string // "Standardgruppe", "Gruppe 1", ... or ""
	Room   string // "ES30i.EG.004", "MS-Teams", ...
	Start  time.Time
	End    time.Time
}

// Everyone reports whether the event is for the whole programme rather than
// one group.
func (e Event) Everyone() bool { return e.Group == "" || e.Group == "Standardgruppe" }

// Options configures a Client.
type Options struct {
	URL  string // default URL; tests point it at a fake server
	HTTP *http.Client
	Now  func() time.Time
}

// Client fetches and caches timetable searches.
type Client struct {
	url   string
	hc    *http.Client
	cache *cache.TTL[[]Event]
}

// New returns a Client.
func New(o Options) *Client {
	if o.URL == "" {
		o.URL = URL
	}
	if o.HTTP == nil {
		o.HTTP = &http.Client{Timeout: 20 * time.Second}
	}
	return &Client{url: o.URL, hc: o.HTTP, cache: cache.New[[]Event](TTL, o.Now)}
}

// Events returns the events matching query between from and to (exclusive),
// sorted by start. The timetable is asked for whole days.
func (c *Client) Events(ctx context.Context, query string, from, to time.Time, refresh bool) ([]Event, time.Time, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, time.Time{}, ErrNoQuery
	}
	// The timetable's days are Vienna days, whatever zone the dates are shown
	// in; "end" is inclusive.
	day := func(t time.Time) string { return t.In(textfmt.Vienna).Format("2006-01-02") }
	start, end := day(from), day(to.Add(-time.Nanosecond))
	e, err := c.cache.Get(ctx, query+"|"+start+"|"+end, refresh, func(ctx context.Context) ([]Event, error) {
		return c.fetch(ctx, query, start, end)
	})
	if errors.Is(err, errNoResults) {
		// Not cached: an outage can look the same (see fetch).
		return nil, time.Time{}, nil
	}
	if err != nil {
		return nil, time.Time{}, err
	}
	out := slices.DeleteFunc(slices.Clone(e.Value), func(ev Event) bool {
		return !ev.End.After(from) || !ev.Start.Before(to)
	})
	return out, e.FetchedAt, nil
}

type rawEvent struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Start     string `json:"start"`
	End       string `json:"end"`
	ClassName string `json:"className"`
}

func (c *Client) fetch(ctx context.Context, query, start, end string) ([]Event, error) {
	u := c.url + "?" + url.Values{"q": {query}, "start": {start}, "end": {end}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	if len(body) > maxBody {
		return nil, fmt.Errorf("%w: answer larger than %d bytes", ErrUnavailable, maxBody)
	}
	ct, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	// A search without results is answered with HTTP 500, an empty body and
	// a JSON content type; anything else that is not 200 is a failure.
	if resp.StatusCode == http.StatusInternalServerError && len(body) == 0 && ct == "application/json" {
		return nil, errNoResults
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: HTTP %d", ErrUnavailable, resp.StatusCode)
	}
	var raw []rawEvent
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("%w: not JSON: %w", ErrUnavailable, err)
	}
	out := make([]Event, 0, len(raw))
	for _, r := range raw {
		ev, err := parse(r)
		if err != nil {
			continue // one malformed entry must not hide the rest
		}
		out = append(out, ev)
	}
	slices.SortStableFunc(out, func(a, b Event) int { return a.Start.Compare(b.Start) })
	return out, nil
}

var (
	// "Databases and Query Languages (-126350) (Integrierte Lehrveranstaltung (IL))"
	courseHead = regexp.MustCompile(`^(.+?) \(-\d+\) \((.*)\)$`)
	// the abbreviation at the end of the type: "… (IL)"
	kindAbbrev = regexp.MustCompile(`\(([^()]+)\)$`)
	// the programme suffix of the last part: "ES30i ES30i.EG.004 (DAT 2026)"
	programme = regexp.MustCompile(` \([^()]*\)$`)
)

// parse splits the title by its syntax: "<course> (-<number>) (<type>),
// <people…>, <group>, [<note>,] <room> (<programme>)". The group comes from
// className, not from the title.
func parse(r rawEvent) (Event, error) {
	start, err := time.ParseInLocation("2006-01-02T15:04:05", r.Start, textfmt.Vienna)
	if err != nil {
		return Event{}, err
	}
	end, err := time.ParseInLocation("2006-01-02T15:04:05", r.End, textfmt.Vienna)
	if err != nil {
		return Event{}, err
	}
	ev := Event{ID: r.ID, Title: strings.TrimSpace(r.Title), Start: start, End: end,
		Group: strings.TrimSpace(strings.TrimPrefix(r.ClassName, "timetable_group_"))}

	parts := strings.Split(ev.Title, ", ")
	if len(parts) < 2 {
		ev.Course = ev.Title
		return ev, nil
	}
	ev.Room = room(parts[len(parts)-1])
	parts = parts[:len(parts)-1]

	// The course name may itself contain ", ": take the shortest head that
	// has the "(-number) (type)" shape.
	head := 1
	for k := 1; k <= len(parts); k++ {
		if courseHead.MatchString(strings.Join(parts[:k], ", ")) {
			head = k
			break
		}
	}
	first := strings.Join(parts[:head], ", ")
	if m := courseHead.FindStringSubmatch(first); m != nil {
		ev.Course = strings.TrimSpace(m[1])
		if k := kindAbbrev.FindStringSubmatch(m[2]); k != nil {
			ev.Kind = k[1]
		} else {
			ev.Kind = m[2]
		}
	} else {
		ev.Course = strings.Join(strings.Fields(first), " ")
	}

	var people []string
	for _, p := range parts[head:] {
		p = strings.TrimSpace(p)
		if p == "" || p == "." || p == "-" || p == ev.Group {
			continue
		}
		people = append(people, p)
	}
	ev.People = strings.Join(people, ", ")
	return ev, nil
}

// room drops the programme suffix and the building code repeated in front
// of the room: "ES30i ES30i.EG.004 (DAT 2026)" → "ES30i.EG.004".
func room(s string) string {
	s = programme.ReplaceAllString(strings.TrimSpace(s), "")
	if f := strings.Fields(s); len(f) == 2 && strings.HasPrefix(f[1], f[0]+".") {
		return f[1]
	}
	return s
}
