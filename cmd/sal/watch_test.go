package main

import (
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"

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
