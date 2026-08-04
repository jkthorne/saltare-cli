package ui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/jkthorne/saltare/cli/internal/api"
	"github.com/jkthorne/saltare/cli/internal/tablefmt"
)

// searchView is the ctrl+f overlay: debounced workspace search over the
// /api/v1/search endpoint. `/` in feed focus opens it scoped to the
// current channel.
type searchView struct {
	active  bool
	input   textinput.Model
	channel string // slug filter; "" = workspace-wide
	rows    []searchRow
	sel     int
	loading bool
	ran     bool // at least one query completed (distinguishes "" from "no hits")
	gen     int  // debounce + response generation
}

// searchRow is one rendered line; headers are not selectable.
type searchRow struct {
	header   string
	message  *api.SearchMessage
	task     *api.Task
	document *api.Document
	upload   *api.Upload
}

func (r searchRow) selectable() bool { return r.header == "" }

func newSearchView() searchView {
	ti := textinput.New()
	ti.Placeholder = "search the workspace…"
	ti.Prompt = "◢ "
	return searchView{input: ti}
}

func (s *searchView) open(channelSlug, channelName string) tea.Cmd {
	s.active = true
	s.channel = channelSlug
	s.input.SetValue("")
	s.rows = nil
	s.sel = 0
	s.loading = false
	s.ran = false
	if channelSlug != "" {
		s.input.Placeholder = "search #" + channelName + "…"
	} else {
		s.input.Placeholder = "search the workspace…"
	}
	return s.input.Focus()
}

func (s *searchView) close() {
	s.active = false
	s.input.Blur()
}

func (s *searchView) setResults(results *api.SearchResults) {
	s.loading = false
	s.ran = true
	s.rows = nil
	for i := range results.Messages {
		if len(s.rows) == 0 {
			s.rows = append(s.rows, searchRow{header: "messages"})
		}
		s.rows = append(s.rows, searchRow{message: &results.Messages[i]})
	}
	if len(results.Tasks) > 0 {
		s.rows = append(s.rows, searchRow{header: "tasks"})
		for i := range results.Tasks {
			s.rows = append(s.rows, searchRow{task: &results.Tasks[i]})
		}
	}
	if len(results.Documents) > 0 {
		s.rows = append(s.rows, searchRow{header: "documents"})
		for i := range results.Documents {
			s.rows = append(s.rows, searchRow{document: &results.Documents[i]})
		}
	}
	if len(results.Uploads) > 0 {
		s.rows = append(s.rows, searchRow{header: "files"})
		for i := range results.Uploads {
			s.rows = append(s.rows, searchRow{upload: &results.Uploads[i]})
		}
	}
	s.sel = 0
	s.advanceToSelectable(1)
}

func (s *searchView) move(delta int) {
	if len(s.rows) == 0 {
		return
	}
	next := s.sel + delta
	for next >= 0 && next < len(s.rows) && !s.rows[next].selectable() {
		next += delta
	}
	if next >= 0 && next < len(s.rows) {
		s.sel = next
	}
}

// advanceToSelectable nudges sel off a header (used after rebuild).
func (s *searchView) advanceToSelectable(delta int) {
	if s.sel < len(s.rows) && !s.rows[s.sel].selectable() {
		s.move(delta)
	}
}

func (s *searchView) selected() (searchRow, bool) {
	if s.sel >= len(s.rows) || !s.rows[s.sel].selectable() {
		return searchRow{}, false
	}
	return s.rows[s.sel], true
}

func (s *searchView) rowLine(row searchRow, width int) string {
	switch {
	case row.message != nil:
		m := row.message
		line := styleSenderUser.Render(m.Sender.Name) + " " + styleFeedTopic.Render("#"+m.Channel.Slug) +
			"  " + strings.ReplaceAll(m.Body, "\n", " ")
		return truncate(line, width)
	case row.task != nil:
		t := row.task
		line := "☑ " + t.Title + "  " + styleFeedTopic.Render(t.Slug+" · "+t.State)
		return truncate(line, width)
	case row.document != nil:
		d := row.document
		return truncate("▤ "+d.Title+"  "+styleFeedTopic.Render(d.Slug), width)
	case row.upload != nil:
		u := row.upload
		return truncate("⇱ "+u.Title+"  "+styleFeedTopic.Render(u.Slug+" · "+tablefmt.HumanSize(u.FileSize)), width)
	default:
		return ""
	}
}

func (s *searchView) render(width, height int) string {
	title := "⌕ search"
	if s.channel != "" {
		title += "  " + styleFeedTopic.Render("#"+s.channel)
	}
	var rows []string
	rows = append(rows, stylePickerTitle.Render(title), s.input.View(), "")

	switch {
	case s.loading:
		rows = append(rows, styleFeedTopic.Render("searching…"))
	case !s.ran:
		rows = append(rows, styleFeedTopic.Render("type at least 2 characters"))
	case len(s.rows) == 0:
		rows = append(rows, styleFeedTopic.Render("no results"))
	}

	for i, row := range s.rows {
		if row.header != "" {
			rows = append(rows, styleDateLabel.Render(strings.ToUpper(row.header)))
			continue
		}
		line := s.rowLine(row, width-6)
		if i == s.sel {
			rows = append(rows, stylePickerSel.Render("▸ "+line))
		} else {
			rows = append(rows, stylePickerRow.Render("  "+line))
		}
	}

	rows = append(rows, "", styleFeedTopic.Render("enter open · y copy link/embed · esc close"))
	body := strings.Join(rows, "\n")
	return styleTasksPane.Width(width).Height(height).MaxHeight(height).Render(body)
}
