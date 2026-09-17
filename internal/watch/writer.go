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
	path      string
	last      []byte    // content fingerprint, UpdatedAt excluded
	lastWrite time.Time // when the document on disk last claimed to be true
}

func NewWriter(path string) *Writer { return &Writer{path: path} }

func (w *Writer) Path() string { return w.path }

// Publish writes s when its content differs from the last publish, and
// otherwise at most once per heartbeat.
//
// The heartbeat has to be a real write. An earlier version skipped it and moved
// mtime instead, on the theory that a rewrite of identical bytes wakes every
// FileView in the shell for nothing — but staleness is judged on UpdatedAt
// *inside* the document, so touching mtime left every reader looking at an
// hour-old timestamp and calling a healthy daemon stopped. A liveness signal
// nobody reads is not a liveness signal.
//
// Two writes a minute on an idle workspace is the price, and it is the right
// one: the alternative is a bar that says "watcher stopped" whenever nothing is
// happening, which is most of the time.
//
// Reports whether the *content* changed, so a caller can tell news from a
// heartbeat.
func (w *Writer) Publish(s State) (bool, error) {
	fingerprint, err := fingerprint(s)
	if err != nil {
		return false, err
	}
	unchanged := w.last != nil && bytes.Equal(w.last, fingerprint)
	// One stat, so a document someone deleted comes straight back rather than
	// waiting out the heartbeat. Readers treat a missing file as "nothing is
	// installed", which is a much worse thing to say than a stale count.
	if unchanged && s.UpdatedAt.Sub(w.lastWrite) < HeartbeatSec*time.Second && w.onDisk() {
		return false, nil
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
	w.lastWrite = s.UpdatedAt
	return !unchanged, nil
}

// onDisk reports whether the published document is still where it was left.
func (w *Writer) onDisk() bool {
	_, err := os.Stat(w.path)
	return err == nil
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
