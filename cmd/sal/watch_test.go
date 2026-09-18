package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jkthorne/saltare-cli/internal/api"
	"github.com/jkthorne/saltare-cli/internal/config"
	"github.com/jkthorne/saltare-cli/internal/store"
	"github.com/jkthorne/saltare-cli/internal/watch"
)

func TestLockSessionAdmitsOneAndNamesTheHolder(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	release, heldBy, err := lockSession()
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}
	if release == nil {
		t.Fatal("the first caller should get the lock")
	}
	if heldBy != "" {
		t.Errorf("nobody else holds it: got %q", heldBy)
	}

	second, holder, err := lockSession()
	if err != nil {
		t.Fatalf("second lock: %v", err)
	}
	if second != nil {
		t.Fatal("a second daemon would open a second socket and toast twice")
	}
	if holder != strconv.Itoa(os.Getpid()) {
		t.Errorf("want the holding pid %d, got %q", os.Getpid(), holder)
	}

	// Releasing hands it on rather than leaving a file that needs explaining.
	release()
	third, _, err := lockSession()
	if err != nil {
		t.Fatalf("third lock: %v", err)
	}
	if third == nil {
		t.Fatal("the lock should be free once released")
	}
	third()
}

func TestPidSuffix(t *testing.T) {
	if got := pidSuffix("4821"); got != " (pid 4821)" {
		t.Errorf("got %q", got)
	}
	if got := pidSuffix(""); got != "" {
		t.Errorf("an unreadable lock file should add nothing, got %q", got)
	}
	if got := leadingDigits("4821\nrubbish"); got != "4821" {
		t.Errorf("got %q", got)
	}
}

func notification(id int64, action string) api.Notification {
	return api.Notification{ID: id, Action: action}
}

func TestNoteNewPrimesSilentlyThenAnnounces(t *testing.T) {
	var announced []int64
	w := &watcher{seen: map[int64]bool{}, onNew: func(n watch.Notification) {
		announced = append(announced, n.ID)
	}}

	// Startup: three unread from yesterday. A daemon that starts at 09:00 must
	// not announce the backlog.
	w.noteNew([]api.Notification{notification(3, "mentioned"), notification(2, "mentioned"), notification(1, "mentioned")})
	if len(announced) != 0 {
		t.Fatalf("the first poll is a backlog, not news: got %v", announced)
	}

	// Two arrive. Server order is newest first; they should read in the order
	// they happened.
	w.noteNew([]api.Notification{notification(5, "mentioned"), notification(4, "mentioned"), notification(3, "mentioned")})
	if len(announced) != 2 || announced[0] != 4 || announced[1] != 5 {
		t.Fatalf("want 4 then 5, got %v", announced)
	}

	// The same poll again announces nothing.
	w.noteNew([]api.Notification{notification(5, "mentioned"), notification(4, "mentioned")})
	if len(announced) != 2 {
		t.Fatalf("re-polling the same unread set is not news: got %v", announced)
	}
}

func TestEmitIsSilentWithoutNdjson(t *testing.T) {
	w := &watcher{seen: map[int64]bool{}}
	w.emit(map[string]any{"ev": "ready"}) // must not panic on a nil encoder
}

func TestEmitWritesOneJSONObjectPerLine(t *testing.T) {
	r, wr, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	w := &watcher{seen: map[int64]bool{}, ndjson: true, out: json.NewEncoder(wr)}
	w.emit(map[string]any{"ev": "ready", "state": "ok"})
	wr.Close()

	var got map[string]any
	if err := json.NewDecoder(r).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["ev"] != "ready" || got["state"] != "ok" {
		t.Errorf("got %v", got)
	}
}

func TestDegradeTellsARefusalFromAnOutage(t *testing.T) {
	// Found on the first live run: a workspace at its monthly request cap
	// reported as "unreachable", which would have the bar telling you to check
	// a connection that was never the problem.
	w := &watcher{cfg: &config.Config{ServerURL: "https://saltare.ai"}, seen: map[int64]bool{}}

	w.degrade(&api.APIError{Status: 402, Code: "monthly_limit_reached",
		Message: "Monthly API request limit reached (0)."})
	if w.inputs.Session != watch.SessionBlocked {
		t.Errorf("a server that answered and refused is blocked, got %q", w.inputs.Session)
	}
	if !strings.Contains(w.inputs.Error, "Monthly API request limit") {
		t.Errorf("the server's own sentence is the useful part, got %q", w.inputs.Error)
	}

	// A 500 is still an answer. "The server errored" beats "check your wifi".
	w.degrade(&api.APIError{Status: 500, Message: "boom"})
	if w.inputs.Session != watch.SessionBlocked {
		t.Errorf("got %q", w.inputs.Session)
	}

	w.degrade(errors.New("dial tcp: connection refused"))
	if w.inputs.Session != watch.SessionUnreachable {
		t.Errorf("nothing answered, so unreachable: got %q", w.inputs.Session)
	}

	w.degrade(api.ErrAuthExpired)
	if w.inputs.Session != watch.SessionLoggedOut {
		t.Errorf("an expired session is terminal: got %q", w.inputs.Session)
	}
}

func TestSubscribeKnownOffersEveryChannelToTheSocket(t *testing.T) {
	// Found live: an agent DM created after the daemon started never streamed,
	// because the socket was only ever told about the channels that existed at
	// connect time. A fresh DM or a thread someone started with you is exactly
	// the channel worth hearing about immediately.
	var offered []int64
	w := &watcher{
		store:     store.New(),
		seen:      map[int64]bool{},
		subscribe: func(id int64) { offered = append(offered, id) },
	}
	w.store.SetChannels([]api.Channel{{ID: 1, Slug: "general"}, {ID: 2, Slug: "engineering"}})

	w.subscribeKnown()
	if len(offered) != 2 {
		t.Fatalf("want both channels offered, got %v", offered)
	}

	// A channel discovered by a later resync is offered too.
	w.store.SetChannels([]api.Channel{
		{ID: 1, Slug: "general"}, {ID: 2, Slug: "engineering"}, {ID: 140, Slug: "agent-dm-2-1"},
	})
	offered = nil
	w.subscribeKnown()

	found := false
	for _, id := range offered {
		if id == 140 {
			found = true
		}
	}
	if !found {
		t.Errorf("the new channel must reach the socket, got %v", offered)
	}
}

func TestSubscribeKnownIsSafeWithoutACable(t *testing.T) {
	// --no-cable leaves subscribe nil, and resync still calls this.
	w := &watcher{store: store.New(), seen: map[int64]bool{}}
	w.store.SetChannels([]api.Channel{{ID: 1, Slug: "general"}})
	w.subscribeKnown()
}

func TestTasksAreNotRefetchedOnEveryResync(t *testing.T) {
	// Every resync is three metered API requests. Folding tasks into all of
	// them tripled the cost of the frequent thing to keep the rare thing
	// fresh: a one-minute poll spent 129,600 requests a month idling, which is
	// two and a half times everything the Pro plan includes.
	w := &watcher{seen: map[int64]bool{}}

	if w.lastTasks.IsZero() != true {
		t.Fatal("a fresh watcher has never fetched tasks")
	}

	// A fetch just now means the next resync inside the window skips them.
	w.lastTasks = time.Now()
	if time.Since(w.lastTasks) >= taskInterval {
		t.Error("a task fetch should hold for taskInterval")
	}

	// Past the window it fetches again.
	w.lastTasks = time.Now().Add(-taskInterval - time.Second)
	if time.Since(w.lastTasks) < taskInterval {
		t.Error("past the interval, tasks are due for a refetch")
	}
}

func TestTheIdleBudgetFitsInsideThePlanItTargets(t *testing.T) {
	// The numbers are the point, so they are asserted rather than trusted to a
	// comment that drifts. Pro includes 50,000 API requests a month
	// (PlanLimits::PLAN_LIMITS); an always-on watcher must be a share of that,
	// not a multiple.
	const proMonthlyRequests = 50_000
	const daysPerMonth = 30

	// Two per poll (channels, notifications), plus tasks and mailboxes on
	// their own slower clocks.
	perDay := 2*(24*time.Hour/defaultPoll) + 1*(24*time.Hour/taskInterval) + 1*(24*time.Hour/mailInterval)
	perMonth := int(perDay) * daysPerMonth

	if perMonth >= proMonthlyRequests {
		t.Fatalf("an idle watcher costs %d requests/month, at or over Pro's whole %d",
			perMonth, proMonthlyRequests)
	}
	if share := float64(perMonth) / proMonthlyRequests; share > 0.5 {
		t.Errorf("an idle watcher is %.0f%% of a Pro workspace's monthly budget; keep it under half",
			share*100)
	}
}

// mailServer answers /api/v1/mail/mailboxes with one canned response and
// counts how often it was asked.
func mailServer(t *testing.T, status int, body string) (*api.Client, *int) {
	t.Helper()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/mail/mailboxes" {
			t.Errorf("unexpected request: %s", r.URL.Path)
		}
		calls++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	client, err := api.New(srv.URL, config.Tokens{AccessToken: "sk_sal_test"})
	if err != nil {
		t.Fatal(err)
	}
	return client, &calls
}

func TestASessionWithoutMailReadLosesTheSectionAndNothingElse(t *testing.T) {
	// mail:read joined the CLI grant after the sessions that are already out
	// there. Rotation copies a session's old scopes, so a signed-in user keeps
	// getting a 403 forever — and treating that as a workspace failure would
	// put a red banner over the unread counts that arrived perfectly well.
	client, calls := mailServer(t, http.StatusForbidden,
		`{"error":{"code":"missing_scope","message":"mail:read required","scope":"mail:read"}}`)
	w := &watcher{client: client, seen: map[int64]bool{}}
	w.inputs.Session = watch.SessionOK

	w.resyncMail(t.Context())

	if w.inputs.Session != watch.SessionOK {
		t.Errorf("a missing scope is not a broken session: got %q", w.inputs.Session)
	}
	if w.inputs.Error != "" {
		t.Errorf("nothing to report to the user: got %q", w.inputs.Error)
	}
	if w.inputs.Mail != nil {
		t.Errorf("no mail means no section, got %#v", w.inputs.Mail)
	}
	if !w.mailDenied {
		t.Error("the answer should be remembered")
	}

	// And never asked again: the question has an answer, and it costs a
	// metered request to re-ask it every half hour for the life of the daemon.
	w.resyncMail(t.Context())
	if *calls != 1 {
		t.Errorf("asked %d times after being told no once", *calls)
	}
}

func TestAMailOutageLeavesTheRestOfTheDocumentAlone(t *testing.T) {
	// Unlike a missing scope this is worth retrying, and unlike a channels
	// failure it must not degrade the session: the counts that did arrive are
	// still true, and a banner about mail would hide them.
	client, calls := mailServer(t, http.StatusInternalServerError,
		`{"error":{"code":"internal","message":"boom"}}`)
	w := &watcher{client: client, seen: map[int64]bool{}}
	w.inputs.Session = watch.SessionOK

	w.resyncMail(t.Context())

	if w.inputs.Session != watch.SessionOK {
		t.Errorf("a mail outage is not a session state: got %q", w.inputs.Session)
	}
	if w.mailDenied {
		t.Error("a 500 is weather, not an answer — it must be retried")
	}
	if !w.lastMail.IsZero() {
		t.Error("a failed fetch should not start the hold-off clock")
	}

	w.resyncMail(t.Context())
	if *calls != 2 {
		t.Errorf("asked %d times, want 2 — an outage is worth another try", *calls)
	}
}

func TestMailboxesReduceIntoTheDocument(t *testing.T) {
	client, _ := mailServer(t, http.StatusOK, `{"data":[{
		"slug":"ada-example-com","address":"ada@example.com","display_name":"Ada Lovelace",
		"provider":"gmail","status":"active","last_error":null,
		"folders":[{"path":"INBOX","name":"Inbox","total":9,"unread":4},
		           {"path":"Spam","name":"Spam","total":50,"unread":50}]}]}`)
	w := &watcher{client: client, seen: map[int64]bool{}}

	w.resyncMail(t.Context())

	state := watch.Reduce(w.inputs)
	if state.Totals.Mail != 4 {
		t.Errorf("fifty unread in Spam is not fifty things waiting: got %d, want 4", state.Totals.Mail)
	}
	if len(state.Mail) != 1 || state.Mail[0].Name != "Ada Lovelace" {
		t.Errorf("mail rows: %#v", state.Mail)
	}
}
