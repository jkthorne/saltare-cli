package ui

import (
	"strings"

	"github.com/jkthorne/saltare-cli/internal/weblink"
)

// osc8 wraps label in a terminal hyperlink (OSC 8), so a ctrl/cmd-click on it
// opens the URL in a browser. The terminal does the work — no mouse tracking
// is involved, so this costs nothing and never interferes with selection.
// Terminals that don't implement OSC 8 parse the sequence as an unknown OS
// command and discard it, printing the label bare; no capability probe needed.
//
// lipgloss.Width is OSC-aware, so a linked label measures as its text — but
// truncate is not (its fallback slices runes and would cut a sequence in half).
// Link outermost: style and truncate first, wrap last.
func osc8(url, label string) string {
	if url == "" {
		return label
	}
	return "\x1b]8;;" + url + "\x1b\\" + label + "\x1b]8;;\x1b\\"
}

// webLinks is the TUI's view of weblink.Links: the URL knowledge lives in
// that package because `sal open` needs it too, and this keeps the renderers
// reading the same way they always have.
type webLinks struct {
	server    string
	workspace string
}

func (w webLinks) links() weblink.Links {
	return weblink.Links{Server: w.server, Workspace: w.workspace}
}

func (w webLinks) ok() bool     { return w.links().OK() }
func (w webLinks) base() string { return w.links().Base() }

func (w webLinks) channel(kind, slug string) string { return w.links().Channel(kind, slug) }

func (w webLinks) message(channelKind, channelSlug string, messageID int64) string {
	return w.links().Message(channelKind, channelSlug, messageID)
}

func (w webLinks) task(slug string) string  { return w.links().Task(slug) }
func (w webLinks) agent(slug string) string { return w.links().Agent(slug) }

// threadSlugPrefix is how the server mints a thread's slug, which is the only
// signal an [[channel:…]] reference carries about whether it routes to /t/ or
// /channels/.
const threadSlugPrefix = weblink.ThreadSlugPrefix

// embed resolves the web URL an [[type:slug]] reference points at, or "" when
// there isn't one to build. Only tasks, channels, and agents have a stable
// slug-addressed page: documents, uploads, and databases are reached through
// data-tree paths sal doesn't carry, and [[msg:id]] has no channel to anchor a
// permalink to. Those stay plain chips rather than dead links.
func (w webLinks) embed(ref embedRef) string {
	switch ref.kind {
	case "task":
		return w.task(ref.ref)
	case "agent":
		return w.agent(ref.ref)
	case "channel":
		kind := ""
		if strings.HasPrefix(ref.ref, threadSlugPrefix) {
			kind = "thread"
		}
		return w.channel(kind, ref.ref)
	}
	return ""
}
