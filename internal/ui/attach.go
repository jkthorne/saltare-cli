package ui

import (
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/jkthorne/saltare-cli/internal/api"
	"github.com/jkthorne/saltare-cli/internal/tablefmt"
)

// attachView is the composer's attach prompt (ctrl+y): path-looking input
// streams an upload, anything else live-searches existing uploads; enter
// inserts [[upload:slug]] at the composer cursor.
type attachView struct {
	active    bool
	input     textinput.Model
	results   []api.Upload
	sel       int
	loading   bool
	uploading bool
	gen       int // debounce + response generation (searchView precedent)
}

func newAttachView() attachView {
	ti := textinput.New()
	ti.Placeholder = "path (~/ ./ /) to upload, or search uploads…"
	ti.Prompt = "⇱ "
	return attachView{input: ti}
}

// looksLikePath decides upload-a-file vs search-existing.
func looksLikePath(s string) bool {
	return s == "~" || strings.HasPrefix(s, "~/") || strings.HasPrefix(s, "/") ||
		strings.HasPrefix(s, "./") || strings.HasPrefix(s, "../")
}

func (a *attachView) close() {
	a.active = false
	a.input.Blur()
	a.input.SetValue("")
	a.results = nil
	a.sel = 0
	a.loading = false
	a.uploading = false
}

func (m Model) openAttach() (tea.Model, tea.Cmd) {
	m.threadPicker.active = false
	m.embedPicker.active = false
	m.attach.close()
	m.attach.active = true
	m.comp.blur()
	return m, m.attach.input.Focus()
}

// updateAttachInput forwards typing to the input and re-arms the search
// debounce when the text changed into a non-path query. Every change bumps
// gen, so switching to a path also invalidates in-flight searches.
func (m *Model) updateAttachInput(msg tea.Msg) tea.Cmd {
	before := m.attach.input.Value()
	var cmd tea.Cmd
	m.attach.input, cmd = m.attach.input.Update(msg)
	if m.attach.input.Value() == before {
		return cmd
	}
	m.attach.gen++
	value := strings.TrimSpace(m.attach.input.Value())
	if looksLikePath(value) || len(value) < 2 {
		m.attach.results = nil
		m.attach.sel = 0
		m.attach.loading = false
		return cmd
	}
	m.attach.loading = true
	gen := m.attach.gen
	return tea.Batch(cmd, tea.Tick(searchDebounce, func(time.Time) tea.Msg {
		return attachDebounceMsg{gen}
	}))
}

func (m *Model) runAttachSearch() tea.Cmd {
	query := strings.TrimSpace(m.attach.input.Value())
	if looksLikePath(query) || len(query) < 2 {
		return nil
	}
	client, ctx := m.client, m.ctx
	gen := m.attach.gen
	return func() tea.Msg {
		uploads, err := client.Uploads(ctx, api.UploadsOpts{Query: query})
		return attachResultsMsg{gen: gen, uploads: uploads, err: err}
	}
}

func (m Model) handleAttachKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	a := &m.attach
	switch msg.String() {
	case "esc":
		a.close()
		m.focus = focusComposer
		return m, m.comp.focus()
	case "up":
		if a.sel > 0 {
			a.sel--
		}
		return m, nil
	case "down":
		if a.sel < len(a.results)-1 {
			a.sel++
		}
		return m, nil
	case "enter":
		value := strings.TrimSpace(a.input.Value())
		if looksLikePath(value) {
			path := expandHome(value)
			info, err := os.Stat(path)
			if err != nil {
				m.softErr = err.Error()
				return m, nil
			}
			if info.IsDir() {
				m.softErr = path + " is a directory"
				return m, nil
			}
			a.uploading = true
			client, ctx := m.client, m.ctx
			return m, func() tea.Msg {
				upload, err := client.UploadFile(ctx, path, "")
				if err != nil {
					return softErrMsg{err}
				}
				return attachDoneMsg{upload: *upload}
			}
		}
		if a.sel < len(a.results) {
			return m.insertUploadRef(a.results[a.sel].Slug)
		}
		return m, nil
	}
	// Everything else is typing — never intercept letters here, they belong
	// to the query/path.
	return m, m.updateAttachInput(msg)
}

// insertUploadRef closes the prompt and drops the embed at the composer cursor.
// Shared by enter and by a click, so the two can't drift.
func (m Model) insertUploadRef(slug string) (tea.Model, tea.Cmd) {
	m.attach.close()
	m.comp.insertAtCursor("[[upload:" + slug + "]]")
	m.focus = focusComposer
	return m, m.comp.focus()
}

func (m Model) renderAttach(width, height int) string {
	body := m.attachLines(width).join()
	return styleTasksPane.Width(width).Height(height).MaxHeight(height).Render(body)
}

// attachLines lays the prompt out, tagging each line with its index into the
// results. A path-looking query has no results to tag — enter uploads instead.
func (m Model) attachLines(width int) *rowBuilder {
	a := m.attach
	b := &rowBuilder{}
	b.chrome(stylePickerTitle.Render("⇱ attach"), "", a.input.View(), "")

	value := strings.TrimSpace(a.input.Value())
	switch {
	case a.uploading:
		b.chrome(styleFeedTopic.Render("uploading " + value + " …"))
	case looksLikePath(value):
		b.chrome(styleFeedTopic.Render("enter uploads this file"))
	case a.loading:
		b.chrome(styleFeedTopic.Render("searching…"))
	case len(a.results) == 0 && len(value) >= 2:
		b.chrome(styleFeedTopic.Render("no matches"))
	default:
		for i, u := range a.results {
			category := "pending"
			if u.Category != nil {
				category = *u.Category
			}
			label := u.Slug + "  " + styleFeedTopic.Render(category+" · "+tablefmt.HumanSize(u.FileSize)) + "  " + u.Title
			b.row(pickerLine(label, i == a.sel, width), i)
		}
	}

	b.chrome("", styleFeedTopic.Render("enter insert [[upload:slug]] · esc cancel"))
	return b
}
