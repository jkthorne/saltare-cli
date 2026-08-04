package config

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// EditorCommand builds the user's editor invocation for a file. The value
// may carry flags ("code -w"), so it splits on whitespace.
func EditorCommand(path string) *exec.Cmd {
	raw := os.Getenv("VISUAL")
	if raw == "" {
		raw = os.Getenv("EDITOR")
	}
	if raw == "" {
		raw = "vi"
	}
	parts := strings.Fields(raw)
	return exec.Command(parts[0], append(parts[1:], path)...)
}

// EditBufferPath returns ~/.config/saltare/edit/doc-<slug>.md — a stable
// location (not /tmp) so a crashed editor or a failed save never loses an
// edit; callers delete the file only after a successful save.
func EditBufferPath(slug string) (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	editDir := filepath.Join(dir, "edit")
	if err := os.MkdirAll(editDir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(editDir, fmt.Sprintf("doc-%s.md", slug)), nil
}
