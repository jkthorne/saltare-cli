package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
)

// lockedKeychain is what a headless Linux box, a container, or a locked
// login keyring looks like to go-keyring.
var lockedKeychain = errors.New("keyring unavailable")

func sampleTokens() Tokens {
	return Tokens{
		AccessToken:      "sk_sal_access",
		RefreshToken:     "rt_sal_refresh",
		AccessExpiresAt:  time.Date(2026, 10, 10, 7, 33, 47, 0, time.UTC),
		RefreshExpiresAt: time.Date(2027, 3, 9, 7, 33, 47, 0, time.UTC),
	}
}

func credsFilePath(t *testing.T, home string) string {
	t.Helper()
	return filepath.Join(home, ".config", "saltare", credsFile)
}

func TestTokensRoundTripThroughTheKeychain(t *testing.T) {
	home := withHome(t)
	keyring.MockInit()

	want := sampleTokens()
	if err := SaveTokens(want); err != nil {
		t.Fatal(err)
	}

	got, err := LoadTokens()
	if err != nil {
		t.Fatal(err)
	}
	if !got.AccessExpiresAt.Equal(want.AccessExpiresAt) || got.AccessToken != want.AccessToken ||
		got.RefreshToken != want.RefreshToken || !got.RefreshExpiresAt.Equal(want.RefreshExpiresAt) {
		t.Fatalf("round trip changed the tokens:\n got %+v\nwant %+v", got, want)
	}

	// A working keychain must not also spill the tokens to disk.
	if _, err := os.Stat(credsFilePath(t, home)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("tokens were written to disk despite a working keychain")
	}
	if src := TokensSource(); src != "keychain" {
		t.Fatalf("source = %q, want keychain", src)
	}
}

// Without a usable keychain sal still has to work — but the fallback file
// holds a live bearer token, so it must be owner-only.
func TestTokensFallBackToAnOwnerOnlyFile(t *testing.T) {
	home := withHome(t)
	keyring.MockInitWithError(lockedKeychain)

	want := sampleTokens()
	if err := SaveTokens(want); err != nil {
		t.Fatal(err)
	}

	path := credsFilePath(t, home)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("no fallback file written: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("%s mode = %o, want 600", credsFile, perm)
	}

	got, err := LoadTokens()
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != want.AccessToken || got.RefreshToken != want.RefreshToken {
		t.Fatalf("fallback round trip changed the tokens: %+v", got)
	}
	if src := TokensSource(); src != "file "+path {
		t.Fatalf("source = %q, want %q", src, "file "+path)
	}
}

func TestLoadTokensReportsNoTokensBeforeLogin(t *testing.T) {
	withHome(t)
	keyring.MockInitWithError(lockedKeychain)

	if _, err := LoadTokens(); !errors.Is(err, ErrNoTokens) {
		t.Fatalf("want ErrNoTokens, got %v", err)
	}
	if src := TokensSource(); src != "none" {
		t.Fatalf("source = %q, want none", src)
	}
}

// `sal logout` must not leave a usable token behind in either store, whichever
// one the session happened to be using.
func TestDeleteTokensClearsBothStores(t *testing.T) {
	home := withHome(t)

	// A file left over from an earlier keychain-less run...
	keyring.MockInitWithError(lockedKeychain)
	if err := SaveTokens(sampleTokens()); err != nil {
		t.Fatal(err)
	}
	// ...and a keychain entry from a later one.
	keyring.MockInit()
	if err := SaveTokens(sampleTokens()); err != nil {
		t.Fatal(err)
	}

	if err := DeleteTokens(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(credsFilePath(t, home)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("logout left the fallback credentials file behind")
	}
	if _, err := keyring.Get(keyringService, keyringUser); err == nil {
		t.Fatal("logout left the keychain entry behind")
	}
	if _, err := LoadTokens(); !errors.Is(err, ErrNoTokens) {
		t.Fatalf("want ErrNoTokens after logout, got %v", err)
	}
}

func TestDeleteTokensIsIdempotent(t *testing.T) {
	withHome(t)
	keyring.MockInit()

	if err := DeleteTokens(); err != nil {
		t.Fatalf("logout with nothing saved must not error: %v", err)
	}
}

func TestLoadTokensSurfacesACorruptStore(t *testing.T) {
	home := withHome(t)
	keyring.MockInitWithError(lockedKeychain)

	dir := filepath.Join(home, ".config", "saltare")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(credsFilePath(t, home), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := LoadTokens()
	if err == nil {
		t.Fatal("want a decode error")
	}
	if errors.Is(err, ErrNoTokens) {
		t.Fatal("a corrupt store must not read as a missing one — that would hide the real fault")
	}
}
