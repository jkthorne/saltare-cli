package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/jkthorne/saltare-cli/internal/agenda"
	"github.com/jkthorne/saltare-cli/internal/api"
)

// tasksView is the full-pane task list (replaces the feed while active).
type tasksView struct {
	active  bool
	mine    bool
	agenda  bool // day-grouped next-7-days mode; sel indexes rows, not tasks
	loading bool
	tasks   []api.Task
	sel     int

	rows     []agendaRow // agenda mode only: headers + task rows
	fetchGen int         // stale tasksLoadedMsg responses are dropped

	projects []api.Project

	detail     *api.Task // non-nil = detail pane open
	returnHome bool      // detail was opened from home — esc returns there

	// New-task flow: title input, then a project pick when several exist.
	inputOpen    bool
	input        textinput.Model
	pickOpen     bool
	pickSel      int
	pendingTitle string
}

// agendaRow is one display line of the agenda: a section header or a task.
type agendaRow struct {
	header string
	task   *api.Task
}

func (r agendaRow) selectable() bool { return r.header == "" }

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
	if t.agenda {
		t.moveAgenda(delta)
		return
	}
	if len(t.tasks) == 0 {
		return
	}
	next := t.sel + delta
	if next >= 0 && next < len(t.tasks) {
		t.sel = next
	}
}

// moveAgenda skips section headers (searchView.move precedent).
func (t *tasksView) moveAgenda(delta int) {
	if len(t.rows) == 0 {
		return
	}
	next := t.sel + delta
	for next >= 0 && next < len(t.rows) && !t.rows[next].selectable() {
		next += delta
	}
	if next >= 0 && next < len(t.rows) {
		t.sel = next
	}
}

// advanceToSelectable nudges sel off a header (used after rebuild).
func (t *tasksView) advanceToSelectable(delta int) {
	if t.sel < len(t.rows) && !t.rows[t.sel].selectable() {
		t.moveAgenda(delta)
	}
}

func (t *tasksView) selected() (api.Task, bool) {
	if t.agenda {
		if t.sel < len(t.rows) && t.rows[t.sel].task != nil {
			return *t.rows[t.sel].task, true
		}
		return api.Task{}, false
	}
	if len(t.tasks) == 0 || t.sel >= len(t.tasks) {
		return api.Task{}, false
	}
	return t.tasks[t.sel], true
}

// rebuildAgendaRows rederives the display rows from the task list. The
// grouping drops out-of-window tasks itself, so a stale-mode fetch still
// renders correctly.
func (t *tasksView) rebuildAgendaRows() {
	sections := agenda.Group(t.tasks, time.Now(), agenda.WindowDays)
	t.rows = t.rows[:0]
	for _, s := range sections {
		label := s.Label
		if s.Date != "" {
			label += " · " + s.Date
		}
		t.rows = append(t.rows, agendaRow{header: label})
		for i := range s.Tasks {
			task := s.Tasks[i]
			t.rows = append(t.rows, agendaRow{task: &task})
		}
	}
	if t.sel >= len(t.rows) {
		t.sel = 0
	}
	t.advanceToSelectable(1)
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
	body := t.listLines(width).join()
	return styleTasksPane.Width(width).Height(height).MaxHeight(height).Render(body)
}

// listLines lays the pane out, tagging each line with the item it draws. The
// item space follows the cursor: while the new-task project picker is up the
// rows are projects (pickSel), in agenda mode they index t.rows, and otherwise
// t.tasks — the same three spaces move() switches between, so a click lands
// where the keyboard would.
func (t *tasksView) listLines(width int) *rowBuilder {
	scope := "mine"
	if !t.mine {
		scope = "all"
	}
	title := "☑ tasks — " + scope
	if t.agenda {
		title = "☑ agenda — next 7 days"
	}
	b := &rowBuilder{}
	b.chrome(stylePickerTitle.Render(title), "")

	switch {
	case t.inputOpen && t.pickOpen:
		b.chrome(styleFeedTopic.Render("project for: "+t.pendingTitle), "")
		for i, p := range t.projects {
			label := fmt.Sprintf("%s  ·  %d active", p.Name, p.ActiveTasksCount)
			b.row(pickerLine(label, i == t.pickSel, width), i)
		}
		b.chrome("", styleFeedTopic.Render("enter create · esc cancel"))
	case t.inputOpen:
		b.chrome(t.input.View(), "", styleFeedTopic.Render("enter next · esc cancel"))
	case t.loading:
		b.chrome(styleFeedTopic.Render("loading…"))
	case t.agenda && len(t.rows) == 0:
		b.chrome(styleFeedTopic.Render("nothing due in the next 7 days"))
	case t.agenda:
		today := time.Now().Format("2006-01-02")
		for i, row := range t.rows {
			if row.header != "" {
				b.chrome(styleFeedTopic.Render(row.header))
			} else {
				b.row(t.taskRow(*row.task, i == t.sel, today, width), i)
			}
		}
	case len(t.tasks) == 0:
		b.chrome(styleFeedTopic.Render("nothing here — n creates a task"))
	default:
		today := time.Now().Format("2006-01-02")
		for i, task := range t.tasks {
			b.row(t.taskRow(task, i == t.sel, today, width), i)
		}
	}

	hints := "j/k move · enter detail · x done/reopen · n new · m mine/all · r refresh · esc chat"
	if t.agenda {
		hints = "j/k move · enter detail · x done/reopen · n new · r refresh · esc chat"
	}
	b.chrome("", styleFeedTopic.Render(hints))
	return b
}

// renderDetail mirrors the web task page hierarchy: title, state facts,
// dates, description, subtasks — with the discussion one keystroke away.
func (t *tasksView) renderDetail(r *feedRenderer, width, height int) string {
	task := t.detail
	today := time.Now().Format("2006-01-02")

	// The renderer carries the workspace URL context, and it is optional here
	// (the markdown path below already guards on nil). A zero webLinks yields no
	// URLs, so a nil renderer degrades to unlinked text rather than broken links.
	var links webLinks
	if r != nil {
		links = r.links
	}

	title := stateGlyph(task.State) + " " + task.Title
	titleStyle := styleFeedTitle
	if task.State == "completed" || task.State == "cancelled" {
		titleStyle = styleTaskDone
	}

	var rows []string
	// Linked after truncation — truncate slices runes and would cut an escape
	// sequence in half.
	rows = append(rows, osc8(links.task(task.Slug), titleStyle.Render(truncate(title, width-6))))
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
		desc, refs := tokenizeEmbeds(strings.TrimSpace(*task.Description))
		rendered := ""
		if r != nil && r.markdown != nil {
			if out, err := r.markdown.Render(desc); err == nil {
				rendered = restoreEmbedChips(strings.Trim(out, "\n"), refs, links)
			}
		}
		if rendered == "" {
			rendered = restoreEmbedChips(wrapPlain(desc, width-6), refs, links)
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
