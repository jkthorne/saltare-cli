package config

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// partFiles lists the temp siblings SafeWriteFile creates, so a test can prove
// none survived.
func partFiles(t *testing.T, dir string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "*.part-*"))
	if err != nil {
		t.Fatal(err)
	}
	return matches
}

func TestSafeWriteFileWritesTheWholeStream(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "report.pdf")
	body := strings.Repeat("saltare", 5000)

	n, err := SafeWriteFile(target, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if n != int64(len(body)) {
		t.Fatalf("wrote %d bytes, want %d", n, len(body))
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != body {
		t.Fatalf("content mismatch: got %d bytes, want %d", len(got), len(body))
	}
	if left := partFiles(t, dir); len(left) != 0 {
		t.Fatalf("temp files left behind: %v", left)
	}
}

// The whole point of temp+rename: an interrupted download must not leave a
// truncated file sitting under the real name, and must not clobber whatever
// was already there.
func TestSafeWriteFileLeavesNothingBehindOnAFailedStream(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "report.pdf")
	existing := []byte("the previous, complete download")
	if err := os.WriteFile(target, existing, 0o600); err != nil {
		t.Fatal(err)
	}

	broken := io.MultiReader(
		bytes.NewReader([]byte("first half")),
		errReader{errors.New("connection reset")},
	)

	n, err := SafeWriteFile(target, broken)
	if err == nil {
		t.Fatal("want the read error to surface")
	}
	if n != 0 {
		t.Fatalf("a failed write must report 0 bytes, got %d", n)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, existing) {
		t.Fatalf("the existing file was damaged: %q", got)
	}
	if left := partFiles(t, dir); len(left) != 0 {
		t.Fatalf("temp files left behind: %v", left)
	}
}

func TestSafeWriteFileReplacesAnExistingFileWhole(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(target, []byte("a much longer previous version"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := SafeWriteFile(target, strings.NewReader("new")); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Fatalf("content = %q, want %q — the old bytes must not survive", got, "new")
	}
}

func TestSafeWriteFileFailsWhenTheDirectoryIsMissing(t *testing.T) {
	target := filepath.Join(t.TempDir(), "no-such-dir", "file.txt")

	if _, err := SafeWriteFile(target, strings.NewReader("x")); err == nil {
		t.Fatal("want an error when the target directory does not exist")
	}
}

type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }
