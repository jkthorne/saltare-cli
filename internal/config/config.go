// Package config persists sal's non-secret settings under ~/.config/saltare
// and its tokens in the OS keychain (file fallback). One config = one signed-in
// device session; multi-workspace switching re-runs `sal login`.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrNotConfigured means no config file exists yet — the caller should tell
// the user to run `sal login`.
var ErrNotConfigured = errors.New("not configured; run `sal login`")

type Config struct {
	ServerURL     string `json:"server_url"`
	WorkspaceSlug string `json:"workspace_slug"`
	WorkspaceName string `json:"workspace_name"`
	Email         string `json:"email"`
	// DeviceID is minted once per install; re-logins with the same id revoke
	// the prior session server-side instead of accumulating sessions.
	DeviceID string `json:"device_id"`
	// UserID of the signed-in user — used to self-assign tasks created here.
	UserID int64 `json:"user_id"`
}

// Dir returns ~/.config/saltare, creating it if needed. Deliberately not
// os.UserConfigDir: on macOS that is ~/Library/Application Support, and a
// terminal tool wants the greppable dotfile convention on every platform.
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".config", "saltare")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

func path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

func Load() (*Config, error) {
	p, err := path()
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotConfigured
	}
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", p, err)
	}
	if cfg.ServerURL == "" {
		return nil, ErrNotConfigured
	}
	return &cfg, nil
}

func (c *Config) Save() error {
	p, err := path()
	if err != nil {
		return err
	}
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, append(raw, '\n'), 0o600)
}

func Delete() error {
	p, err := path()
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
