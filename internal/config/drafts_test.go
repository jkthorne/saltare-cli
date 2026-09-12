package config

import (
	"os"
	"path/filepath"
	"testing"
)

func draftsFilePath(t *testing.T, home string) string {
	t.Helper()
	return filepath.Join(home, ".config", "saltare", "drafts.json")
}

// Drafts are a convenience, never a reason to fail: an unreadable or corrupt
// file starts the session clean rather than erroring on boot.
func TestLoadDraftsStartsCleanWhenThereIsNothingUsable(t *testing.T) {
	home := withHome(t)

	if got := LoadDrafts(); len(got) != 0 {
		t.Fatalf("want no drafts before anything is saved, got %v", got)
	}

	dir := filepath.Join(home, ".config", "saltare")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(draftsFilePath(t, home), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := LoadDrafts(); len(got) != 0 {
		t.Fatalf("a corrupt drafts file must read as empty, got %v", got)
	}
}

func TestDraftsRoundTrip(t *testing.T) {
	home := withHome(t)

	want := map[string]string{
		"acme/general":   "half a thought",
		"acme/eng":       "another\nwith a newline",
		"other-ws/eng":   "same channel slug, different workspace",
		"acme/thread-42": "",
	}
	if err := SaveDrafts(want); err != nil {
		t.Fatal(err)
	}

	got := LoadDrafts()
	if len(got) != len(want) {
		t.Fatalf("want %d drafts, got %d: %v", len(want), len(got), got)
	}
	for key, body := range want {
		if got[key] != body {
			t.Errorf("draft %q = %q, want %q", key, got[key], body)
		}
	}

	info, err := os.Stat(draftsFilePath(t, home))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("drafts.json mode = %o, want 600", perm)
	}
}

// Clearing the last draft removes the file instead of leaving an empty object
// behind — and removing an already-absent file is not an error, since the TUI
// saves drafts on every channel switch.
func TestSaveDraftsRemovesTheFileWhenEmpty(t *testing.T) {
	home := withHome(t)

	if err := SaveDrafts(map[string]string{"acme/general": "text"}); err != nil {
		t.Fatal(err)
	}
	if err := SaveDrafts(map[string]string{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(draftsFilePath(t, home)); !os.IsNotExist(err) {
		t.Fatal("an empty draft set must delete drafts.json")
	}
	if err := SaveDrafts(nil); err != nil {
		t.Fatalf("saving no drafts twice must not error: %v", err)
	}
	if got := LoadDrafts(); len(got) != 0 {
		t.Fatalf("want no drafts, got %v", got)
	}
}
