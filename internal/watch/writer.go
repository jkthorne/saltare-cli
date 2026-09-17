package watch

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/jkthorne/saltare-cli/internal/config"
)

// Writer publishes the state document. It is the only writer of the file, and
// it writes through config.SafeWriteFile (temp sibling + rename) because the
// readers watch with inotify: a plain truncate-and-write hands a half-written
// file to the bar, which then draws a parse error.
type Writer struct {
	path string
	last []byte // content fingerprint, UpdatedAt excluded
}

func NewWriter(path string) *Writer { return &Writer{path: path} }

func (w *Writer) Path() string { return w.path }

// Publish writes s when its content differs from the last publish, and
// otherwise only moves mtime forward. The distinction matters: every write
// wakes every FileView in the shell, and a 30-second heartbeat that rewrote an
// identical file would repaint the bar twice a minute for nothing.
//
// Reports whether the content changed.
func (w *Writer) Publish(s State) (bool, error) {
	fingerprint, err := fingerprint(s)
	if err != nil {
		return false, err
	}
	if w.last != nil && bytes.Equal(w.last, fingerprint) {
		return false, w.touch(s.UpdatedAt)
	}

	body, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return false, err
	}
	body = append(body, '\n')
	if err := os.MkdirAll(filepath.Dir(w.path), 0o700); err != nil {
		return false, err
	}
	if _, err := config.SafeWriteFile(w.path, bytes.NewReader(body)); err != nil {
		return false, err
	}
	// SafeWriteFile's temp file is created with the default mask; the document
	// carries message previews and task titles, so it is chmod'd down after
	// the rename rather than left readable to the rest of the machine.
	if err := os.Chmod(w.path, 0o600); err != nil {
		return false, err
	}
	w.last = fingerprint
	return true, nil
}

// touch moves mtime without rewriting. A reader tells "quiet" from "dead" by
// age, so a heartbeat has to be visible even when nothing changed.
func (w *Writer) touch(at time.Time) error {
	if err := os.Chtimes(w.path, at, at); err != nil {
		if os.IsNotExist(err) {
			w.last = nil // the file was removed under us; the next Publish rewrites it
			return nil
		}
		return err
	}
	return nil
}

// fingerprint is the document with UpdatedAt zeroed — otherwise every
// heartbeat would compare as a change, which is exactly what Publish exists
// to avoid.
func fingerprint(s State) ([]byte, error) {
	s.UpdatedAt = time.Time{}
	return json.Marshal(s)
}

// Read loads a published document. Readers (`sal status`, and anything else
// that wants the counts without a session) use this; it needs no network, no
// token, and no server.
func Read(path string) (*State, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(body, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// Age is how long ago the document was published, by its own timestamp rather
// than by mtime — a file copied between machines keeps its claim about when it
// was true.
func (s *State) Age(now time.Time) time.Duration { return now.Sub(s.UpdatedAt) }

// Stale reports whether the daemon has missed enough heartbeats to assume it
// is gone. Three of them: one missed beat is a busy machine, three is a
// process that stopped.
func (s *State) Stale(now time.Time) bool {
	beat := s.HeartbeatSec
	if beat <= 0 {
		beat = HeartbeatSec
	}
	return s.Age(now) > time.Duration(3*beat)*time.Second
}
