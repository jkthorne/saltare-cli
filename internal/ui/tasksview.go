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

	detail *api.Task // non-nil = detail pane open

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

func (t *tasksView) render(r *feedRenderer, width, height int) string {
	if t.detail != nil {
		return t.renderDetail(r, width, height)
	}

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

	rows = append(rows, "", styleFeedTopic.Render("j/k move · enter detail · x done/reopen · n new · m mine/all · r refresh · esc chat"))
	body := strings.Join(rows, "\n")
	return styleTasksPane.Width(width).Height(height).MaxHeight(height).Render(body)
}

// renderDetail mirrors the web task page hierarchy: title, state facts,
// dates, description, subtasks — with the discussion one keystroke away.
func (t *tasksView) renderDetail(r *feedRenderer, width, height int) string {
	task := t.detail
	today := time.Now().Format("2006-01-02")

	title := stateGlyph(task.State) + " " + task.Title
	titleStyle := styleFeedTitle
	if task.State == "completed" || task.State == "cancelled" {
		titleStyle = styleTaskDone
	}

	var rows []string
	rows = append(rows, titleStyle.Render(truncate(title, width-6)))
	breadcrumb := task.Slug
	if name := t.projectName(task.ProjectID); name != "" {
		breadcrumb = name + " ▸ " + breadcrumb
	}
	rows = append(rows, styleFeedTopic.Render(breadcrumb), "")

	facts := []string{"state: " + task.State}
	if task.Priority != nil && *task.Priority != "none" {
		facts = append(facts, "priority: "+*task.Priority)
	}
	if task.Assignee != nil {
		facts = append(facts, fmt.Sprintf("assignee: %s #%d", strings.ToLower(task.Assignee.Type), task.Assignee.ID))
	}
	rows = append(rows, stylePickerRow.Render(strings.Join(facts, "  ·  ")))

	var dates []string
	if task.StartDate != nil && *task.StartDate != "" {
		dates = append(dates, "start "+*task.StartDate)
	}
	if task.DueDate != nil && *task.DueDate != "" {
		due := "due " + *task.DueDate
		if *task.DueDate < today && task.State != "completed" && task.State != "cancelled" {
			due = styleTaskOverdue.Render(due + " ⊗ overdue")
		}
		dates = append(dates, due)
	}
	if task.ReminderAt != nil {
		dates = append(dates, "reminder "+task.ReminderAt.Local().Format("Jan 2 15:04"))
	}
	if len(dates) > 0 {
		rows = append(rows, stylePickerRow.Render(strings.Join(dates, "  ·  ")))
	}
	if task.SubtasksCount > 0 {
		rows = append(rows, stylePickerRow.Render(fmt.Sprintf("subtasks: %d", task.SubtasksCount)))
	}
	rows = append(rows, "")

	if task.Description != nil && strings.TrimSpace(*task.Description) != "" {
		desc := strings.TrimSpace(*task.Description)
		rendered := ""
		if r != nil && r.markdown != nil {
			if out, err := r.markdown.Render(desc); err == nil {
				rendered = renderEmbedChips(strings.Trim(out, "\n"))
			}
		}
		if rendered == "" {
			rendered = renderEmbedChips(wrapPlain(desc, width-6))
		}
		rows = append(rows, rendered)
	} else {
		rows = append(rows, styleFeedTopic.Render("(no description)"))
	}

	rows = append(rows, "", styleFeedTopic.Render("enter/o discussion · x done/reopen · s next state · y copy embed · esc back"))
	return styleTasksPane.Width(width).Height(height).MaxHeight(height).Render(strings.Join(rows, "\n"))
}

// nextTaskState is the s-key cycle; completed/cancelled reopen.
func nextTaskState(state string) string {
	switch state {
	case "open":
		return "in_progress"
	case "in_progress":
		return "waiting"
	case "waiting":
		return "completed"
	default:
		return "open"
	}
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
