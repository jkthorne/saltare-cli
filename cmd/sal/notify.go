package main

import (
	"os/exec"
	"strconv"
	"strings"

	"github.com/jkthorne/saltare-cli/internal/watch"
	"github.com/jkthorne/saltare-cli/internal/weblink"
)

// notifier turns a notification into a desktop toast.
//
// omarchy-notification-send is preferred over bare notify-send for one reason
// that matters: it takes --exec, so the toast is clickable and lands you in the
// channel. Without it a toast tells you something happened and then makes you
// go find it.
type notifier struct {
	links weblink.Links
	// all disables the personal-only filter. Off by default: credit alerts and
	// connector trouble belong in the workspace, not on top of whatever you
	// were doing.
	all bool

	// run is the exec seam, so the routing can be tested without a desktop.
	run func(name string, args ...string) error
	// have reports whether a helper exists; likewise a seam.
	have func(name string) bool
}

func newNotifier(links weblink.Links, all bool) *notifier {
	return &notifier{
		links: links,
		all:   all,
		run:   func(name string, args ...string) error { return exec.Command(name, args...).Start() },
		have:  func(name string) bool { _, err := exec.LookPath(name); return err == nil },
	}
}

func (n *notifier) notify(note watch.Notification) {
	if !n.all && !watch.IsPersonal(note.Action) {
		return
	}
	name, args := n.command(note)
	if name == "" {
		return
	}
	_ = n.run(name, args...)
}

// command builds the toast. Returns an empty name when no notifier exists on
// the machine at all — a missing libnotify is not an error worth interrupting
// the daemon for.
func (n *notifier) command(note watch.Notification) (string, []string) {
	headline, body := phrase(note)

	if n.have("omarchy-notification-send") {
		args := []string{"--app-name", "saltare", "-u", "normal"}
		// -r replaces: five messages in one channel update one toast instead
		// of stacking five. Keyed on the channel, so two channels still get
		// two toasts.
		if id := replaceID(note); id != 0 {
			args = append(args, "-r", strconv.Itoa(id))
		}
		args = append(args, headline, body)
		if target := n.target(note); len(target) > 0 {
			args = append(args, "--exec", "sal")
			args = append(args, target...)
		}
		return "omarchy-notification-send", args
	}
	if n.have("notify-send") {
		// No --exec here; the toast informs but cannot navigate.
		return "notify-send", []string{"-a", "saltare", headline, body}
	}
	return "", nil
}

// target is the `sal open …` argv a click runs.
func (n *notifier) target(note watch.Notification) []string {
	switch {
	case note.ChannelSlug != "":
		return []string{"open", "channel", note.ChannelSlug}
	case note.TaskSlug != "":
		return []string{"open", "task", note.TaskSlug}
	default:
		return nil
	}
}

// phrase reads as the web app's own notification does: actor, then the
// server's wording for the action, then where.
func phrase(note watch.Notification) (headline, body string) {
	who := note.Actor
	if who == "" {
		who = "Saltare"
	}
	headline = who
	if note.ChannelSlug != "" {
		headline += " · #" + note.ChannelSlug
	}

	body = note.Preview
	if body == "" {
		body = note.TaskTitle
	}
	if body == "" {
		body = note.Description
	} else if note.Description != "" && note.Action != "mentioned" && note.Action != "new_message" {
		// A mention's preview speaks for itself; a task assignment needs to
		// say what happened to it.
		body = note.Description + ": " + body
	}
	return headline, body
}

// replaceID collapses a burst in one channel onto one toast. Derived from the
// channel slug rather than the notification id, which is the whole point —
// a fresh id per message is exactly what stacks five toasts.
func replaceID(note watch.Notification) int {
	key := note.ChannelSlug
	if key == "" {
		key = note.TaskSlug
	}
	if key == "" {
		return 0
	}
	// FNV-1a, folded into the positive half of int32 so it survives being
	// passed as a notification id.
	const offset, prime = 2166136261, 16777619
	hash := uint32(offset)
	for _, b := range []byte(strings.ToLower(key)) {
		hash ^= uint32(b)
		hash *= prime
	}
	return int(hash&0x7fffffff)%1_000_000 + 1
}
