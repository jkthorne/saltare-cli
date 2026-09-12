package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestEditorCommandPrefersVisualThenEditorThenVi(t *testing.T) {
	tests := []struct {
		name       string
		visual     string
		editor     string
		wantBinary string
		wantArgs   []string
	}{
		{name: "visual wins", visual: "code -w", editor: "nano", wantBinary: "code", wantArgs: []string{"-w"}},
		{name: "editor when visual is unset", editor: "nano", wantBinary: "nano"},
		{name: "vi when neither is set", wantBinary: "vi"},
		{name: "flags split off the binary", editor: "emacsclient -nw -a ''", wantBinary: "emacsclient", wantArgs: []string{"-nw", "-a", "''"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("VISUAL", tc.visual)
			t.Setenv("EDITOR", tc.editor)

			cmd := EditorCommand("/tmp/doc.md")

			// exec.Command resolves the binary against PATH, so compare the
			// base name rather than the absolute path it may have found.
			if got := filepath.Base(cmd.Path); got != tc.wantBinary {
				t.Errorf("binary = %q, want %q", got, tc.wantBinary)
			}
			// Args[0] is the command itself; the file must come last.
			args := cmd.Args[1:]
			if len(args) == 0 || args[len(args)-1] != "/tmp/doc.md" {
				t.Fatalf("file must be the last argument, got %v", cmd.Args)
			}
			if got := strings.Join(args[:len(args)-1], " "); got != strings.Join(tc.wantArgs, " ") {
				t.Errorf("flags = %q, want %q", got, strings.Join(tc.wantArgs, " "))
			}
		})
	}
}

// The edit buffer lives under the config dir, not /tmp, so a crashed editor
// or a failed save never loses an edit to a reboot's tmp sweep.
func TestEditBufferPathIsAStableSlugNamedFileUnderTheConfigDir(t *testing.T) {
	home := withHome(t)

	path, err := EditBufferPath("release-notes")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".config", "saltare", "edit", "doc-release-notes.md")
	if path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}

	// Stable across calls — the recovery story depends on it.
	again, err := EditBufferPath("release-notes")
	if err != nil {
		t.Fatal(err)
	}
	if again != path {
		t.Fatalf("path changed between calls: %q then %q", path, again)
	}
}
