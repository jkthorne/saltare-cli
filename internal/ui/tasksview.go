package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/jkthorne/saltare/cli/internal/api"
)

// tasksView is the full-pane task list (replaces the feed while active).
type tasksView struct {
	active  bool
	mine    bool
	loading bool
	tasks   []api.Task
	sel     int

	projects []api.Project

	// New-task flow: title input, then a project pick when several exist.
	inputOpen    bool
	input        textinput.Model
	pickOpen     bool
	pickSel      int
	pendingTitle string
}

func newTasksView() tasksView {
	ti := textinput.New()
	ti.Placeholder = "task title…"
	ti.Prompt = "☐ "
	return tasksView{mine: true, input: ti}
}

func stateGlyph(state string) string {
	switch state {
	case "open":
		return "○"
	case "in_progress":
		return "◐"
	case "waiting":
		return "◇"
	case "completed":
		return "●"
	case "cancelled":
		return "✕"
	default:
		return "·"
	}
}

func (t *tasksView) move(delta int) {
	if len(t.tasks) == 0 {
		return
	}
	next := t.sel + delta
	if next >= 0 && next < len(t.tasks) {
		t.sel = next
	}
}

func (t *tasksView) selected() (api.Task, bool) {
	if len(t.tasks) == 0 || t.sel >= len(t.tasks) {
		return api.Task{}, false
	}
	return t.tasks[t.sel], true
}

func (t *tasksView) openInput() tea.Cmd {
	t.inputOpen = true
	t.input.SetValue("")
	return t.input.Focus()
}

func (t *tasksView) closeInput() {
	t.inputOpen = false
	t.pickOpen = false
	t.input.Blur()
}

func (t *tasksView) projectName(id int64) string {
	for _, p := range t.projects {
		if p.ID == id {
			return p.Name
		}
	}
	return ""
}

func (t *tasksView) render(width, height int) string {
	scope := "mine"
	if !t.mine {
		scope = "all"
	}
	var rows []string
	rows = append(rows, stylePickerTitle.Render("☑ tasks — "+scope))
	rows = append(rows, "")

	switch {
	case t.inputOpen && t.pickOpen:
		rows = append(rows, styleFeedTopic.Render("project for: "+t.pendingTitle), "")
		for i, p := range t.projects {
			label := fmt.Sprintf("%s  ·  %d active", p.Name, p.ActiveTasksCount)
			if i == t.pickSel {
				rows = append(rows, stylePickerSel.Render("▸ "+truncate(label, width-6)))
			} else {
				rows = append(rows, stylePickerRow.Render("  "+truncate(label, width-6)))
			}
		}
		rows = append(rows, "", styleFeedTopic.Render("enter create · esc cancel"))
	case t.inputOpen:
		rows = append(rows, t.input.View(), "", styleFeedTopic.Render("enter next · esc cancel"))
	case t.loading:
		rows = append(rows, styleFeedTopic.Render("loading…"))
	case len(t.tasks) == 0:
		rows = append(rows, styleFeedTopic.Render("nothing here — n creates a task"))
	default:
		today := time.Now().Format("2006-01-02")
		for i, task := range t.tasks {
			rows = append(rows, t.taskRow(task, i == t.sel, today, width))
		}
	}

	rows = append(rows, "", styleFeedTopic.Render("j/k move · x done/reopen · n new · m mine/all · r refresh · esc chat"))
	body := strings.Join(rows, "\n")
	return styleTasksPane.Width(width).Height(height).MaxHeight(height).Render(body)
}

func (t *tasksView) taskRow(task api.Task, selected bool, today string, width int) string {
	glyph := stateGlyph(task.State)
	due := ""
	if task.DueDate != nil && *task.DueDate != "" {
		if *task.DueDate < today && task.State != "completed" && task.State != "cancelled" {
			due = styleTaskOverdue.Render(" ⊗ " + *task.DueDate)
		} else {
			due = styleTimestamp.Render(" · " + *task.DueDate)
		}
	}
	project := ""
	if name := t.projectName(task.ProjectID); name != "" {
		project = styleFeedTopic.Render("  [" + name + "]")
	}

	marker := "  "
	if selected {
		marker = "▸ "
	}
	title := truncate(glyph+" "+task.Title, width-16)
	style := stylePickerRow
	if selected {
		style = stylePickerSel
	}
	if task.State == "completed" || task.State == "cancelled" {
		style = styleTaskDone
	}
	return marker + style.Render(title) + due + project
}
