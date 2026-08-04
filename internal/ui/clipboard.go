package ui

import (
	"fmt"
	"os"
	"strings"

	osc52 "github.com/aymanbagabas/go-osc52/v2"
)

// copyToClipboard writes an OSC 52 sequence so the copy works over SSH and
// inside tmux. It goes to stderr: stdout belongs to the bubbletea renderer,
// and terminals process the sequence from either stream.
func copyToClipboard(text string) error {
	seq := osc52.New(text)
	if os.Getenv("TMUX") != "" {
		seq = seq.Tmux()
	}
	_, err := seq.WriteTo(os.Stderr)
	return err
}

// messagePermalink builds the canonical web URL for a message: thread
// channels live at /t/:slug, everything else at /channels/:slug.
func (m *Model) messagePermalink(channelKind, channelSlug string, messageID int64) string {
	base := strings.TrimRight(m.cfg.ServerURL, "/") + "/w/" + m.cfg.WorkspaceSlug
	path := "/channels/" + channelSlug
	if channelKind == "thread" {
		path = "/t/" + channelSlug
	}
	return fmt.Sprintf("%s%s#message_%d", base, path, messageID)
}

// copyRowReference copies whatever travels best for a search row: messages
// get their permalink URL, tasks and documents their [[embed]] reference
// (documents have no stable web URL — they live at data-tree paths).
func (m *Model) copyRowReference(row searchRow) (string, error) {
	switch {
	case row.message != nil:
		url := m.messagePermalink(row.message.Channel.Kind, row.message.Channel.Slug, row.message.ID)
		return "permalink copied", copyToClipboard(url)
	case row.task != nil:
		return "embed copied", copyToClipboard("[[task:" + row.task.Slug + "]]")
	case row.document != nil:
		return "embed copied", copyToClipboard("[[doc:" + row.document.Slug + "]]")
	case row.upload != nil:
		return "embed copied", copyToClipboard("[[upload:" + row.upload.Slug + "]]")
	}
	return "", nil
}
