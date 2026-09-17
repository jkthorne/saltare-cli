// Package weblink builds the saltare web app URLs sal points at. It lives on
// its own because two unrelated callers need the same knowledge: the TUI, for
// OSC-8 hyperlinks and copied permalinks, and `sal open`, for handing a
// notification's target to a browser. The thread-versus-channel rule below is
// exactly the kind of fact that goes wrong when it is written twice.
package weblink

import (
	"fmt"
	"strings"
)

// ThreadSlugPrefix is how the server mints a thread's slug ("thread-" plus 8
// bytes of hex), which is the only signal a bare slug carries about whether it
// routes to /t/ or /channels/.
const ThreadSlugPrefix = "thread-"

// Links is the zero-value-safe builder: without a server and a workspace it
// yields no URLs at all, so a caller with no session degrades to plain text
// rather than to a broken link.
type Links struct {
	Server    string
	Workspace string
}

func (l Links) OK() bool { return l.Server != "" && l.Workspace != "" }

// Base is https://host/w/workspace-slug — the prefix every workspace URL shares.
func (l Links) Base() string {
	if !l.OK() {
		return ""
	}
	return strings.TrimRight(l.Server, "/") + "/w/" + l.Workspace
}

// Channel is a channel's web URL. Threads are first-class channels with their
// own permalink at /t/:slug; everything else lives under /channels/:slug.
func (l Links) Channel(kind, slug string) string {
	if !l.OK() || slug == "" {
		return ""
	}
	if kind == "thread" {
		return l.Base() + "/t/" + slug
	}
	return l.Base() + "/channels/" + slug
}

// ChannelBySlug is Channel for a caller holding a slug and no kind — a
// notification payload, say. The slug's own prefix is the only hint there is.
func (l Links) ChannelBySlug(slug string) string {
	kind := ""
	if strings.HasPrefix(slug, ThreadSlugPrefix) {
		kind = "thread"
	}
	return l.Channel(kind, slug)
}

// Message is the anchored permalink for one message inside its channel.
func (l Links) Message(channelKind, channelSlug string, messageID int64) string {
	url := l.Channel(channelKind, channelSlug)
	if url == "" {
		return ""
	}
	return fmt.Sprintf("%s#message_%d", url, messageID)
}

func (l Links) Task(slug string) string {
	if !l.OK() || slug == "" {
		return ""
	}
	return l.Base() + "/tasks/" + slug
}

func (l Links) Agent(slug string) string {
	if !l.OK() || slug == "" {
		return ""
	}
	return l.Base() + "/agents/" + slug
}
