package tools_test

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/armenbarseghyan/fhj-study-mcp/internal/cache"
	"github.com/armenbarseghyan/fhj-study-mcp/internal/moodle"
	"github.com/armenbarseghyan/fhj-study-mcp/internal/moodletest"
	"github.com/armenbarseghyan/fhj-study-mcp/internal/study"
	"github.com/armenbarseghyan/fhj-study-mcp/internal/textfmt"
	"github.com/armenbarseghyan/fhj-study-mcp/internal/timetable"
	"github.com/armenbarseghyan/fhj-study-mcp/internal/tools"
)

var update = flag.Bool("update", false, "rewrite golden files")

// now is the fixed clock of every scenario: Friday 2026-10-02 12:00 Vienna.
var now = time.Date(2026, 10, 2, 12, 0, 0, 0, textfmt.Vienna)

// fakeMoodle serves the shared fixture world (see moodletest.World).
func fakeMoodle(t *testing.T, extra ...moodletest.Option) *moodletest.Server {
	t.Helper()
	return moodletest.New(t, moodletest.World(t, extra...)...)
}

type env struct {
	srv     *moodletest.Server
	session *mcp.ClientSession
	dlDir   string
}

func setup(t *testing.T, token string, extra ...moodletest.Option) *env {
	t.Helper()
	srv := fakeMoodle(t, extra...)
	client, err := moodle.New(moodle.Config{BaseURL: srv.URL, Token: token, Backoff: func(int) time.Duration { return 0 }})
	if err != nil {
		t.Fatal(err)
	}
	dl := t.TempDir()
	clock := func() time.Time { return now }
	svc := study.New(cache.NewSource(client, clock), study.Options{Now: clock, DownloadDir: dl})
	tt := timetable.New(timetable.Options{URL: fakeTimetable(t).URL, Now: clock})
	return connect(t, tools.Deps{Service: svc, Now: clock, Version: "test", Timetable: tt, TimetableQuery: "dat26"}, srv, dl)
}

// fakeTimetable serves the synthetic DAT26 week for q=dat26 and the
// timetable's answer to a search without results (HTTP 500, empty JSON body)
// for anything else.
func fakeTimetable(t *testing.T) *httptest.Server {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(moodletest.TestdataDir(), "timetable", "dat26.synthetic.json"))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if !strings.HasPrefix(r.URL.Query().Get("q"), "dat26") {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(ts.Close)
	return ts
}

func connect(t *testing.T, deps tools.Deps, srv *moodletest.Server, dl string) *env {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "fhj-study-mcp", Version: "test"}, nil)
	tools.Register(server, deps)
	st, ct := mcp.NewInMemoryTransports()
	ctx := context.Background()
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return &env{srv: srv, session: cs, dlDir: dl}
}

func (e *env) call(t *testing.T, name string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := e.session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var b strings.Builder
	for _, c := range res.Content {
		b.WriteString(c.(*mcp.TextContent).Text)
	}
	out := b.String()
	if strings.Contains(out, moodletest.Token) {
		t.Fatalf("%s output leaks the token:\n%s", name, out)
	}
	return out, res.IsError
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	got = strings.ReplaceAll(got, goldenHost(t), "https://moodle.test")
	path := filepath.Join(moodletest.TestdataDir(), "golden", name+".md")
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing golden file (run: make golden): %v", err)
	}
	if got != string(want) {
		t.Errorf("%s differs from golden file (run: make golden to accept)\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

// goldenHost is replaced by a stable name, because httptest ports change.
var currentHost string

func goldenHost(*testing.T) string { return currentHost }

func TestToolsGolden(t *testing.T) {
	tests := []struct {
		name string
		tool string
		args map[string]any
	}{
		{"courses", "moodle_courses", nil},
		{"deadlines", "moodle_deadlines", nil},
		{"deadlines.no-overdue.30d", "moodle_deadlines", map[string]any{"days": 30, "include_overdue": false}},
		{"deadlines.3d", "moodle_deadlines", map[string]any{"days": 3}},
		{"contents.12308", "moodle_course_contents", map[string]any{"course": "12308"}},
		{"contents.unix-subsections", "moodle_course_contents", map[string]any{"course": "unix"}},
		{"contents.ambiguous", "moodle_course_contents", map[string]any{"course": "(DAT"}},
		{"contents.notfound", "moodle_course_contents", map[string]any{"course": "chemistry"}},
		{"search.gitlab", "moodle_search", map[string]any{"query": "gitlab"}},
		{"search.in-subsection", "moodle_search", map[string]any{"query": "screenrecording"}},
		{"search.nothing", "moodle_search", map[string]any{"query": "vpn"}},
		{"search.in-files.vpn", "moodle_search", map[string]any{"query": "vpn", "in_files": true}},
		{"search.in-files.course", "moodle_search", map[string]any{"query": "ssh-keygen", "in_files": true, "course": "programming"}},
		{"search.in-files.nothing", "moodle_search", map[string]any{"query": "kubernetes", "in_files": true, "course": "12308"}},
		{"grades.all", "moodle_grades", nil},
		{"grades.one", "moodle_grades", map[string]any{"course": "programming"}},
		{"announcements", "moodle_announcements", nil},
		{"announcements.1d", "moodle_announcements", map[string]any{"days": 1}},
		{"whats_new", "moodle_whats_new", nil},
		{"whats_new.5d", "moodle_whats_new", map[string]any{"days": 5}},
		{"whats_new.1d", "moodle_whats_new", map[string]any{"days": 1}},
		{"whoami", "moodle_whoami", nil},
		{"timetable", "fhj_timetable", nil},
		{"timetable.groups", "fhj_timetable", map[string]any{"groups": []string{"Gruppe 1"}, "days": 14}},
		{"timetable.empty", "fhj_timetable", map[string]any{"query": "chemistry"}},
	}
	e := setup(t, moodletest.Token)
	currentHost = e.srv.URL
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, isErr := e.call(t, tt.tool, tt.args)
			if isErr {
				t.Fatalf("unexpected error result:\n%s", out)
			}
			golden(t, tt.name, out)
		})
	}
}

// Semantic checks of the main tool, independent of formatting.
func TestDeadlines_Semantics(t *testing.T) {
	e := setup(t, moodletest.Token)
	out, _ := e.call(t, "moodle_deadlines", nil)
	mustContain := []string{
		"Homework R0", "❌ not submitted", // overdue, unsubmitted, within 7 days
		"Quiz LaTeX Basics",          // calendar only
		"Homework R1", "✅ submitted", // merged from both sources
		"Homework R2", // assignment only
		"2026-10-07 17:15 CEST (Wednesday) — in 5 days",
		"Hidden or not yet available: 1 assignment",
	}
	for _, s := range mustContain {
		if !strings.Contains(out, s) {
			t.Errorf("missing %q in:\n%s", s, out)
		}
	}
	for _, s := range []string{"Old Exercise", "Optional Exercises", "Home Assignment Unix Shells", "ist fällig"} {
		if strings.Contains(out, s) {
			t.Errorf("must not contain %q:\n%s", s, out)
		}
	}
	if n := strings.Count(out, "Homework R1"); n != 1 {
		t.Errorf("Homework R1 appears %d times, want 1 (dedup)", n)
	}

	// The calendar's "instance" is a cmid (190501); status calls must use the
	// activity id (501), and the calendar-only quiz must be resolved to 77.
	var assignIDs, quizIDs []string
	for _, r := range e.srv.Requests() {
		switch r.Form.Get("wsfunction") {
		case "mod_assign_get_submission_status":
			assignIDs = append(assignIDs, r.Form.Get("assignid"))
		case "mod_quiz_get_user_attempts":
			quizIDs = append(quizIDs, r.Form.Get("quizid"))
		}
	}
	for _, id := range assignIDs {
		if strings.HasPrefix(id, "190") {
			t.Errorf("submission status asked with cmid %s as assignid (all: %v)", id, assignIDs)
		}
	}
	if !slices.Equal(quizIDs, []string{"77"}) {
		t.Errorf("quiz attempts asked for %v, want [77]", quizIDs)
	}
}

func TestCaching(t *testing.T) {
	e := setup(t, moodletest.Token)
	for range 3 {
		e.call(t, "moodle_course_contents", map[string]any{"course": "12308"})
	}
	if n := e.srv.Calls("core_course_get_contents"); n != 1 {
		t.Errorf("contents fetched %d times, want 1 (cached)", n)
	}
	if n := e.srv.Calls("core_enrol_get_users_courses"); n != 1 {
		t.Errorf("courses fetched %d times, want 1", n)
	}
	if n := e.srv.Calls("core_webservice_get_site_info"); n != 1 {
		t.Errorf("site info fetched %d times, want 1", n)
	}

	e.call(t, "moodle_course_contents", map[string]any{"course": "12308", "refresh": true})
	if n := e.srv.Calls("core_course_get_contents"); n != 2 {
		t.Errorf("refresh must refetch: %d calls", n)
	}

	e.call(t, "moodle_grades", map[string]any{"course": "12308"})
	e.call(t, "moodle_grades", map[string]any{"course": "12308"})
	if n := e.srv.Calls("gradereport_user_get_grade_items"); n != 2 {
		t.Errorf("grades must not be cached: %d calls", n)
	}

	// Search reuses the contents cache filled above.
	before := e.srv.Calls("core_course_get_contents")
	e.call(t, "moodle_search", map[string]any{"query": "gitlab"})
	if got := e.srv.Calls("core_course_get_contents") - before; got != 2 {
		t.Errorf("search fetched contents %d times, want 2 (12308 cached; 12326 and 12307 new)", got)
	}
}

func TestInvalidToken(t *testing.T) {
	e := setup(t, "revoked-token")
	for _, tool := range []string{"moodle_courses", "moodle_deadlines", "moodle_search", "moodle_grades",
		"moodle_announcements", "moodle_whats_new", "moodle_whoami"} {
		args := map[string]any{}
		if tool == "moodle_search" {
			args["query"] = "git"
		}
		out, isErr := e.call(t, tool, args)
		if !isErr || !strings.Contains(out, "The Moodle token is invalid") {
			t.Errorf("%s: isErr=%v out=%q", tool, isErr, out)
		}
	}
}

func TestInitError(t *testing.T) {
	e := connect(t, tools.Deps{InitErr: errors.New("MOODLE_TOKEN is not set")}, nil, "")
	out, isErr := e.call(t, "moodle_deadlines", nil)
	if !isErr || !strings.Contains(out, "MOODLE_TOKEN") {
		t.Fatalf("isErr=%v out=%q", isErr, out)
	}
}

func TestPartialFailure(t *testing.T) {
	// One course's contents fail: search still answers from the others.
	e := setup(t, moodletest.Token, moodletest.Route("core_course_get_contents", func(p url.Values) moodletest.Resp {
		if p.Get("courseid") == "12326" {
			return moodletest.ErrorFile(t, "requireloginerror")
		}
		return moodletest.File(t, "core_course_get_contents", "real-12308")
	}))
	out, isErr := e.call(t, "moodle_search", map[string]any{"query": "gitlab"})
	if isErr || !strings.Contains(out, "GitLab IT+") || !strings.Contains(out, "⚠ Refresher on Unix Shells and LaTeX: The course or activity is not accessible") {
		t.Fatalf("isErr=%v\n%s", isErr, out)
	}
}

func TestDownload(t *testing.T) {
	files := moodletest.Files(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Disposition", `attachment; filename="IT+ Labs — Working on a DAT VM.pdf"`)
		_, _ = w.Write([]byte("%PDF-1.7"))
	}))
	e := setup(t, moodletest.Token, files)
	browserURL := e.srv.URL + "/pluginfile.php/327811/mod_resource/content/1/IT%2B%20Labs.pdf"

	out, isErr := e.call(t, "moodle_download", map[string]any{"fileurl": browserURL})
	want := filepath.Join(e.dlDir, "IT+ Labs — Working on a DAT VM.pdf")
	if isErr || !strings.Contains(out, want) || !strings.Contains(out, "In the browser: "+browserURL) {
		t.Fatalf("isErr=%v\n%s", isErr, out)
	}
	if b, err := os.ReadFile(want); err != nil || string(b) != "%PDF-1.7" {
		t.Fatalf("file: %q %v", b, err)
	}

	// A hidden folder in the home directory is refused before anything is fetched.
	before := e.srv.Calls("pluginfile")
	out, isErr = e.call(t, "moodle_download", map[string]any{"fileurl": browserURL, "dest": "~/.ssh/"})
	if !isErr || !strings.Contains(out, "saved only into the download folder") || e.srv.Calls("pluginfile") != before {
		t.Fatalf("hidden dest: isErr=%v %s", isErr, out)
	}

	out, isErr = e.call(t, "moodle_download", map[string]any{"fileurl": "https://evil.example/pluginfile.php/1/a.pdf"})
	if !isErr || !strings.Contains(out, "does not point to a file of this Moodle") {
		t.Fatalf("foreign URL: isErr=%v %s", isErr, out)
	}
}

// Without MOODLE_DOWNLOAD_DIR the file lands in ~/Downloads/moodle/, a folder
// created on first use.
func TestDownload_DefaultDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	srv := fakeMoodle(t, moodletest.Files(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("%PDF-1.7"))
	})))
	client, err := moodle.New(moodle.Config{BaseURL: srv.URL, Token: moodletest.Token, Backoff: func(int) time.Duration { return 0 }})
	if err != nil {
		t.Fatal(err)
	}
	clock := func() time.Time { return now }
	svc := study.New(cache.NewSource(client, clock), study.Options{Now: clock})
	e := connect(t, tools.Deps{Service: svc, Now: clock, Version: "test"}, srv, "")

	out, isErr := e.call(t, "moodle_download", map[string]any{"fileurl": srv.URL + "/pluginfile.php/1/a.pdf"})
	want := filepath.Join(home, "Downloads", "moodle", "a.pdf")
	if isErr || !strings.Contains(out, want) {
		t.Fatalf("isErr=%v, want %s\n%s", isErr, want, out)
	}
	if b, err := os.ReadFile(want); err != nil || string(b) != "%PDF-1.7" {
		t.Fatalf("file: %q %v", b, err)
	}
}

func TestListTools(t *testing.T) {
	e := setup(t, moodletest.Token)
	res, err := e.session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]*mcp.Tool{}
	for _, tl := range res.Tools {
		byName[tl.Name] = tl
	}
	want := []string{"moodle_courses", "moodle_deadlines", "moodle_course_contents", "moodle_search",
		"moodle_grades", "moodle_announcements", "moodle_whats_new", "moodle_download", "moodle_whoami", "fhj_timetable"}
	if len(byName) != len(want) {
		t.Errorf("tools = %d, want %d", len(byName), len(want))
	}
	for _, n := range want {
		tl, ok := byName[n]
		if !ok {
			t.Errorf("missing tool %s", n)
			continue
		}
		if tl.Description == "" || tl.Annotations == nil {
			t.Errorf("%s: description/annotations missing", n)
		}
		if n != "moodle_download" && !tl.Annotations.ReadOnlyHint {
			t.Errorf("%s must be read-only", n)
		}
		if tl.OutputSchema != nil {
			t.Errorf("%s: unexpected output schema (output is markdown)", n)
		}
	}

	schema, _ := json.Marshal(byName["moodle_deadlines"].InputSchema)
	for _, s := range []string{`"default":14`, `"minimum":1`, `"maximum":90`, `"include_overdue"`} {
		if !strings.Contains(string(schema), s) {
			t.Errorf("deadlines schema lacks %s: %s", s, schema)
		}
	}
	contents, _ := json.Marshal(byName["moodle_course_contents"].InputSchema)
	if !strings.Contains(string(contents), `"required":["course"]`) {
		t.Errorf("course must be required: %s", contents)
	}
}

func TestInputValidation(t *testing.T) {
	e := setup(t, moodletest.Token)
	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"moodle_deadlines", map[string]any{"days": 0}},
		{"moodle_deadlines", map[string]any{"days": 365}},
		{"moodle_course_contents", map[string]any{}},
		{"moodle_search", map[string]any{"query": "x"}},
	} {
		if out, isErr := e.call(t, tc.tool, tc.args); !isErr {
			t.Errorf("%s %v must be rejected, got:\n%s", tc.tool, tc.args, out)
		}
	}
}

func TestSearchInFiles_TextIsCached(t *testing.T) {
	e := setup(t, moodletest.Token)
	out, isErr := e.call(t, "moodle_search", map[string]any{"query": "different setup", "in_files": true, "course": "12308"})
	if isErr || !strings.Contains(out, "p. 2") || !strings.Contains(out, "Different setup for macOS") {
		t.Fatalf("isErr=%v\n%s", isErr, out)
	}
	first := e.srv.Calls("pluginfile")
	if first == 0 {
		t.Fatal("files must be fetched on the first search")
	}
	e.call(t, "moodle_search", map[string]any{"query": "vpn", "in_files": true, "course": "12308"})
	if n := e.srv.Calls("pluginfile"); n != first {
		t.Errorf("unchanged files fetched again: %d → %d", first, n)
	}
}

// The timetable needs no Moodle: it works without a token, and it explains
// what to set when no search is configured.
func TestTimetable_WithoutMoodle(t *testing.T) {
	clock := func() time.Time { return now }
	tt := timetable.New(timetable.Options{URL: fakeTimetable(t).URL, Now: clock})
	e := connect(t, tools.Deps{InitErr: errors.New("MOODLE_TOKEN is not set"), Now: clock, Timetable: tt}, nil, "")
	out, isErr := e.call(t, "fhj_timetable", map[string]any{"query": "dat26"})
	if isErr || !strings.Contains(out, "Programming and Data Processing (UE)") {
		t.Fatalf("isErr=%v\n%s", isErr, out)
	}
	// Groups are matched as typed in the setup, case-insensitively.
	out, isErr = e.call(t, "fhj_timetable", map[string]any{"query": "dat26", "groups": []string{" gruppe 1"}})
	if isErr || !strings.Contains(out, "· Gruppe 1 ·") || strings.Contains(out, "· Gruppe 2 ·") {
		t.Fatalf("lower-case group: isErr=%v\n%s", isErr, out)
	}
	out, isErr = e.call(t, "fhj_timetable", nil)
	if !isErr || !strings.Contains(out, "FHJ_TIMETABLE") {
		t.Fatalf("no query: isErr=%v %s", isErr, out)
	}
	if out, _ := e.call(t, "moodle_deadlines", nil); !strings.Contains(out, "MOODLE_TOKEN") {
		t.Fatalf("Moodle tools still report the init error: %s", out)
	}
}

// When the course contents cannot be read, a calendar-only quiz has no known
// activity id: its status stays unknown and its cmid is never sent as quizid.
func TestDeadlines_QuizWithoutContents(t *testing.T) {
	e := setup(t, moodletest.Token, moodletest.Route("core_course_get_contents", func(url.Values) moodletest.Resp {
		return moodletest.ErrorFile(t, "requireloginerror")
	}))
	out, isErr := e.call(t, "moodle_deadlines", nil)
	if isErr || !strings.Contains(out, "Quiz LaTeX Basics") || strings.Contains(out, "not attempted yet") {
		t.Fatalf("isErr=%v\n%s", isErr, out)
	}
	if n := e.srv.Calls("mod_quiz_get_user_attempts"); n != 0 {
		t.Errorf("quiz attempts asked %d times without a known quiz id", n)
	}
}

// Configured groups apply to the configured search only; after the last class
// of the period the answer says so instead of hinting at a failure.
func TestTimetable_GroupsAndLateEvening(t *testing.T) {
	late := time.Date(2026, 10, 2, 21, 0, 0, 0, textfmt.Vienna)
	clock := func() time.Time { return late }
	tt := timetable.New(timetable.Options{URL: fakeTimetable(t).URL, Now: clock})
	e := connect(t, tools.Deps{Now: clock, Timetable: tt, TimetableQuery: "dat26", TimetableGroups: []string{"Gruppe 2"}}, nil, "")

	out, _ := e.call(t, "fhj_timetable", map[string]any{"days": 7})
	if strings.Contains(out, "Gruppe 3") || !strings.Contains(out, "Only Gruppe 2") {
		t.Errorf("configured search must use the configured groups:\n%s", out)
	}
	out, _ = e.call(t, "fhj_timetable", map[string]any{"days": 7, "query": "dat26 speaker:Erika"})
	if !strings.Contains(out, "Gruppe 3") || strings.Contains(out, "Only Gruppe 2") {
		t.Errorf("another search must not inherit the configured groups:\n%s", out)
	}
	out, _ = e.call(t, "fhj_timetable", map[string]any{"days": 1})
	if !strings.Contains(out, "No more classes") || strings.Contains(out, "failures") {
		t.Errorf("after today's last class:\n%s", out)
	}
}
