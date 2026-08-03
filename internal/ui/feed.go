package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"github.com/jkthorne/saltare/cli/internal/api"
)

// groupWindow matches the web feed: consecutive messages from the same sender
// within 5 minutes share one header.
const groupWindow = 5 * time.Minute

type feedRenderer struct {
	width    int
	markdown *glamour.TermRenderer
	cache    map[string]string // key: id:updated_at → rendered body
}

func newFeedRenderer(width int) *feedRenderer {
	r := &feedRenderer{cache: map[string]string{}}
	r.Resize(width)
	return r
}

func (r *feedRenderer) Resize(width int) {
	if width < 20 {
		width = 20
	}
	if width == r.width && r.markdown != nil {
		return
	}
	r.width = width
	// Glamour's dark style indents by 2; wrap inside that so lines fit.
	md, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(width-4),
	)
	if err == nil {
		r.markdown = md
	}
	r.cache = map[string]string{} // width changed — rendered lines are stale
}

// Render lays out a channel's full timeline for the viewport.
func (r *feedRenderer) Render(msgs []api.Message) string {
	var b strings.Builder
	var prev *api.Message
	for i := range msgs {
		m := &msgs[i]
		if m.ArchivedAt != nil {
			continue
		}
		if prev == nil || !sameDay(prev.CreatedAt, m.CreatedAt) {
			b.WriteString(r.dateSeparator(m.CreatedAt))
		}
		if m.IsSystemEvent() {
			b.WriteString(r.systemEvent(m))
			prev = m
			continue
		}
		if needsHeader(prev, m) {
			b.WriteString(r.header(m))
		}
		b.WriteString(r.body(m))
		prev = m
	}
	if b.Len() == 0 {
		return styleFeedTopic.Render("no messages yet")
	}
	return b.String()
}

func needsHeader(prev, m *api.Message) bool {
	if prev == nil || prev.IsSystemEvent() {
		return true
	}
	if prev.Sender.Type != m.Sender.Type || prev.Sender.ID != m.Sender.ID {
		return true
	}
	return m.CreatedAt.Sub(prev.CreatedAt) > groupWindow
}

func (r *feedRenderer) header(m *api.Message) string {
	nameStyle := styleSenderUser
	if m.Sender.Type == "Agent" {
		nameStyle = styleSenderAgent
	}
	name := m.Sender.Name
	if name == "" {
		name = fmt.Sprintf("%s#%d", strings.ToLower(m.Sender.Type), m.Sender.ID)
	}
	ts := styleTimestamp.Render(m.CreatedAt.Local().Format("15:04"))
	return "\n" + nameStyle.Render(name) + "  " + ts + "\n"
}

func (r *feedRenderer) body(m *api.Message) string {
	key := fmt.Sprintf("%d:%d", m.ID, m.UpdatedAt.UnixNano())
	if cached, ok := r.cache[key]; ok {
		return cached
	}

	text := strings.TrimRight(m.Body, "\n")
	var out string
	if r.markdown != nil {
		if rendered, err := r.markdown.Render(text); err == nil {
			out = strings.Trim(rendered, "\n") + "\n"
		}
	}
	if out == "" {
		out = lipgloss.NewStyle().Width(r.width-2).Render(text) + "\n"
	}
	if m.EditedAt != nil {
		out = strings.TrimRight(out, "\n") + " " + styleEditedTag.Render("(edited)") + "\n"
	}
	r.cache[key] = out
	return out
}

func (r *feedRenderer) systemEvent(m *api.Message) string {
	event := strings.ReplaceAll(*m.SystemEvent, "_", " ")
	who := m.Sender.Name
	if who != "" {
		who += " "
	}
	line := fmt.Sprintf("· %s%s  %s", who, event, m.CreatedAt.Local().Format("15:04"))
	return styleSystemEvent.Render(line) + "\n"
}

func (r *feedRenderer) dateSeparator(t time.Time) string {
	label := " " + t.Local().Format("Mon, Jan 2") + " "
	pad := r.width - lipgloss.Width(label) - 2
	if pad < 2 {
		pad = 2
	}
	left := pad / 2
	right := pad - left
	return "\n" +
		styleDateRule.Render(strings.Repeat("─", left)) +
		styleDateLabel.Render(label) +
		styleDateRule.Render(strings.Repeat("─", right)) + "\n"
}

func sameDay(a, b time.Time) bool {
	al, bl := a.Local(), b.Local()
	return al.Year() == bl.Year() && al.YearDay() == bl.YearDay()
}
