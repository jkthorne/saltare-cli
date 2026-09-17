package main

import (
	"encoding/json"
	"os"
	"strconv"
	"testing"

	"github.com/jkthorne/saltare-cli/internal/api"
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
