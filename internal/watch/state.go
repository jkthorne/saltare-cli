// Package watch holds the state document `sal watch` publishes and the pure
// reducer that builds it. The document is the seam between the daemon and
// every reader: the Omarchy bar widget, `sal status`, waybar, a shell prompt.
//
// Nothing in this package talks to the network or the clock. The daemon
// gathers inputs and calls Reduce; keeping that boundary is what makes the
// counts testable, and the counts are the whole product.
package watch

import (
	"os"
	"path/filepath"
	"time"
)

// Schema is the document version. Readers refuse a version they do not know
// rather than half-parse it. Adding a field is free; removing or retyping one
// is a bump.
const Schema = 1

// Session states, which exist to keep a reader from misdiagnosing a failure.
//
// The line between the last two is whether the server answered. A plan limit,
// a revoked scope and a 500 are all answers: the network is fine and retrying
// changes nothing. Calling those "unreachable" sends someone to debug their
// wifi, which is how this constant came to exist — the first real run of the
// daemon hit a monthly request cap and the widget blamed the connection.
const (
	SessionOK          = "ok"
	SessionLoggedOut   = "logged-out"
	SessionUnreachable = "unreachable" // nothing answered: DNS, refused, timeout
	SessionBlocked     = "blocked"     // the server answered and said no
)

// Caps. Totals stay exact above them — a popup cannot show more than this, and
// an unbounded file is an unbounded re-parse inside the shell process every
// time a message arrives.
const (
	MaxChannels      = 20
	MaxNotifications = 20
	MaxTasks         = 10
)

// HeartbeatSec is how often the daemon touches the file when nothing changed,
// so a reader can tell "quiet" from "dead" by mtime alone.
const HeartbeatSec = 30

type State struct {
	Schema       int       `json:"schema"`
	UpdatedAt    time.Time `json:"updated_at"`
	HeartbeatSec int       `json:"heartbeat_sec"`
	// Session is the account; Live is the socket. A dropped socket is
	// {state: ok, live: false} — the counts are stale, not wrong, and a bar
	// that blanked them would be telling you something false.
	Session       string         `json:"state"`
	Live          bool           `json:"live"`
	Server        string         `json:"server"`
	Workspace     Workspace      `json:"workspace"`
	User          User           `json:"user"`
	Totals        Totals         `json:"totals"`
	Channels      []Channel      `json:"channels"`
	Notifications []Notification `json:"notifications"`
	Work          Work           `json:"work"`
	Error         string         `json:"error"`
}

type Workspace struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}

type User struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// Totals are exact. The lists below them are capped; these are not, so a bar
// badge never lies because a workspace got busy.
type Totals struct {
	Unread        int `json:"unread"`
	Mentions      int `json:"mentions"`
	Notifications int `json:"notifications"`
	Overdue       int `json:"overdue"`
	DueToday      int `json:"due_today"`
}

// Channel carries no "#" on its title: the prefix is a rendering decision, and
// a DM's title is a person's name rather than a channel's.
type Channel struct {
	Slug      string `json:"slug"`
	Title     string `json:"title"`
	Kind      string `json:"kind"`
	Unread    int    `json:"unread"`
	Mentioned bool   `json:"mentioned"`
}

// Notification is the toast source. Text is rendered here rather than on the
// server because the serializer deliberately sends `action` and lets clients
// phrase it — so Describe is a port of the server's own wording.
type Notification struct {
	ID          int64     `json:"id"`
	Action      string    `json:"action"`
	Actor       string    `json:"actor"`
	Description string    `json:"description"`
	ChannelSlug string    `json:"channel_slug"`
	Preview     string    `json:"preview"`
	TaskSlug    string    `json:"task_slug"`
	TaskTitle   string    `json:"task_title"`
	CreatedAt   time.Time `json:"created_at"`
}

type Work struct {
	Overdue []Task `json:"overdue"`
	Today   []Task `json:"today"`
}

type Task struct {
	Slug    string `json:"slug"`
	Title   string `json:"title"`
	DueDate string `json:"due_date"`
	State   string `json:"state"`
}

// StatePath resolves the document's location. XDG_STATE_HOME wins, then
// ~/.local/state — the same convention omarchy.agents uses for its own
// records, which is what a reader will guess.
func StatePath() (string, error) {
	if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
		return filepath.Join(dir, "saltare", "watch.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "saltare", "watch.json"), nil
}
