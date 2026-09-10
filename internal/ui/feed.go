package ui

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"github.com/jkthorne/saltare-cli/internal/api"
)

// embedPattern matches Saltare cross-references — [[type:slug]] inline chips and
// ![[type:slug]] block cards. It only matches source markdown: glamour does not
// pass the syntax through verbatim (see tokenizeEmbeds).
var embedPattern = regexp.MustCompile(`!?\[\[(\w+):([^\]\n]+)\]\]`)

// embedRef is one [[type:slug]] reference; msg refs carry an integer id.
type embedRef struct {
	kind string
	ref  string
}

// extractEmbeds pulls a message's references in order, deduped. The server
// strips identifier whitespace (EmbedPreprocessor) — match that.
func extractEmbeds(body string) []embedRef {
	matches := embedPattern.FindAllStringSubmatch(body, -1)
	var refs []embedRef
	seen := map[string]bool{}
	for _, groups := range matches {
		ref := embedRef{kind: groups[1], ref: strings.TrimSpace(groups[2])}
		key := ref.kind + ":" + ref.ref
		if ref.ref == "" || seen[key] {
			continue
		}
		seen[key] = true
		refs = append(refs, ref)
	}
	return refs
}

// Embed chips are a two-phase substitution, mirroring the server's
// EmbedPreprocessor: references become inert tokens before markdown rendering and
// chips after. Matching the raw [[type:slug]] syntax in rendered output does not
// work — glamour colours per token and splits on punctuation, so "[[" comes back
// as "[" ESC-codes "[", and the pattern never fires. A marker of nothing but
// letters and digits survives paragraphs, wrapping, lists, headings, and inline
// code intact.
var embedTokenPattern = regexp.MustCompile(`zzsalembedz(\d+)z`)

func embedToken(i int) string { return fmt.Sprintf("zzsalembedz%dz", i) }

// tokenizeEmbeds swaps every reference for a marker and returns them in the order
// they appear (repeats included — each occurrence gets its own marker).
func tokenizeEmbeds(s string) (string, []embedRef) {
	var refs []embedRef
	out := embedPattern.ReplaceAllStringFunc(s, func(match string) string {
		groups := embedPattern.FindStringSubmatch(match)
		refs = append(refs, embedRef{kind: groups[1], ref: strings.TrimSpace(groups[2])})
		return embedToken(len(refs) - 1)
	})
	return out, refs
}

// restoreEmbedChips swaps the markers back for styled chips, hyperlinked where the
// reference resolves to a web URL (see webLinks.embed). A marker with no matching
// reference — a user who typed one literally — is left exactly as it was.
func restoreEmbedChips(s string, refs []embedRef, links webLinks) string {
	if len(refs) == 0 {
		return s
	}
	return embedTokenPattern.ReplaceAllStringFunc(s, func(match string) string {
		idx, err := strconv.Atoi(embedTokenPattern.FindStringSubmatch(match)[1])
		if err != nil || idx < 0 || idx >= len(refs) {
			return match
		}
		ref := refs[idx]
		chip := styleEmbedChip.Render("⟨" + ref.kind + ":" + ref.ref + "⟩")
		return osc8(links.embed(ref), chip)
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
	links    webLinks
	markdown *glamour.TermRenderer
	cache    map[string]string // key: id:updated_at → rendered body
}

func newFeedRenderer(width int, links webLinks) *feedRenderer {
	r := &feedRenderer{links: links, cache: map[string]string{}}
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
// selectedID > 0 marks that message with a gutter bar; unreadBeforeID > 0
// draws the new-messages rule above that message. channel may be nil — it only
// supplies the slug each message's timestamp hyperlinks to.
func (r *feedRenderer) Render(msgs []api.Message, channel *api.Channel, selectedID, unreadBeforeID int64) (string, []msgBlock) {
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
		if m.ID == unreadBeforeID {
			emit(r.unreadSeparator(), 0)
		}
		if m.IsSystemEvent() {
			emit(r.systemEvent(m), m.ID)
			prev = m
			continue
		}
		var segment strings.Builder
		if needsHeader(prev, m) {
			segment.WriteString(r.header(m, channel))
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

func (r *feedRenderer) header(m *api.Message, channel *api.Channel) string {
	nameStyle := styleSenderUser
	if m.Sender.Type == "Agent" {
		nameStyle = styleSenderAgent
	}
	name := m.Sender.Name
	if name == "" {
		name = fmt.Sprintf("%s#%d", strings.ToLower(m.Sender.Type), m.Sender.ID)
	}
	ts := styleTimestamp.Render(m.CreatedAt.Local().Format("15:04"))
	if channel != nil {
		// The timestamp opens the message on the web — the same URL `y` copies.
		// Wrapped after styling so the label keeps its rendered width.
		ts = osc8(r.links.message(channel.Kind, channel.Slug, m.ID), ts)
	}
	return "\n" + nameStyle.Render(name) + "  " + ts + "\n"
}

func (r *feedRenderer) body(m *api.Message) string {
	key := fmt.Sprintf("%d:%d", m.ID, m.UpdatedAt.UnixNano())
	if cached, ok := r.cache[key]; ok {
		return cached
	}

	text, refs := tokenizeEmbeds(strings.TrimRight(m.Body, "\n"))
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
	out = restoreEmbedChips(out, refs, r.links)
	r.cache[key] = out
	return out
}

func (r *feedRenderer) systemEvent(m *api.Message) string {
	event := strings.ReplaceAll(*m.SystemEvent, "_", " ")
	who := m.Sender.Name
	if who != "" {
		who += " "
	}
	line := fmt.Sprintf("· %s%s%s  %s", who, event, eventContext(m.Metadata), m.CreatedAt.Local().Format("15:04"))
	return styleSystemEvent.Render(line) + "\n"
}

// eventContext compacts system-event metadata: state/priority changes show
// "from → to", assignments show who.
func eventContext(metadata map[string]any) string {
	if metadata == nil {
		return ""
	}
	from, fromOK := metadata["from"].(string)
	to, toOK := metadata["to"].(string)
	if fromOK && toOK {
		return fmt.Sprintf(" %s → %s", from, to)
	}
	if name, ok := metadata["assignee_name"].(string); ok && name != "" {
		return " → " + name
	}
	return ""
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

// unreadSeparator is the "── NEW ──" rule above the first message that
// arrived after the caller's read cursor.
func (r *feedRenderer) unreadSeparator() string {
	label := " NEW "
	pad := r.width - lipgloss.Width(label) - 4
	if pad < 2 {
		pad = 2
	}
	left := pad / 2
	right := pad - left
	return styleUnreadRule.Render(strings.Repeat("─", left)) +
		styleUnreadLabel.Render(label) +
		styleUnreadRule.Render(strings.Repeat("─", right)) + "\n"
}

func sameDay(a, b time.Time) bool {
	al, bl := a.Local(), b.Local()
	return al.Year() == bl.Year() && al.YearDay() == bl.YearDay()
}
