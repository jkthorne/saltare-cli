package watch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jkthorne/saltare-cli/internal/api"
)

func tempPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "state", "watch.json")
}

func TestPublishWritesOwnerOnlyAndReadsBack(t *testing.T) {
	path := tempPath(t)
	w := NewWriter(path)

	in := baseInputs()
	in.Channels = []api.Channel{channel(1, "general", 3)}
	changed, err := w.Publish(Reduce(in))
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if !changed {
		t.Error("the first publish is always a change")
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		// The document carries message previews and task titles.
		t.Errorf("want owner-only 0600, got %o", perm)
	}

	got, err := Read(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.Totals.Unread != 3 || got.Workspace.Slug != "acme" || got.Schema != Schema {
		t.Errorf("round trip lost something: %+v", got)
	}
}

func TestPublishSkipsARepeatWithinTheHeartbeat(t *testing.T) {
	path := tempPath(t)
	w := NewWriter(path)
	in := baseInputs()

	if _, err := w.Publish(Reduce(in)); err != nil {
		t.Fatalf("first publish: %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	// Same content, a second later: nothing to say and the last word is fresh.
	in.Now = in.Now.Add(time.Second)
	changed, err := w.Publish(Reduce(in))
	if err != nil {
		t.Fatalf("second publish: %v", err)
	}
	if changed {
		t.Error("identical content is not news")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("a repeat inside the heartbeat window should not rewrite")
	}
}

func TestHeartbeatRewritesSoReadersSeeAFreshTimestamp(t *testing.T) {
	// The bug this exists for: the heartbeat used to move mtime and leave the
	// document's own UpdatedAt alone. Staleness is judged on UpdatedAt, so a
	// perfectly healthy daemon read as "stopped" ninety seconds after the last
	// thing happened — which on a quiet workspace is always.
	path := tempPath(t)
	w := NewWriter(path)
	in := baseInputs()

	if _, err := w.Publish(Reduce(in)); err != nil {
		t.Fatal(err)
	}

	in.Now = in.Now.Add(time.Duration(HeartbeatSec) * time.Second)
	changed, err := w.Publish(Reduce(in))
	if err != nil {
		t.Fatalf("heartbeat publish: %v", err)
	}
	if changed {
		t.Error("a heartbeat is not a content change, and callers tell them apart")
	}

	got, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if !got.UpdatedAt.Equal(in.Now) {
		t.Errorf("the document must carry the heartbeat's time: want %v, got %v", in.Now, got.UpdatedAt)
	}
	if got.Stale(in.Now) {
		t.Error("a document just written must not read as stale")
	}
}

func TestAHealthyDaemonNeverReadsAsStale(t *testing.T) {
	// End to end over the rule readers actually apply: publish on the heartbeat
	// for ten minutes of an idle workspace and never once look stopped.
	path := tempPath(t)
	w := NewWriter(path)
	in := baseInputs()

	for minute := 0; minute < 20; minute++ {
		in.Now = in.Now.Add(time.Duration(HeartbeatSec) * time.Second)
		if _, err := w.Publish(Reduce(in)); err != nil {
			t.Fatalf("publish at %v: %v", in.Now, err)
		}
		got, err := Read(path)
		if err != nil {
			t.Fatal(err)
		}
		if got.Stale(in.Now) {
			t.Fatalf("stale after %d heartbeats on an idle workspace", minute+1)
		}
	}
}

func TestPublishWritesAgainWhenCountsMove(t *testing.T) {
	path := tempPath(t)
	w := NewWriter(path)
	in := baseInputs()
	if _, err := w.Publish(Reduce(in)); err != nil {
		t.Fatal(err)
	}

	in.Channels = []api.Channel{channel(1, "general", 1)}
	changed, err := w.Publish(Reduce(in))
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("a new unread count is a change")
	}
	got, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Totals.Unread != 1 {
		t.Errorf("want the new count on disk, got %d", got.Totals.Unread)
	}
}

func TestPublishRepublishesAfterTheFileIsRemoved(t *testing.T) {
	path := tempPath(t)
	w := NewWriter(path)
	in := baseInputs()
	if _, err := w.Publish(Reduce(in)); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	// Same content, so the fingerprint matches — but there is nothing to touch,
	// and a reader left with no file would sit in "setup" forever.
	if _, err := w.Publish(Reduce(in)); err != nil {
		t.Fatalf("publish over a removed file: %v", err)
	}
	in.Now = in.Now.Add(time.Second)
	if _, err := w.Publish(Reduce(in)); err != nil {
		t.Fatalf("second publish: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("the file should be back: %v", err)
	}
}

func TestStaleAfterThreeMissedHeartbeats(t *testing.T) {
	s := Reduce(baseInputs())
	now := s.UpdatedAt

	if s.Stale(now.Add(time.Duration(2*HeartbeatSec) * time.Second)) {
		t.Error("two beats is a busy machine, not a dead one")
	}
	if !s.Stale(now.Add(time.Duration(3*HeartbeatSec+1) * time.Second)) {
		t.Error("past three beats the daemon has stopped")
	}
}

// The golden is the contract with the view: tests/fixtures/watch.json in the
// plugin repo is this byte-for-byte, so a schema change that lands on one side
// fails on the other.
func TestGoldenDocument(t *testing.T) {
	in := baseInputs()
	in.Channels = []api.Channel{
		channel(1, "general", 3),
		channel(2, "engineering", 9),
		{ID: 3, Slug: "alice-chen", DisplayName: "Alice Chen", Kind: "dm", UnreadCount: ptr(1)},
		// agent_dm earns its place here: the view guessed this kind was "dm"
		// and rendered a live bar row as "#DevOps Monitor".
		{ID: 5, Slug: "agent-dm-2-1-f00b", DisplayName: "Code Reviewer", Kind: "agent_dm", UnreadCount: ptr(4)},
		channel(4, "quiet", 0),
	}
	in.Notifications = []api.Notification{mention(991, 1, "general", "Alice Chen")}
	in.Tasks = []api.Task{
		{Slug: "fix-deploy", Title: "Fix the deploy", State: "open", DueDate: ptr("2026-09-14")},
		{Slug: "ship-plugin", Title: "Ship the Omarchy plugin", State: "in_progress", DueDate: ptr("2026-09-16")},
	}

	body, err := json.MarshalIndent(Reduce(in), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	body = append(body, '\n')

	path := filepath.Join("testdata", "golden.json")
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(path, body, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Log("golden rewritten; copy it to the plugin repo's tests/fixtures/watch.json")
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (regenerate with UPDATE_GOLDEN=1): %v", err)
	}
	if string(want) != string(body) {
		t.Errorf("the published document drifted from the golden.\n--- want ---\n%s\n--- got ---\n%s", want, body)
	}
}
