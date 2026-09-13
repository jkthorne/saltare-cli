package ui

import tea "github.com/charmbracelet/bubbletea"

// Mouse support is strictly additive: every action here has a keyboard
// equivalent, and a terminal (or an ssh hop, or a screen session) that never
// reports mouse events loses nothing. It is also opt-out — enabling mouse
// tracking takes click-drag text selection away from the terminal, so `sal
// --no-mouse`, `"mouse": false` in the config, and the palette's toggle all
// turn it back off. Shift-drag (or option-drag on macOS Terminal) selects text
// with tracking on in most terminals.
//
// Wired here: wheel scrolling, sidebar rows, chat message selection, composer
// focus, home dashboard rows, the tasks/documents/files/database list panes, the
// modal overlays, and the status bar's key hints. Not yet: right-click menus
// and drag.
//
// One rule decides where a click may land: it acts only where the keyboard
// cursor currently is. A pane whose keys have been handed to a prompt (the
// new-document title, the upload path, a cell editor) or to a y/n confirmation
// has no cursor over its rows, so clicks there are inert rather than doing
// something the keyboard can't. The new-task project picker is the one prompt
// whose rows *are* the cursor, and it stays clickable.

// wheelLines is how far one wheel notch moves a cursor-based list. The viewport
// has its own MouseWheelDelta (3) for the surfaces it scrolls itself.
const wheelLines = 3

func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	// Before the first WindowSizeMsg the rects are empty and View is still
	// drawing the loading line — every rect test fails closed.
	if !m.ready {
		return m, nil
	}
	if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown {
		return m.handleWheel(msg)
	}
	// Press-only, left-only. Motion and release arrive too (cell motion reports
	// drags) and would double-fire every click.
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return m, nil
	}
	return m.handleClick(msg)
}

// overlayActive reports whether a modal surface owns the pane. Clicks go to the
// overlay itself; everything behind it — the sidebar included — stays inert,
// because clicking through to a surface whose keys the overlay has taken would
// be worse than ignoring the click.
func (m Model) overlayActive() bool {
	return m.pal.active || m.search.active || m.notify.active || m.attach.active ||
		m.threadPicker.active || m.embedPicker.active
}

func (m Model) handleWheel(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	// The sidebar has no scroll of its own — its window follows the cursor, so a
	// wheel event there would have to move the selection, which opens channels.
	if m.overlayActive() || !m.rects.pane.contains(msg.X, msg.Y) {
		return m, nil
	}

	up := msg.Button == tea.MouseButtonWheelUp
	switch {
	case m.view == viewDocs && m.docs.viewing != nil:
		var cmd tea.Cmd
		m.docs.vp, cmd = m.docs.vp.Update(msg)
		return m, cmd

	case m.view == viewDB && m.db.level == dbLevelGrid:
		// bubbles/table has no mouse handling at all — translate the wheel into
		// the cursor moves its keyboard bindings use.
		if up {
			m.db.grid.MoveUp(wheelLines)
		} else {
			m.db.grid.MoveDown(wheelLines)
		}
		return m, nil

	case m.view == viewChat:
		// The whole pane scrolls the feed, not just the viewport rect: people
		// scroll wherever the pointer happens to be sitting. The viewport
		// handles the wheel itself. This covers the assistant, which shares m.vp.
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m Model) handleClick(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	// The status bar sits under everything, overlays included — its hints name
	// global keys, and they answer a click wherever those keys answer a press.
	if m.rects.status.contains(msg.X, msg.Y) {
		return m.clickStatus(msg.X)
	}
	if m.overlayActive() {
		if !m.rects.pane.contains(msg.X, msg.Y) {
			return m, nil
		}
		return m.clickOverlay(msg.Y)
	}
	if m.rects.sidebar.contains(msg.X, msg.Y) {
		return m.clickSidebar(msg.Y)
	}
	if !m.rects.pane.contains(msg.X, msg.Y) {
		return m, nil
	}
	switch m.view {
	case viewHome:
		return m.clickHome(msg.Y)
	case viewChat:
		return m.clickChat(msg)
	case viewTasks:
		return m.clickTasks(msg.Y)
	case viewDocs:
		return m.clickDocs(msg.Y)
	case viewFiles:
		return m.clickFiles(msg.Y)
	case viewDB:
		return m.clickDB(msg.Y)
	}
	return m, nil
}

// paneItemAt maps a screen row to the item a list pane draws there, or noTarget.
// The builder is rebuilt from live view state rather than cached from the last
// frame, for the same reason the sidebar's is: a fetch landing between the
// render and the click would leave a cached map describing rows that are gone.
func (m Model) paneItemAt(b *rowBuilder, y int) int {
	return b.target(y - m.rects.pane.y - panePadTop)
}

// clickTasks opens the clicked task's detail, which is what enter does. While
// the new-task project picker is up the rows are projects, and clicking one
// picks it and creates the task — again the only thing enter does there.
func (m Model) clickTasks(y int) (tea.Model, tea.Cmd) {
	t := &m.tasks
	// The detail pane draws facts and prose, not rows; the title prompt owns
	// the keys.
	if t.detail != nil || (t.inputOpen && !t.pickOpen) {
		return m, nil
	}
	idx := m.paneItemAt(t.listLines(m.rects.pane.w), y)
	if idx == noTarget {
		return m, nil
	}
	if t.pickOpen {
		if idx >= len(t.projects) {
			return m, nil
		}
		t.pickSel = idx
		project := t.projects[idx]
		title := t.pendingTitle
		t.closeInput()
		return m, m.createTask(project.ID, title)
	}
	if t.agenda {
		if idx >= len(t.rows) || !t.rows[idx].selectable() {
			return m, nil
		}
	} else if idx >= len(t.tasks) {
		return m, nil
	}
	t.sel = idx
	if task, ok := t.selected(); ok {
		selected := task
		t.detail = &selected
	}
	return m, nil
}

// clickDocs opens the clicked document in the reader, which is what enter does.
func (m Model) clickDocs(y int) (tea.Model, tea.Cmd) {
	d := &m.docs
	// The reader scrolls on the wheel and has no rows; the conflict prompt and
	// the new-document title own the keys while they are up.
	if d.viewing != nil || d.conflict != nil || d.inputOpen {
		return m, nil
	}
	idx := m.paneItemAt(d.listLines(m.rects.pane.w), y)
	if idx == noTarget || idx >= len(d.list) {
		return m, nil
	}
	d.sel = idx
	doc := d.list[idx]
	d.loading = true
	return m, m.fetchDocument(doc.Slug, false)
}

// clickFiles moves the cursor only. Nothing here activates on enter — the row
// under the cursor is what d/x/y act on — so a click that opened something
// would be inventing an action the keyboard doesn't have.
func (m Model) clickFiles(y int) (tea.Model, tea.Cmd) {
	f := &m.files
	if f.inputOpen || f.confirmRm != nil {
		return m, nil
	}
	idx := m.paneItemAt(f.listLines(m.rects.pane.w), y)
	if idx == noTarget || idx >= len(f.list) {
		return m, nil
	}
	f.sel = idx
	return m, nil
}

// clickDB opens the clicked table (enter) from the list, and moves the field
// cursor in a row detail — where enter opens a cell editor, which is a prompt a
// stray click shouldn't put you inside.
func (m Model) clickDB(y int) (tea.Model, tea.Cmd) {
	d := &m.db
	switch d.level {
	case dbLevelList:
		idx := m.paneItemAt(d.listLines(m.rects.pane.w), y)
		if idx == noTarget || idx >= len(d.list) {
			return m, nil
		}
		d.sel = idx
		return m.openDBGrid(d.list[idx].Slug)
	case dbLevelGrid:
		// The grid is a place you point at things: the cursor drives y and o as
		// well as enter, so a click selects and a second click on the same row
		// opens it — the feed's bargain, for the same reason.
		if d.loading || d.database == nil {
			return m, nil
		}
		idx := d.gridRowAt(y - m.rects.pane.y - dbGridTopLine)
		if idx < 0 || idx >= len(d.rows) {
			return m, nil
		}
		if idx == d.grid.Cursor() {
			return m.openDBRow()
		}
		d.grid.SetCursor(idx)

	case dbLevelDetail:
		row := d.currentRow()
		if row == nil || d.database == nil || d.editOpen {
			return m, nil
		}
		idx := m.paneItemAt(d.detailLines(row, m.rects.pane.w), y)
		if idx == noTarget || idx >= len(d.columns()) {
			return m, nil
		}
		d.fieldSel = idx
	}
	return m, nil
}

// clickStatus presses the key the hint under the pointer names. Routing the
// click back through handleKey rather than calling the action directly is what
// keeps the mouse from reaching a state the keyboard can't: ctrl+k, ctrl+h and
// ctrl+n are global, ctrl+t only exists in chat, and the key router is the one
// place that knows the difference.
func (m Model) clickStatus(x int) (tea.Model, tea.Cmd) {
	_, spans := m.statusBarLine()
	for _, span := range spans {
		if x >= span.x && x < span.x+span.w {
			return m.handleKey(tea.KeyMsg{Type: span.key})
		}
	}
	return m, nil
}

// clickOverlay runs the row under the pointer, which is what enter does in every
// one of these: they are all pickers, and a picker's whole purpose is choosing a
// row. The text input keeps focus either way — a click that only moved a
// highlight would leave the overlay needing a keystroke it was opened to avoid.
func (m Model) clickOverlay(y int) (tea.Model, tea.Cmd) {
	switch {
	case m.pal.active:
		idx := m.paneItemAt(m.pal.lines(m.rects.pane.w), y)
		if idx == noTarget || idx >= len(m.pal.filtered) {
			return m, nil
		}
		item := m.pal.filtered[idx]
		m.pal.close()
		return m.runPaletteItem(item)

	case m.search.active:
		idx := m.paneItemAt(m.search.lines(m.rects.pane.w), y)
		if idx == noTarget || idx >= len(m.search.rows) || !m.search.rows[idx].selectable() {
			return m, nil
		}
		m.search.sel = idx
		return m.openSearchResult(m.search.rows[idx])

	case m.notify.active:
		idx := m.paneItemAt(m.notify.lines(m.rects.pane.w), y)
		if idx == noTarget || idx >= len(m.notify.items) {
			return m, nil
		}
		m.notify.sel = idx
		return m.openNotification(m.notify.items[idx])

	case m.attach.active:
		idx := m.paneItemAt(m.attachLines(m.rects.pane.w), y)
		if idx == noTarget || idx >= len(m.attach.results) {
			return m, nil
		}
		return m.insertUploadRef(m.attach.results[idx].Slug)

	case m.threadPicker.active:
		idx := m.paneItemAt(m.threadPickerLines(m.rects.pane.w), y)
		if idx == noTarget || idx >= len(m.threadPicker.threads) {
			return m, nil
		}
		thread := m.threadPicker.threads[idx]
		m.threadPicker.active = false
		return m.openThread(thread)

	case m.embedPicker.active:
		idx := m.paneItemAt(m.embedPickerLines(m.rects.pane.w), y)
		if idx == noTarget || idx >= len(m.embedPicker.refs) {
			return m, nil
		}
		ref := m.embedPicker.refs[idx]
		m.embedPicker.active = false
		return m.followEmbed(ref)
	}
	return m, nil
}

// clickSidebar opens the row under the pointer — the same thing enter does, so a
// single click activates rather than merely selecting. Nav-rail rows switch mode
// through openSelected → runNav; channels and agent stubs open a feed, which
// means leaving whatever non-chat view is up.
func (m Model) clickSidebar(y int) (tea.Model, tea.Cmd) {
	idx := m.sidebarItemAt(y)
	if idx < 0 || idx >= len(m.items) {
		return m, nil
	}
	m.selected = idx
	if !m.items[idx].isNav() {
		m.view = viewChat
	}
	return m.openSelected(true)
}

// sidebarItemAt maps a screen row to a sidebar item index, or -1 for the pinned
// header, a group label, a spacer, or a "↑ N more" marker. It rebuilds the
// column from the same state View renders rather than caching the last frame:
// unread counts mutate on every cable event, so a cached map would go stale
// between the render and the click.
func (m Model) sidebarItemAt(y int) int {
	rows := sidebarVisible(m.store, m.sidebarState())
	return rows.target(y - m.rects.sidebar.y)
}

func (m Model) clickHome(y int) (tea.Model, tea.Cmd) {
	rows := m.homeRows()
	idx := m.homeLines(rows, m.rects.pane.w).target(y - m.rects.pane.y - panePadTop)
	if idx < 0 || idx >= len(rows) {
		return m, nil
	}
	m.home.sel = idx
	return m.activateHomeRow(rows[idx])
}

func (m Model) clickChat(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.rects.composer.contains(msg.X, msg.Y) {
		if m.focus == focusComposer {
			return m, nil
		}
		m.focus = focusComposer
		m.feedSel = 0
		m.refreshFeed(false)
		return m, m.comp.focus()
	}
	// The assistant borrows m.vp, so feedBlocks describes a channel that isn't
	// on screen. Its transcript has nothing to select.
	if m.assist.active || !m.rects.feed.contains(msg.X, msg.Y) {
		return m, nil
	}

	id := m.messageAt(msg.Y)
	if id == 0 {
		return m, nil // a date rule, the NEW marker, or blank space below the feed
	}
	// Clicking the already-selected message activates it, the way enter does.
	// That's a deliberate substitute for double-click: no timing threshold to
	// get wrong, and no ambiguity about what the first click did.
	if id == m.feedSel && m.focus == focusFeed {
		return m.followSelectionEmbeds()
	}
	m.focus = focusFeed
	m.feedSel = id
	m.comp.blur()
	m.refreshFeed(false)
	return m, nil
}

// messageAt maps a screen row in the feed to the message drawn there. feedBlocks
// already records every message's line span in the viewport's content — this is
// scrollToSelection's arithmetic run backwards.
func (m Model) messageAt(y int) int64 {
	line := y - m.rects.feed.y + m.vp.YOffset
	for _, b := range m.feedBlocks {
		if line >= b.Line && line < b.Line+b.Rows {
			return b.ID
		}
	}
	return 0
}

func mouseToggleLabel(on bool) string {
	if on {
		return "⊙ mouse: on"
	}
	return "⊙ mouse: off"
}

// toggleMouse turns tracking off and on inside a running session — the escape
// hatch for "I need to drag-select this stack trace right now".
func (m Model) toggleMouse() (tea.Model, tea.Cmd) {
	m.mouse = !m.mouse
	if m.mouse {
		return m, tea.Batch(tea.EnableMouseCellMotion, m.showToast("mouse on"))
	}
	return m, tea.Batch(tea.DisableMouse, m.showToast("mouse off — drag to select text"))
}
