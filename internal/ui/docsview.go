package ui

import (
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"

	"github.com/jkthorne/saltare-cli/internal/api"
)

// docsView is the ctrl+o mode: a document browser with a glamour reader
// and an $EDITOR round-trip. It owns its viewport and renderer — the
// shared m.vp belongs to chat/assistant and gets rewritten by async cable
// refreshes, and a shared renderer would thrash its width-keyed cache.
type docsView struct {
	list    []api.Document
	sel     int
	loading bool

	viewing  *api.Document // non-nil = reader open
	vp       viewport.Model
	renderer *feedRenderer
	links    webLinks

	inputOpen bool // new-document title prompt
	input     textinput.Model

	editSlug string    // in-flight $EDITOR session state
	editBase time.Time // updated_at at fetch — the concurrency guard
	editPath string
	editBody string // pre-edit body, to skip no-op saves

	conflict *docConflict // pending 409 prompt
}

// docConflict is an unresolved stale_document save: the edited body waits
// on disk while the user picks overwrite or abandon.
type docConflict struct {
	slug string
	path string
	body string
}

func newDocsView(links webLinks) docsView {
	ti := textinput.New()
	ti.Placeholder = "document title…"
	ti.Prompt = "▤ "
	return docsView{input: ti, links: links, renderer: newFeedRenderer(80, links)}
}

func (d *docsView) resize(width, height int) {
	d.renderer.Resize(width)
	d.vp.Width = width
	d.vp.Height = height - 2 // title line + separator; status bar is outside
}

func (d *docsView) move(delta int) {
	if len(d.list) == 0 {
		return
	}
	next := d.sel + delta
	if next >= 0 && next < len(d.list) {
		d.sel = next
	}
}

func (d *docsView) selected() (api.Document, bool) {
	if len(d.list) == 0 || d.sel >= len(d.list) {
		return api.Document{}, false
	}
	return d.list[d.sel], true
}

// showDocument renders a fetched document into the reader viewport.
func (d *docsView) showDocument(doc *api.Document) {
	d.viewing = doc
	body := strings.TrimSpace(doc.Body)
	if body == "" {
		d.vp.SetContent(styleFeedTopic.Render("(empty document — e opens your editor)"))
		d.vp.GotoTop()
		return
	}
	source, refs := tokenizeEmbeds(body)
	if d.renderer.markdown != nil {
		if out, err := d.renderer.markdown.Render(source); err == nil {
			d.vp.SetContent(restoreEmbedChips(strings.Trim(out, "\n"), refs, d.links))
			d.vp.GotoTop()
			return
		}
	}
	d.vp.SetContent(restoreEmbedChips(wrapPlain(source, d.vp.Width-2), refs, d.links))
	d.vp.GotoTop()
}

// upsert refreshes the list entry after a save/create.
func (d *docsView) upsert(doc api.Document) {
	for i := range d.list {
		if d.list[i].Slug == doc.Slug {
			d.list[i] = doc
			return
		}
	}
	d.list = append([]api.Document{doc}, d.list...)
}

func (d *docsView) render(width, height int) string {
	if d.conflict != nil {
		body := strings.Join([]string{
			stylePickerTitle.Render("▤ " + d.conflict.slug),
			"",
			styleStatusDead.Render(" document changed on the server while you edited "),
			"",
			stylePickerRow.Render("your version is kept at " + d.conflict.path),
			"",
			styleFeedTopic.Render("o overwrite with your version · esc keep the file and abandon the save"),
		}, "\n")
		return styleTasksPane.Width(width).Height(height).MaxHeight(height).Render(body)
	}

	if d.viewing != nil {
		title := styleFeedTitle.Render("▤ "+d.viewing.Title) + "  " + styleFeedTopic.Render(d.viewing.Slug)
		hint := styleFeedTopic.Render("e edit · y copy embed · j/k scroll · esc back")
		return title + "\n" + d.vp.View() + "\n" + hint
	}

	body := d.listLines(width).join()
	return styleTasksPane.Width(width).Height(height).MaxHeight(height).Render(body)
}

// listLines lays the browser out, tagging each line with its index into d.list.
// A new-document prompt above the list shifts every row down, which is exactly
// the drift the builder exists to track.
func (d *docsView) listLines(width int) *rowBuilder {
	b := &rowBuilder{}
	b.chrome(stylePickerTitle.Render("▤ documents"), "")
	if d.inputOpen {
		b.chrome(d.input.View(), "")
	}
	switch {
	case d.loading:
		b.chrome(styleFeedTopic.Render("loading…"))
	case len(d.list) == 0:
		b.chrome(styleFeedTopic.Render("no documents yet — n creates one"))
	}
	for i, doc := range d.list {
		label := doc.Title + "  " + styleFeedTopic.Render(doc.Slug)
		if doc.Published {
			label += " " + styleStatusOK.Render("· published")
		}
		label += "  " + styleTimestamp.Render(doc.UpdatedAt.Local().Format("Jan 2"))
		b.row(pickerLine(label, i == d.sel, width), i)
	}
	b.chrome("", styleFeedTopic.Render("enter read · e edit · n new · y copy embed · r refresh · esc back"))
	return b
}
