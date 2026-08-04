package ui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jkthorne/saltare/cli/internal/api"
)

// homeView is the boot dashboard: unread notification count, unread
// channels, and my-work due buckets. It deliberately caches no rows — the
// store's unread counts mutate on every cable event, so rows are rebuilt
// from live data on each keypress/render.
type homeView struct {
	sel         int
	notifCount  int
	notifLoaded bool
}

// homeRow is one display line: a section header, a dim info line, or a
// selectable target (notifications inbox, a channel, a task).
type homeRow struct {
	label     string
	header    bool
	channelID int64
	task      *api.Task
	notif     bool
}

func (r homeRow) selectable() bool { return r.channelID != 0 || r.task != nil || r.notif }

// homeSections buckets active dated tasks for the my-work block.
func homeSections(tasks []api.Task, now time.Time) (overdue, dueToday, dueWeek []api.Task) {
	today := now.Format("2006-01-02")
	weekEnd := now.AddDate(0, 0, 6).Format("2006-01-02")
	for _, t := range tasks {
		if t.State == "completed" || t.State == "cancelled" {
			continue
		}
		if t.DueDate == nil || *t.DueDate == "" {
			continue
		}
		switch {
		case *t.DueDate < today:
			overdue = append(overdue, t)
		case *t.DueDate == today:
			dueToday = append(dueToday, t)
		case *t.DueDate <= weekEnd:
			dueWeek = append(dueWeek, t)
		}
	}
	return overdue, dueToday, dueWeek
}

func (m *Model) homeRows() []homeRow {
	var rows []homeRow

	switch {
	case !m.home.notifLoaded:
		rows = append(rows, homeRow{label: "◉ notifications …"})
	case m.home.notifCount == 1:
		rows = append(rows, homeRow{label: "◉ 1 unread notification", notif: true})
	case m.home.notifCount > 1:
		rows = append(rows, homeRow{label: fmt.Sprintf("◉ %d unread notifications", m.home.notifCount), notif: true})
	default:
		rows = append(rows, homeRow{label: "◉ no unread notifications"})
	}

	rows = append(rows, homeRow{label: "unread channels", header: true})
	anyUnread := false
	for _, it := range m.items {
		if it.channel == nil {
			continue
		}
		count := m.store.Unread(it.channel.ID)
		if count == 0 {
			continue
		}
		anyUnread = true
		rows = append(rows, homeRow{
			label:     fmt.Sprintf("%s %s  (%d)", kindGlyph(it.channel.Kind), it.channel.Title(), count),
			channelID: it.channel.ID,
		})
	}
	if !anyUnread {
		rows = append(rows, homeRow{label: "all read"})
	}

	overdue, dueToday, dueWeek := homeSections(m.tasks.tasks, time.Now())
	section := func(title string, tasks []api.Task) {
		if len(tasks) == 0 {
			return
		}
		rows = append(rows, homeRow{label: title, header: true})
		for i := range tasks {
			task := tasks[i]
			rows = append(rows, homeRow{task: &task})
		}
	}
	section("overdue", overdue)
	section("due today", dueToday)
	section("due this week", dueWeek)
	return rows
}

// moveHome skips headers and dim info lines.
func (m *Model) moveHome(delta int) {
	rows := m.homeRows()
	if len(rows) == 0 {
		return
	}
	next := m.home.sel + delta
	for next >= 0 && next < len(rows) && !rows[next].selectable() {
		next += delta
	}
	if next >= 0 && next < len(rows) {
		m.home.sel = next
	}
}

// leaveHomeToChat drops into the already-warm feed. The boot markRead is
// deferred to here (handleChannelsLoaded gates it on viewChat), and unlike
// the old boot path it snapshots first, so the NEW rule is armed.
func (m Model) leaveHomeToChat() (tea.Model, tea.Cmd) {
	m.view = viewChat
	m.focus = focusComposer
	cmds := []tea.Cmd{m.comp.focus()}
	if c, ok := m.store.Channel(m.focusedID); ok && c.Member && m.store.Unread(c.ID) > 0 {
		m.snapshotUnread(c)
		cmds = append(cmds, m.markRead(c))
	}
	// The viewport still holds the frame built before the snapshot (cable
	// refreshes on home render without a mark) — rebuild so the NEW rule shows.
	m.refreshFeed(true)
	return m, tea.Batch(cmds...)
}

// goHome returns to the dashboard from anywhere (ctrl+h, palette),
// closing overlays and refreshing home's data.
func (m Model) goHome() (tea.Model, tea.Cmd) {
	m.pal.close()
	m.search.close()
	m.attach.close()
	m.notify.active = false
	m.threadPicker.active = false
	m.view = viewHome
	m.comp.blur()
	m.tasks.detail = nil
	m.tasks.agenda = false
	m.tasks.mine = true
	m.tasks.loading = true
	m.tasks.fetchGen++
	return m, tea.Batch(m.fetchTasks(), m.fetchNotificationCount())
}

func (m Model) handleHomeKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		return m.leaveHomeToChat()
	case "j", "down":
		m.moveHome(1)
		return m, nil
	case "k", "up":
		m.moveHome(-1)
		return m, nil
	case "r":
		m.tasks.agenda = false
		m.tasks.mine = true
		m.tasks.fetchGen++
		return m, tea.Batch(m.fetchChannels(), m.fetchTasks(), m.fetchNotificationCount())
	case "enter":
		rows := m.homeRows()
		if m.home.sel >= len(rows) || !rows[m.home.sel].selectable() {
			return m, nil
		}
		row := rows[m.home.sel]
		switch {
		case row.notif:
			return m, m.fetchNotifications()
		case row.channelID != 0:
			// openSelected skips snapshot+markRead for the already-focused
			// channel, so that jump goes through leaveHomeToChat instead.
			if row.channelID == m.focusedID {
				return m.leaveHomeToChat()
			}
			m.view = viewChat
			return m.openChannelByID(row.channelID)
		case row.task != nil:
			m.view = viewTasks
			m.tasks.active = true
			task := *row.task
			m.tasks.detail = &task
			m.tasks.returnHome = true
			return m, nil
		}
	}
	return m, nil
}

func (m Model) renderHome(width, height int) string {
	rows := m.homeRows()
	today := time.Now().Format("2006-01-02")

	var lines []string
	lines = append(lines, stylePickerTitle.Render("⌂ home — "+m.cfg.WorkspaceName), "")
	for i, row := range rows {
		selected := i == m.home.sel && row.selectable()
		switch {
		case row.header:
			lines = append(lines, "", styleFeedTopic.Render(row.label))
		case row.task != nil:
			lines = append(lines, m.tasks.taskRow(*row.task, selected, today, width))
		case row.selectable():
			if selected {
				lines = append(lines, stylePickerSel.Render("▸ "+truncate(row.label, width-6)))
			} else {
				lines = append(lines, stylePickerRow.Render("  "+truncate(row.label, width-6)))
			}
		default:
			lines = append(lines, styleFeedTopic.Render("  "+truncate(row.label, width-6)))
		}
	}
	lines = append(lines, "", styleFeedTopic.Render("j/k move · enter open · r refresh · esc chat · ctrl+k palette"))
	return styleTasksPane.Width(width).Height(height).MaxHeight(height).Render(strings.Join(lines, "\n"))
}
