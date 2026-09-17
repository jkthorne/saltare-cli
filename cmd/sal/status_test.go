package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jkthorne/saltare-cli/internal/watch"
)

func fresh() *watch.State {
	return &watch.State{
		Schema: watch.Schema, UpdatedAt: time.Now(), HeartbeatSec: watch.HeartbeatSec,
		Session: watch.SessionOK, Live: true,
		Workspace: watch.Workspace{Slug: "acme", Name: "Acme"},
	}
}

func TestStatusClassPrecedence(t *testing.T) {
	now := time.Now()

	mention := fresh()
	mention.Totals.Mentions = 1
	if got := statusClass(mention, now); got != "mention" {
		t.Errorf("mention: got %q", got)
	}

	// A dropped socket outranks a mention: the count on screen is last-known,
	// and saying so matters more than saying someone called your name.
	offline := fresh()
	offline.Totals.Mentions = 1
	offline.Live = false
	if got := statusClass(offline, now); got != "offline" {
		t.Errorf("offline: got %q", got)
	}

	stopped := fresh()
	stopped.UpdatedAt = now.Add(-time.Duration(4*watch.HeartbeatSec) * time.Second)
	stopped.Live = false
	if got := statusClass(stopped, now); got != "stopped" {
		t.Errorf("stopped: got %q", got)
	}

	// Signed out outranks stale: restarting a watcher with nothing to sign in
	// with fixes nothing.
	signin := fresh()
	signin.UpdatedAt = now.Add(-time.Hour)
	signin.Session = watch.SessionLoggedOut
	if got := statusClass(signin, now); got != "signin" {
		t.Errorf("signin: got %q", got)
	}

	if got := statusClass(fresh(), now); got != "ok" {
		t.Errorf("ok: got %q", got)
	}
}

func TestBadgeIsEmptyWithNothingWaiting(t *testing.T) {
	if got := badge(fresh()); got != "" {
		t.Errorf("a bar should show no number rather than a zero, got %q", got)
	}
	s := fresh()
	s.Totals.Unread = 13
	if got := badge(s); got != "13" {
		t.Errorf("got %q", got)
	}
}

func TestLineSaysAllClearRatherThanJustTheWorkspace(t *testing.T) {
	got := line(fresh(), time.Now())
	if !strings.Contains(got, "all clear") {
		t.Errorf("got %q", got)
	}
}

func TestPrintStatusWaybarAlwaysEmitsJSONEvenWithNoFile(t *testing.T) {
	var out bytes.Buffer
	// A bar module that prints nothing leaves a hole where the widget was;
	// printing the setup state is what makes the instruction reachable.
	if err := printStatus(&out, filepath.Join(t.TempDir(), "absent.json"), false, true); err != nil {
		t.Fatalf("waybar output must not fail: %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %v (%q)", err, out.String())
	}
	if got["class"] != "setup" || !strings.Contains(got["tooltip"], "sal watch") {
		t.Errorf("the setup state should name its own fix, got %v", got)
	}
}

func TestPrintStatusHumanFailsWithNoFile(t *testing.T) {
	var out bytes.Buffer
	err := printStatus(&out, filepath.Join(t.TempDir(), "absent.json"), false, false)
	if err == nil {
		t.Fatal("a prompt or a script needs a non-zero exit here")
	}
	if !strings.Contains(err.Error(), "sal watch") {
		t.Errorf("the error should name the fix, got %q", err)
	}
}

func TestPrintStatusReadsAPublishedDocument(t *testing.T) {
	path := filepath.Join(t.TempDir(), "watch.json")
	w := watch.NewWriter(path)
	s := *fresh()
	s.Totals = watch.Totals{Unread: 13, Mentions: 1, Overdue: 1, DueToday: 1}
	if _, err := w.Publish(s); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := printStatus(&out, path, false, false); err != nil {
		t.Fatalf("read: %v", err)
	}
	for _, want := range []string{"Acme", "13 unread", "1 mention", "1 overdue", "1 due today"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in %q", want, out.String())
		}
	}
}

func TestAgo(t *testing.T) {
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "just now"},
		{5 * time.Minute, "5m ago"},
		{3 * time.Hour, "3h ago"},
		{50 * time.Hour, "2d ago"},
	} {
		if got := ago(tc.d); got != tc.want {
			t.Errorf("%v: want %q, got %q", tc.d, tc.want, got)
		}
	}
}
