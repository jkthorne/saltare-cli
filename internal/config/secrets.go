package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/zalando/go-keyring"
)

const keyringService = "saltare-sal"
const keyringUser = "device-session"
const credsFile = "credentials.json"

var ErrNoTokens = errors.New("no saved tokens; run `sal login`")

// Tokens is the persisted half of a device session. The access token is an
// sk_sal_ ApiKey (30d); the refresh token rotates it (180d, one-time-use).
type Tokens struct {
	AccessToken      string    `json:"access_token"`
	RefreshToken     string    `json:"refresh_token"`
	AccessExpiresAt  time.Time `json:"access_expires_at"`
	RefreshExpiresAt time.Time `json:"refresh_expires_at"`
}

// SaveTokens prefers the OS keychain and falls back to a 0600 file for
// environments without one (headless Linux, containers).
func SaveTokens(t Tokens) error {
	raw, err := json.Marshal(t)
	if err != nil {
		return err
	}
	if err := keyring.Set(keyringService, keyringUser, string(raw)); err == nil {
		return nil
	}
	p, err := credsPath()
	if err != nil {
		return err
	}
	return os.WriteFile(p, append(raw, '\n'), 0o600)
}

func LoadTokens() (Tokens, error) {
	var t Tokens
	if raw, err := keyring.Get(keyringService, keyringUser); err == nil {
		if err := json.Unmarshal([]byte(raw), &t); err != nil {
			return t, err
		}
		return t, nil
	}
	p, err := credsPath()
	if err != nil {
		return t, err
	}
	raw, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return t, ErrNoTokens
	}
	if err != nil {
		return t, err
	}
	if err := json.Unmarshal(raw, &t); err != nil {
		return t, err
	}
	return t, nil
}

// TokensSource reports where LoadTokens would read from: "keychain", "file
// <path>", or "none". A locked or unavailable keychain silently demotes sal to
// the file fallback, which is worth seeing when diagnosing a broken session.
func TokensSource() string {
	if _, err := keyring.Get(keyringService, keyringUser); err == nil {
		return "keychain"
	}
	p, err := credsPath()
	if err != nil {
		return "unknown"
	}
	if _, err := os.Stat(p); err == nil {
		return "file " + p
	}
	return "none"
}

func DeleteTokens() error {
	// Best-effort on both stores; only report unexpected file errors.
	_ = keyring.Delete(keyringService, keyringUser)
	p, err := credsPath()
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func credsPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, credsFile), nil
}
