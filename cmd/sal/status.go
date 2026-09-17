package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/jkthorne/saltare-cli/internal/watch"
)

// followInterval is a stat() every half second, which is cheaper than it
// sounds and more correct than watching the path with inotify: the daemon
// publishes through a temp file and a rename, so a watch registered on the
// inode goes deaf after the first write.
const followInterval = 500 * time.Millisecond

func runStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	statePath := fs.String("state", "", "state file (default $XDG_STATE_HOME/saltare/watch.json)")
	asJSON := fs.Bool("json", false, "print the whole document")
	waybar := fs.Bool("waybar", false, "print waybar's custom-module JSON")
	follow := fs.Bool("follow", false, "reprint whenever the document changes")
	if err := fs.Parse(args); err != nil {
		return err
	}

	path := *statePath
	if path == "" {
		resolved, err := watch.StatePath()
		if err != nil {
			return err
		}
		path = resolved
	}

	render := func() error { return printStatus(os.Stdout, path, *asJSON, *waybar) }
	if !*follow {
		return render()
	}

	// Print once immediately so a bar has something before the first change,
	// and never fail the loop on a transient read — the file is being renamed
	// into place underneath us by design.
	_ = render()
	var last string
	for {
		if stamp := fingerprintOf(path); stamp != last {
			last = stamp
			_ = render()
		}
		time.Sleep(followInterval)
	}
}

func fingerprintOf(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return "missing"
	}
	return fmt.Sprintf("%d:%d", info.ModTime().UnixNano(), info.Size())
}

func printStatus(out io.Writer, path string, asJSON, waybar bool) error {
	state, err := watch.Read(path)
	now := time.Now()

	if err != nil {
		switch {
		case waybar:
			fmt.Fprintln(out, waybarLine("", "setup", "Saltare: the watcher isn't running.\nsal watch --install-service"))
			return nil
		case asJSON:
			return json.NewEncoder(out).Encode(map[string]any{"state": "setup", "error": err.Error()})
		}
		return fmt.Errorf("no state file at %s — start the watcher with `sal watch --install-service`", path)
	}

	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{
			"document":    state,
			"age_seconds": int(state.Age(now).Seconds()),
			"stale":       state.Stale(now),
			"class":       statusClass(state, now),
		})
	}
	if waybar {
		fmt.Fprintln(out, waybarLine(badge(state), statusClass(state, now), tooltip(state, now)))
		return nil
	}

	fmt.Fprintln(out, line(state, now))
	if state.Session == watch.SessionLoggedOut {
		return fmt.Errorf("signed out — run `sal login`")
	}
	return nil
}

// statusClass is the one word a bar styles on. Precedence is deliberate:
// a signed-out session outranks a stale file, because restarting a watcher
// that has nothing to sign in with fixes nothing.
func statusClass(s *watch.State, now time.Time) string {
	switch {
	case s.Session == watch.SessionLoggedOut:
		return "signin"
	case s.Stale(now):
		return "stopped"
	case s.Session == watch.SessionUnreachable || !s.Live:
		return "offline"
	case s.Totals.Mentions > 0:
		return "mention"
	default:
		return "ok"
	}
}

// badge is what fits in a bar: the unread count, or nothing at all when there
// is nothing waiting.
func badge(s *watch.State) string {
	if s.Totals.Unread == 0 {
		return ""
	}
	return fmt.Sprintf("%d", s.Totals.Unread)
}

func line(s *watch.State, now time.Time) string {
	parts := []string{}
	if s.Workspace.Name != "" {
		parts = append(parts, s.Workspace.Name)
	}
	switch statusClass(s, now) {
	case "signin":
		return strings.Join(append(parts, "signed out — run `sal login`"), " · ")
	case "stopped":
		parts = append(parts, "watcher stopped")
	case "offline":
		parts = append(parts, "offline")
	}

	if s.Totals.Unread > 0 {
		parts = append(parts, fmt.Sprintf("%d unread", s.Totals.Unread))
	}
	if s.Totals.Mentions > 0 {
		parts = append(parts, fmt.Sprintf("%d mention%s", s.Totals.Mentions, plural(s.Totals.Mentions)))
	}
	if s.Totals.Overdue > 0 {
		parts = append(parts, fmt.Sprintf("%d overdue", s.Totals.Overdue))
	}
	if s.Totals.DueToday > 0 {
		parts = append(parts, fmt.Sprintf("%d due today", s.Totals.DueToday))
	}
	if len(parts) == 1 || (len(parts) == 0) {
		parts = append(parts, "all clear")
	}
	if age := s.Age(now); age > time.Duration(s.HeartbeatSec)*time.Second {
		parts = append(parts, ago(age))
	}
	return strings.Join(parts, " · ")
}

func tooltip(s *watch.State, now time.Time) string {
	lines := []string{line(s, now)}
	for _, n := range s.Notifications {
		who := n.Actor
		if who == "" {
			who = "Someone"
		}
		where := n.ChannelSlug
		if where != "" {
			where = " in #" + where
		}
		lines = append(lines, fmt.Sprintf("%s %s%s", who, n.Description, where))
		if len(lines) > 6 {
			break
		}
	}
	for _, t := range s.Work.Overdue {
		lines = append(lines, "overdue: "+t.Title)
		if len(lines) > 9 {
			break
		}
	}
	return strings.Join(lines, "\n")
}

func waybarLine(text, class, tooltip string) string {
	body, err := json.Marshal(map[string]string{
		"text": text, "alt": class, "class": class, "tooltip": tooltip,
	})
	if err != nil {
		return `{"text":"","class":"error"}`
	}
	return string(body)
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
