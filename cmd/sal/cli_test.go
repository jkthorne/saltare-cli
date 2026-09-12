package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zalando/go-keyring"

	"github.com/jkthorne/saltare-cli/internal/config"
)

// The scriptable half of sal is what other programs parse, so these drive the
// real run* entry points against a stub server and assert both the request
// that went out and the bytes that came back out of stdout.

type recordedRequest struct {
	Method string
	Path   string
	Query  url.Values
	Body   string
}

type stubServer struct {
	*httptest.Server

	mu       sync.Mutex
	requests []recordedRequest
}

// newStubServer serves canned JSON per "METHOD /path" route. An unrouted
// request fails the test rather than returning a confusing 404 body.
func newStubServer(t *testing.T, routes map[string]any) *stubServer {
	t.Helper()
	s := &stubServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.requests = append(s.requests, recordedRequest{
			Method: r.Method, Path: r.URL.Path, Query: r.URL.Query(), Body: string(body),
		})
		s.mu.Unlock()

		if auth := r.Header.Get("Authorization"); auth != "Bearer sk_sal_test" {
			t.Errorf("%s %s carried Authorization %q", r.Method, r.URL.Path, auth)
		}

		key := r.Method + " " + r.URL.Path
		payload, ok := routes[key]
		if !ok {
			t.Errorf("unexpected request: %s (routes: %v)", key, keysOf(routes))
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"no route"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if fn, isFunc := payload.(func(*http.Request) any); isFunc {
			payload = fn(r)
		}
		_ = json.NewEncoder(w).Encode(payload)
	}))
	t.Cleanup(s.Close)
	return s
}

func keysOf(routes map[string]any) []string {
	out := make([]string, 0, len(routes))
	for k := range routes {
		out = append(out, k)
	}
	return out
}

func (s *stubServer) took(t *testing.T, method, path string) recordedRequest {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.requests {
		if r.Method == method && r.Path == path {
			return r
		}
	}
	t.Fatalf("no %s %s request was made; got %v", method, path, s.requests)
	return recordedRequest{}
}

// signIn writes the config and tokens session() expects, pointed at the stub.
func signIn(t *testing.T, serverURL string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	keyring.MockInit()

	cfg := config.Config{
		ServerURL:     serverURL,
		WorkspaceSlug: "acme",
		WorkspaceName: "Acme",
		Email:         "jack@example.com",
		DeviceID:      "dev-test",
		UserID:        7,
		UserName:      "Jack",
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	if err := config.SaveTokens(config.Tokens{
		AccessToken:      "sk_sal_test",
		RefreshToken:     "rt_sal_test",
		AccessExpiresAt:  time.Now().Add(24 * time.Hour),
		RefreshExpiresAt: time.Now().Add(90 * 24 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
}

// capture runs fn with stdout and stderr redirected to pipes. The commands
// print through the os.Stdout package var, so swapping it is enough — and it
// also makes term.IsTerminal false, which is the piped-output path scripts get.
func capture(t *testing.T, fn func() error) (stdout, stderr string, err error) {
	t.Helper()
	outR, outW, pipeErr := os.Pipe()
	if pipeErr != nil {
		t.Fatal(pipeErr)
	}
	errR, errW, pipeErr := os.Pipe()
	if pipeErr != nil {
		t.Fatal(pipeErr)
	}

	realOut, realErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outW, errW

	// Drain concurrently: a command that outprints the pipe buffer would
	// otherwise deadlock.
	var wg sync.WaitGroup
	var outBuf, errBuf bytes.Buffer
	wg.Add(2)
	go func() { defer wg.Done(); _, _ = io.Copy(&outBuf, outR) }()
	go func() { defer wg.Done(); _, _ = io.Copy(&errBuf, errR) }()

	err = fn()

	os.Stdout, os.Stderr = realOut, realErr
	outW.Close()
	errW.Close()
	wg.Wait()
	outR.Close()
	errR.Close()
	return outBuf.String(), errBuf.String(), err
}

func TestChannelsJSONPassesTheServerPayloadThrough(t *testing.T) {
	server := newStubServer(t, map[string]any{
		"GET /api/v1/channels": map[string]any{"data": []map[string]any{
			{"id": 1, "slug": "general", "name": "General", "kind": "channel", "member": true, "unread_count": 3},
		}},
	})
	signIn(t, server.URL)

	out, _, err := capture(t, func() error { return runChannels([]string{"--json", "--kind", "channel"}) })
	if err != nil {
		t.Fatal(err)
	}

	var got []map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("--json must emit parseable JSON, got %q: %v", out, err)
	}
	if len(got) != 1 || got[0]["slug"] != "general" {
		t.Fatalf("unexpected payload: %v", got)
	}
	if kind := server.took(t, "GET", "/api/v1/channels").Query.Get("kind"); kind != "channel" {
		t.Errorf("--kind did not reach the server: %q", kind)
	}
}

func TestChannelsPlainOutputMarksMembershipAndUnread(t *testing.T) {
	server := newStubServer(t, map[string]any{
		"GET /api/v1/channels": map[string]any{"data": []map[string]any{
			{"id": 1, "slug": "general", "name": "General", "kind": "channel", "member": true, "unread_count": 3},
			{"id": 2, "slug": "random", "name": "Random", "kind": "channel", "member": false},
		}},
	})
	signIn(t, server.URL)

	out, _, err := capture(t, func() error { return runChannels(nil) })
	if err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want a line per channel, got %q", out)
	}
	if !strings.HasPrefix(lines[0], "*") || !strings.Contains(lines[0], "(3 unread)") {
		t.Errorf("joined channel line lost its marker or unread count: %q", lines[0])
	}
	if strings.HasPrefix(lines[1], "*") {
		t.Errorf("non-member channel must not be starred: %q", lines[1])
	}
	if strings.Contains(lines[1], "unread") {
		t.Errorf("zero unread must print nothing: %q", lines[1])
	}
}

func TestSendPostsTheBodyAndConfirms(t *testing.T) {
	server := newStubServer(t, map[string]any{
		"POST /api/v1/messages": map[string]any{"data": map[string]any{"id": 42, "channel_id": 1, "body": "ship it"}},
	})
	signIn(t, server.URL)

	out, _, err := capture(t, func() error { return runSend([]string{"general", "ship", "it"}) })
	if err != nil {
		t.Fatal(err)
	}

	req := server.took(t, "POST", "/api/v1/messages")
	var body struct {
		ChannelSlug string `json:"channel_slug"`
		Message     struct {
			Body string `json:"body"`
		} `json:"message"`
	}
	if err := json.Unmarshal([]byte(req.Body), &body); err != nil {
		t.Fatal(err)
	}
	// Trailing words join into one message rather than being dropped.
	if body.Message.Body != "ship it" {
		t.Errorf("body = %q, want %q", body.Message.Body, "ship it")
	}
	if body.ChannelSlug != "general" {
		t.Errorf("channel did not reach the server: %q", body.ChannelSlug)
	}
	if !strings.Contains(out, "42") {
		t.Errorf("confirmation should name the message id, got %q", out)
	}
}

// An empty message must fail before the request, not post a blank line.
//
// Note this pins the *args* path, which does not trim: only the stdin path
// runs TrimSpace, so `sal send general "   "` still posts whitespace today.
func TestSendRejectsAnEmptyBodyWithoutCallingTheServer(t *testing.T) {
	server := newStubServer(t, map[string]any{})
	signIn(t, server.URL)

	_, _, err := capture(t, func() error { return runSend([]string{"general", ""}) })
	if err == nil {
		t.Fatal("want an error for an empty message")
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if len(server.requests) != 0 {
		t.Fatalf("empty send still hit the server: %v", server.requests)
	}
}

func TestSearchPrintsEverySectionAndSaysWhenEmpty(t *testing.T) {
	results := map[string]any{"data": map[string]any{
		"messages": []map[string]any{{
			"id": 1, "body": "a hit\nwith a newline", "sender": map[string]any{"name": "Jack"},
			"channel": map[string]any{"slug": "general", "name": "General", "kind": "channel"},
		}},
		"tasks":     []map[string]any{{"slug": "t-1", "title": "Ship", "state": "open"}},
		"documents": []map[string]any{{"slug": "d-1", "title": "Notes"}},
		"uploads":   []map[string]any{{"slug": "u-1", "title": "diagram.png"}},
	}}
	server := newStubServer(t, map[string]any{"GET /api/v1/search": results})
	signIn(t, server.URL)

	out, _, err := capture(t, func() error { return runSearch([]string{"release", "--channel", "general"}) })
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"msg", "task", "doc", "file", "Notes", "diagram.png"} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
	// A newline inside a message body would break the one-hit-per-line shape.
	if strings.Count(strings.TrimRight(out, "\n"), "\n") != 3 {
		t.Errorf("want exactly 4 lines, one per hit:\n%q", out)
	}
	req := server.took(t, "GET", "/api/v1/search")
	if req.Query.Get("q") != "release" || req.Query.Get("channel") != "general" {
		t.Errorf("query did not reach the server: %v", req.Query)
	}

	empty := newStubServer(t, map[string]any{"GET /api/v1/search": map[string]any{"data": map[string]any{}}})
	signIn(t, empty.URL)
	out, errOut, err := capture(t, func() error { return runSearch([]string{"nothing"}) })
	if err != nil {
		t.Fatal(err)
	}
	if out != "" {
		t.Errorf("no hits must print nothing on stdout, got %q", out)
	}
	if !strings.Contains(errOut, "no results") {
		t.Errorf("the empty notice belongs on stderr, got %q", errOut)
	}
}

func TestTasksDefaultsToMineAndPassesStateThrough(t *testing.T) {
	server := newStubServer(t, map[string]any{
		"GET /api/v1/tasks": map[string]any{"data": []map[string]any{
			{"slug": "t-1", "title": "Ship it", "state": "open", "due_date": "2026-09-20"},
			{"slug": "t-2", "title": "Undated", "state": "in_progress"},
		}},
	})
	signIn(t, server.URL)

	out, _, err := capture(t, func() error { return runTasks([]string{"--state", "open"}) })
	if err != nil {
		t.Fatal(err)
	}

	req := server.took(t, "GET", "/api/v1/tasks")
	if req.Query.Get("mine") != "true" {
		t.Error("sal tasks must default to your own tasks")
	}
	if req.Query.Get("state") != "open" {
		t.Errorf("--state did not reach the server: %v", req.Query)
	}
	if !strings.Contains(out, "due 2026-09-20") {
		t.Errorf("a due date should print, got %q", out)
	}
	if strings.Contains(out, "due <nil>") || strings.Contains(out, "Undated  due") {
		t.Errorf("an undated task must print no due suffix, got %q", out)
	}
}

func TestTasksAllDropsTheMineFilter(t *testing.T) {
	server := newStubServer(t, map[string]any{
		"GET /api/v1/tasks": map[string]any{"data": []map[string]any{}},
	})
	signIn(t, server.URL)

	if _, _, err := capture(t, func() error { return runTasks([]string{"--all"}) }); err != nil {
		t.Fatal(err)
	}
	if mine := server.took(t, "GET", "/api/v1/tasks").Query.Get("mine"); mine != "" {
		t.Errorf("--all must not send mine=%q", mine)
	}
}

func TestAgendaWindowsTheFetchAndGroupsByDay(t *testing.T) {
	today := time.Now()
	server := newStubServer(t, map[string]any{
		"GET /api/v1/tasks": map[string]any{"data": []map[string]any{
			{"slug": "t-today", "title": "Due today", "state": "open",
				"due_date": today.Format("2006-01-02")},
			{"slug": "t-late", "title": "Overdue", "state": "open",
				"due_date": today.AddDate(0, 0, -3).Format("2006-01-02")},
		}},
	})
	signIn(t, server.URL)

	out, _, err := capture(t, func() error { return runAgenda([]string{"--days", "3"}) })
	if err != nil {
		t.Fatal(err)
	}

	req := server.took(t, "GET", "/api/v1/tasks")
	if req.Query.Get("mine") != "true" {
		t.Error("agenda is your own work")
	}
	if req.Query.Get("due_before") == "" {
		t.Error("agenda must window the fetch with due_before rather than pulling everything")
	}
	if !strings.Contains(out, "Overdue") || !strings.Contains(out, "Due today") {
		t.Errorf("both tasks should appear:\n%s", out)
	}
	// The overdue task carries its original date, since its section has none.
	if !strings.Contains(out, "was due "+today.AddDate(0, 0, -3).Format("2006-01-02")) {
		t.Errorf("an overdue task should show the date it was due:\n%s", out)
	}
}

func TestAgendaRejectsAZeroDayWindow(t *testing.T) {
	if _, _, err := capture(t, func() error { return runAgenda([]string{"--days", "0"}) }); err == nil {
		t.Fatal("want an error for --days 0")
	}
}

func TestCommandsFailCleanlyBeforeLogin(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	keyring.MockInit()

	for name, run := range map[string]func() error{
		"channels": func() error { return runChannels(nil) },
		"tasks":    func() error { return runTasks(nil) },
		"agenda":   func() error { return runAgenda(nil) },
		"db":       func() error { return runDB(nil) },
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := capture(t, run)
			if err == nil {
				t.Fatal("want an error when no config exists")
			}
			if !strings.Contains(err.Error(), "sal login") {
				t.Errorf("the error should point at `sal login`, got %q", err)
			}
		})
	}
}

func dbRoutes(rowPages map[string][]map[string]any) map[string]any {
	return map[string]any{
		"GET /api/v1/databases/contacts": map[string]any{"data": map[string]any{
			"id": 1, "slug": "contacts", "name": "Contacts", "rows_count": 2,
			"schema": map[string]any{"columns": []map[string]any{
				{"key": "name", "name": "Full name", "type": "text"},
				{"key": "email", "name": "Email", "type": "text"},
			}},
		}},
		"GET /api/v1/databases/contacts/rows": func(r *http.Request) any {
			return map[string]any{"data": rowPages[r.URL.Query().Get("page")]}
		},
	}
}

// Scripts parse the header line, so it carries column *keys* — the human
// names are the TUI's business. Column order follows the schema, not whatever
// order the row hash happens to marshal in.
func TestDBRowsHeadsTheTSVWithColumnKeysInSchemaOrder(t *testing.T) {
	server := newStubServer(t, dbRoutes(map[string][]map[string]any{
		"1": {
			{"id": 1, "position": 0, "data": map[string]any{"email": "jane@example.com", "name": "Jane"}},
			{"id": 2, "position": 1, "data": map[string]any{"name": "Bob", "email": "bob@example.com"}},
		},
	}))
	signIn(t, server.URL)

	out, _, err := capture(t, func() error { return dbRows([]string{"contacts"}) })
	if err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("want a header plus two rows, got %q", out)
	}
	if lines[0] != "id\tname\temail" {
		t.Errorf("header = %q, want id then the column keys in schema order", lines[0])
	}
	if lines[1] != "1\tJane\tjane@example.com" {
		t.Errorf("row 1 = %q — cells must follow the header, not the hash order", lines[1])
	}
}

func TestDBRowsCSVAndLimit(t *testing.T) {
	server := newStubServer(t, dbRoutes(map[string][]map[string]any{
		"1": {
			{"id": 1, "position": 0, "data": map[string]any{"name": "Jane, Q.", "email": "jane@example.com"}},
			{"id": 2, "position": 1, "data": map[string]any{"name": "Bob", "email": "bob@example.com"}},
		},
	}))
	signIn(t, server.URL)

	out, _, err := capture(t, func() error { return dbRows([]string{"contacts", "--csv", "--limit", "1"}) })
	if err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("--limit 1 must yield a header plus one row, got %q", out)
	}
	if lines[0] != "id,name,email" {
		t.Errorf("csv header = %q", lines[0])
	}
	// A comma inside a cell has to be quoted or the CSV is wrong.
	if lines[1] != `1,"Jane, Q.",jane@example.com` {
		t.Errorf("csv row = %q, want the comma-bearing cell quoted", lines[1])
	}
}

// The whole table by default: a 100-row page means there is another page.
func TestDBRowsPagesUntilShort(t *testing.T) {
	full := make([]map[string]any, 100)
	for i := range full {
		full[i] = map[string]any{"id": i + 1, "position": i, "data": map[string]any{"name": "row", "email": "e"}}
	}
	server := newStubServer(t, dbRoutes(map[string][]map[string]any{
		"1": full,
		"2": {{"id": 101, "position": 100, "data": map[string]any{"name": "last", "email": "e"}}},
	}))
	signIn(t, server.URL)

	out, _, err := capture(t, func() error { return dbRows([]string{"contacts"}) })
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(strings.TrimRight(out, "\n"), "\n"); got != 101 {
		t.Fatalf("want 101 rows after the header, got %d", got)
	}
	if !strings.Contains(out, "last") {
		t.Error("the second page never made it into the output")
	}
}

// A table with no columns yet has no meaningful TSV shape; say so instead of
// printing an empty header.
func TestDBRowsExplainsASchemalessTable(t *testing.T) {
	server := newStubServer(t, map[string]any{
		"GET /api/v1/databases/empty": map[string]any{"data": map[string]any{
			"id": 1, "slug": "empty", "name": "Empty",
		}},
	})
	signIn(t, server.URL)

	_, _, err := capture(t, func() error { return dbRows([]string{"empty"}) })
	if err == nil {
		t.Fatal("want an error for a table with no columns")
	}
	if !strings.Contains(err.Error(), "no columns") {
		t.Errorf("error should explain the cause, got %q", err)
	}
}

func TestDBRowsRequiresASlug(t *testing.T) {
	if _, _, err := capture(t, func() error { return dbRows(nil) }); err == nil {
		t.Fatal("want a usage error with no slug")
	}
}

func TestFilesListPrintsSlugsAndSizes(t *testing.T) {
	server := newStubServer(t, map[string]any{
		"GET /api/v1/uploads": map[string]any{"data": []map[string]any{
			{"id": 1, "slug": "diagram", "title": "diagram.png", "byte_size": 2048, "content_type": "image/png"},
		}},
	})
	signIn(t, server.URL)

	out, _, err := capture(t, func() error { return runFiles([]string{"-q", "diagram"}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "diagram") {
		t.Errorf("output should name the upload, got %q", out)
	}
	if q := server.took(t, "GET", "/api/v1/uploads").Query.Get("q"); q != "diagram" {
		t.Errorf("-q did not reach the server: %q", q)
	}
}

// Piped output is raw markdown — glamour's ANSI would corrupt anything
// downstream. capture() replaces stdout with a pipe, which is exactly the
// non-TTY case.
func TestDocsCatPrintsRawMarkdownWhenPiped(t *testing.T) {
	server := newStubServer(t, map[string]any{
		"GET /api/v1/documents/notes": map[string]any{"data": map[string]any{
			"id": 1, "slug": "notes", "title": "Notes", "body": "# Heading\n\nbody text",
		}},
	})
	signIn(t, server.URL)

	out, _, err := capture(t, func() error { return docsCat([]string{"notes"}) })
	if err != nil {
		t.Fatal(err)
	}
	if out != "# Heading\n\nbody text\n" {
		t.Fatalf("piped output must be the raw body, got %q", out)
	}
	if strings.Contains(out, "\x1b[") {
		t.Error("piped output must carry no ANSI escapes")
	}
}
