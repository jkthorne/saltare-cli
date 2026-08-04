package ui

import (
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/jkthorne/saltare/cli/internal/api"
)

const paletteMaxRows = 10

// paletteItem is one executable entry: jump to a channel, DM an agent, or run
// a named action.
type paletteItem struct {
	label     string
	hint      string
	action    string // "channel" | "agent" | one of the actionX constants
	channelID int64
	agent     *api.Agent
}

const (
	actionHome          = "home"
	actionTasksMine     = "tasks_mine"
	actionTasksAll      = "tasks_all"
	actionAgenda        = "agenda"
	actionNewTask       = "new_task"
	actionNotifications = "notifications"
	actionAssistant     = "assistant"
	actionSearch        = "search"
	actionDocs          = "docs"
	actionFiles         = "files"
	actionDB            = "databases"
)

type palette struct {
	active   bool
	input    textinput.Model
	items    []paletteItem
	filtered []paletteItem
	sel      int
}

func newPalette() palette {
	ti := textinput.New()
	ti.Placeholder = "jump to a channel, agent, or action…"
	ti.Prompt = "◢ "
	return palette{input: ti}
}

func (p *palette) open(items []paletteItem) tea.Cmd {
	p.active = true
	p.items = items
	p.input.SetValue("")
	p.refilter()
	return p.input.Focus()
}

func (p *palette) close() {
	p.active = false
	p.input.Blur()
}

func (p *palette) update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	p.input, cmd = p.input.Update(msg)
	p.refilter()
	return cmd
}

func (p *palette) move(delta int) {
	if len(p.filtered) == 0 {
		return
	}
	p.sel = (p.sel + delta + len(p.filtered)) % len(p.filtered)
}

func (p *palette) selected() (paletteItem, bool) {
	if len(p.filtered) == 0 {
		return paletteItem{}, false
	}
	return p.filtered[p.sel], true
}

// refilter ranks by match quality: prefix beats substring; original order
// breaks ties (channels before actions as assembled).
func (p *palette) refilter() {
	query := strings.ToLower(strings.TrimSpace(p.input.Value()))
	if query == "" {
		p.filtered = capRows(p.items)
		p.sel = 0
		return
	}

	type ranked struct {
		item paletteItem
		rank int
		pos  int
	}
	var matches []ranked
	for i, it := range p.items {
		label := strings.ToLower(it.label)
		switch {
		case strings.HasPrefix(label, query):
			matches = append(matches, ranked{it, 0, i})
		case strings.Contains(label, query):
			matches = append(matches, ranked{it, 1, i})
		}
	}
	sort.SliceStable(matches, func(a, b int) bool {
		if matches[a].rank != matches[b].rank {
			return matches[a].rank < matches[b].rank
		}
		return matches[a].pos < matches[b].pos
	})

	p.filtered = nil
	for _, m := range matches {
		p.filtered = append(p.filtered, m.item)
	}
	p.filtered = capRows(p.filtered)
	if p.sel >= len(p.filtered) {
		p.sel = 0
	}
}

func capRows(items []paletteItem) []paletteItem {
	if len(items) > paletteMaxRows {
		return items[:paletteMaxRows]
	}
	return items
}

func (p *palette) render(width int) string {
	var rows []string
	rows = append(rows, stylePickerTitle.Render("command palette"), p.input.View(), "")
	if len(p.filtered) == 0 {
		rows = append(rows, stylePickerRow.Render("no matches"))
	}
	for i, it := range p.filtered {
		label := it.label
		if it.hint != "" {
			label += "  " + styleFeedTopic.Render(it.hint)
		}
		if i == p.sel {
			rows = append(rows, stylePickerSel.Render("▸ "+truncate(label, width-6)))
		} else {
			rows = append(rows, stylePickerRow.Render("  "+truncate(label, width-6)))
		}
	}
	rows = append(rows, "", styleFeedTopic.Render("enter run · esc close"))
	return strings.Join(rows, "\n")
}
