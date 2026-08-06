package ui

import (
	"fmt"
	"strings"
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

// webLinks builds the web app URLs sal points at. The zero value yields no
// URLs at all, so a renderer constructed without workspace context (tests, the
// docs reader before login) degrades to plain unlinked text.
type webLinks struct {
	server    string
	workspace string
}

func (w webLinks) ok() bool { return w.server != "" && w.workspace != "" }

// base is https://host/w/workspace-slug — the prefix every workspace URL shares.
func (w webLinks) base() string {
	if !w.ok() {
		return ""
	}
	return strings.TrimRight(w.server, "/") + "/w/" + w.workspace
}

// channel is a channel's web URL. Threads are first-class channels with their
// own permalink at /t/:slug; everything else lives under /channels/:slug.
func (w webLinks) channel(kind, slug string) string {
	if !w.ok() || slug == "" {
		return ""
	}
	if kind == "thread" {
		return w.base() + "/t/" + slug
	}
	return w.base() + "/channels/" + slug
}

// message is the anchored permalink for one message inside its channel.
func (w webLinks) message(channelKind, channelSlug string, messageID int64) string {
	url := w.channel(channelKind, channelSlug)
	if url == "" {
		return ""
	}
	return fmt.Sprintf("%s#message_%d", url, messageID)
}

func (w webLinks) task(slug string) string {
	if !w.ok() || slug == "" {
		return ""
	}
	return w.base() + "/tasks/" + slug
}

func (w webLinks) agent(slug string) string {
	if !w.ok() || slug == "" {
		return ""
	}
	return w.base() + "/agents/" + slug
}

// threadSlugPrefix is how the server mints a thread's slug ("thread-" + 8 bytes
// of hex), which is the only signal an [[channel:…]] reference carries about
// whether it routes to /t/ or /channels/.
const threadSlugPrefix = "thread-"

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
