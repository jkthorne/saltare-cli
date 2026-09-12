package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withHome points Dir() at a scratch home. Every config path derives from
// os.UserHomeDir, so this keeps tests off the developer's real ~/.config.
func withHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

func TestLoadReportsNotConfiguredBeforeFirstLogin(t *testing.T) {
	withHome(t)

	if _, err := Load(); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("want ErrNotConfigured, got %v", err)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	withHome(t)

	want := Config{
		ServerURL:     "https://saltare.example",
		WorkspaceSlug: "acme",
		WorkspaceName: "Acme",
		Email:         "jack@example.com",
		DeviceID:      "dev-1",
		UserID:        7,
		UserName:      "Jack",
	}
	if err := want.Save(); err != nil {
		t.Fatal(err)
	}

	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if *got != want {
		t.Fatalf("round trip changed the config:\n got %+v\nwant %+v", *got, want)
	}
}

// A config file with no server_url cannot be logged in with, so it has to read
// as "not configured" rather than as a usable config with an empty host.
func TestLoadTreatsAMissingServerURLAsNotConfigured(t *testing.T) {
	home := withHome(t)
	writeConfig(t, home, `{"workspace_slug":"acme"}`)

	if _, err := Load(); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("want ErrNotConfigured, got %v", err)
	}
}

func TestLoadNamesTheFileOnParseFailure(t *testing.T) {
	home := withHome(t)
	writeConfig(t, home, `{not json`)

	_, err := Load()
	if err == nil {
		t.Fatal("want a parse error")
	}
	if errors.Is(err, ErrNotConfigured) {
		t.Fatal("corrupt config must not masquerade as not-configured")
	}
	if !strings.Contains(err.Error(), "config.json") {
		t.Fatalf("error should name the file, got %q", err)
	}
}

// The config file carries the server URL, the email and the device id; it is
// not world-readable by accident.
func TestSaveWritesOwnerOnly(t *testing.T) {
	home := withHome(t)
	cfg := Config{ServerURL: "https://saltare.example"}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(filepath.Join(home, ".config", "saltare", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("config.json mode = %o, want 600", perm)
	}
}

func TestDirCreatesAPrivateDirectory(t *testing.T) {
	home := withHome(t)

	dir, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".config", "saltare"); dir != want {
		t.Fatalf("dir = %q, want %q", dir, want)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Fatalf("config dir mode = %o, want 700", perm)
	}
}

func TestDeleteIsIdempotent(t *testing.T) {
	withHome(t)
	cfg := Config{ServerURL: "https://saltare.example"}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	if err := Delete(); err != nil {
		t.Fatal(err)
	}
	if err := Delete(); err != nil {
		t.Fatalf("deleting an absent config must not error: %v", err)
	}
	if _, err := Load(); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("want ErrNotConfigured after delete, got %v", err)
	}
}

// Mouse is a *bool precisely so an older config file — written before the key
// existed — means "on", not "off".
func TestMouseDefaultsOnAndOnlyOffByDecision(t *testing.T) {
	var absent Config
	if !absent.MouseEnabled() {
		t.Error("a config with no mouse key must default to mouse on")
	}

	var off Config
	off.SetMouse(false)
	if off.MouseEnabled() {
		t.Error("SetMouse(false) must disable the mouse")
	}
	if off.Mouse == nil {
		t.Error("SetMouse must record the decision, not leave it unset")
	}

	var on Config
	on.SetMouse(true)
	if !on.MouseEnabled() {
		t.Error("SetMouse(true) must enable the mouse")
	}
}

// An unset mouse must not serialize, or reading the config back would turn
// "unset" into an explicit false.
func TestUnsetMouseIsOmittedFromTheSavedConfig(t *testing.T) {
	home := withHome(t)
	cfg := Config{ServerURL: "https://saltare.example"}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(filepath.Join(home, ".config", "saltare", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "mouse") {
		t.Fatalf("unset mouse must not be written:\n%s", raw)
	}

	cfg.SetMouse(false)
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.Mouse == nil || *got.Mouse {
		t.Fatal("an explicit mouse:false must survive a round trip")
	}
}

func writeConfig(t *testing.T, home, body string) {
	t.Helper()
	dir := filepath.Join(home, ".config", "saltare")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
