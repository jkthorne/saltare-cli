package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// Drafts persist unsent composer text per channel in a sibling of
// config.json, keyed "{workspace_slug}/{channel_slug}" so one file serves
// re-logins into different workspaces.

func draftsPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "drafts.json"), nil
}

func LoadDrafts() map[string]string {
	p, err := draftsPath()
	if err != nil {
		return map[string]string{}
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return map[string]string{} // missing or unreadable — start clean
	}
	drafts := map[string]string{}
	if json.Unmarshal(raw, &drafts) != nil {
		return map[string]string{}
	}
	return drafts
}

func SaveDrafts(drafts map[string]string) error {
	p, err := draftsPath()
	if err != nil {
		return err
	}
	if len(drafts) == 0 {
		err := os.Remove(p)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	raw, err := json.MarshalIndent(drafts, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, raw, 0o600)
}
