package ui

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"github.com/jkthorne/saltare/cli/internal/api"
)

// embedPattern matches Saltare cross-references — [[type:slug]] inline chips
// and ![[type:slug]] block cards — which glamour passes through verbatim.
var embedPattern = regexp.MustCompile(`!?\[\[(\w+):([^\]\n]+)\]\]`)

// renderEmbedChips swaps embed syntax for compact styled chips after markdown
// rendering. A chip split across wrapped lines stays raw — acceptable.
func renderEmbedChips(s string) string {
	return embedPattern.ReplaceAllStringFunc(s, func(match string) string {
		groups := embedPattern.FindStringSubmatch(match)
		return styleEmbedChip.Render("⟨" + groups[1] + ":" + groups[2] + "⟩")
	})
}

// groupWindow matches the web feed: consecutive messages from the same sender
// within 5 minutes share one header.
const groupWindow = 5 * time.Minute

// feedGutter reserves two columns on every feed line so the selection marker
// doesn't shift the layout when it appears.
const feedGutter = "  "
const feedGutterSel = "▌ "

// msgBlock records where one message's lines live in the assembled feed, so
// selection can highlight and scroll to it.
type msgBlock struct {
	ID   int64
	Line int // first line index in the content
	Rows int // line count of this message's segment
}

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
	// Gutter (2) + glamour's own indent (2) inside the pane width.
	md, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(width-6),
	)
	if err == nil {
		r.markdown = md
	}
	r.cache = map[string]string{} // width changed — rendered lines are stale
}

// Render lays out a channel's timeline and reports each message's line span.
// selectedID > 0 marks that message with a gutter bar.
func (r *feedRenderer) Render(msgs []api.Message, selectedID int64) (string, []msgBlock) {
	var b strings.Builder
	var blocks []msgBlock
	line := 0
	var prev *api.Message

	emit := func(segment string, msgID int64) {
		gutter := feedGutter
		if msgID != 0 && msgID == selectedID {
			gutter = styleSelGutter.Render(feedGutterSel)
		}
		rows := 0
		for _, l := range strings.Split(strings.TrimRight(segment, "\n"), "\n") {
			b.WriteString(gutter + l + "\n")
			rows++
		}
		if msgID != 0 {
			blocks = append(blocks, msgBlock{ID: msgID, Line: line, Rows: rows})
		}
		line += rows
	}

	for i := range msgs {
		m := &msgs[i]
		if m.ArchivedAt != nil {
			continue
		}
		if prev == nil || !sameDay(prev.CreatedAt, m.CreatedAt) {
			emit(r.dateSeparator(m.CreatedAt), 0)
		}
		if m.IsSystemEvent() {
			emit(r.systemEvent(m), m.ID)
			prev = m
			continue
		}
		var segment strings.Builder
		if needsHeader(prev, m) {
			segment.WriteString(r.header(m))
		}
		segment.WriteString(r.body(m))
		emit(segment.String(), m.ID)
		prev = m
	}

	if b.Len() == 0 {
		return styleFeedTopic.Render("no messages yet"), nil
	}
	return b.String(), blocks
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
		out = lipgloss.NewStyle().Width(r.width-4).Render(text) + "\n"
	}
	if m.EditedAt != nil {
		out = strings.TrimRight(out, "\n") + " " + styleEditedTag.Render("(edited)") + "\n"
	}
	out = renderEmbedChips(out)
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
	pad := r.width - lipgloss.Width(label) - 4
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
