package ui

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/jkthorne/saltare-cli/internal/api"
)

// clickAt is a left press, the only click sal acts on.
func clickAt(x, y int) tea.MouseMsg {
	return tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}
}

func wheelAt(x, y int, up bool) tea.MouseMsg {
	button := tea.MouseButtonWheelDown
	if up {
		button = tea.MouseButtonWheelUp
	}
	// Bubble Tea reports wheel notches as presses, and bubbles/viewport ignores
	// anything else — a synthetic event has to match.
	return tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: button}
}

// mouseModel is a sized model sitting in a channel with n messages, which is the
// state every hit test needs: rects only exist after a WindowSizeMsg.
func mouseModel(t *testing.T, w, h, n int) Model {
	t.Helper()
	m := testModel(t)
	channel := api.Channel{ID: 1, Slug: "general", Name: "General", Kind: "public_channel", Member: true}
	step, _ := m.Update(channelsLoadedMsg{channels: []api.Channel{channel}})
	step, _ = step.(Model).Update(tea.WindowSizeMsg{Width: w, Height: h})
	model := step.(Model)

	msgs := make([]api.Message, 0, n)
	for i := 0; i < n; i++ {
		msgs = append(msgs, api.Message{
			ID: int64(100 + i), ChannelID: 1, Body: fmt.Sprintf("message %d", i),
			Sender: api.Sender{Type: "User", ID: int64(7 + i%2), Name: fmt.Sprintf("Sender%d", i%2)},
		})
	}
	model.store.MergeHistory(1, msgs)
	model.focusedID = 1
	model.refreshFeed(true)
	return model
}

// sidebarLineOf finds the screen row a sidebar label is drawn on, so the click
// tests don't hardcode row numbers that shift when the nav rail changes.
func sidebarLineOf(t *testing.T, m Model, label string) int {
	t.Helper()
	rows := sidebarVisible(m.store, m.sidebarState())
	for i, line := range rows.lines {
		if strings.Contains(line, label) {
			return i + m.rects.sidebar.y
		}
	}
	t.Fatalf("sidebar has no row containing %q", label)
	return -1
}

// ── geometry ────────────────────────────────────────────────────────────

// The rects are only trustworthy if they describe what View actually draws.
func TestRectsMatchTheRenderedFrame(t *testing.T) {
	m := mouseModel(t, 100, 30, 2)

	sidebar := renderSidebar(m.store, m.sidebarState())
	if got := lipgloss.Width(sidebar); got != m.rects.sidebar.w {
		t.Errorf("sidebar rect is %d wide, the column renders %d", m.rects.sidebar.w, got)
	}
	if m.rects.pane.x != m.rects.sidebar.w {
		t.Errorf("the pane must start where the sidebar ends: pane.x=%d sidebar.w=%d", m.rects.pane.x, m.rects.sidebar.w)
	}
	if got := m.rects.sidebar.w + m.rects.pane.w; got != m.width {
		t.Errorf("sidebar + pane must fill the terminal: %d != %d", got, m.width)
	}

	lines := strings.Split(m.View(), "\n")
	if len(lines) != m.height {
		t.Fatalf("the frame must be exactly the terminal height: %d lines for height %d", len(lines), m.height)
	}
	if m.rects.status.y != len(lines)-1 {
		t.Errorf("status rect at y=%d, last line is %d", m.rects.status.y, len(lines)-1)
	}
}

// A multi-line draft grows the composer, which has to come out of the feed's
// height — otherwise the frame overflows and the terminal scrolls a line.
func TestComposerGrowthKeepsTheFrameHeight(t *testing.T) {
	m := mouseModel(t, 100, 30, 2)
	before := m.rects.composer.h

	step, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("one")})
	step, _ = step.(Model).Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	step, _ = step.(Model).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("two")})
	grown := step.(Model)

	if grown.rects.composer.h <= before {
		t.Fatalf("the composer must grow with the draft: %d → %d", before, grown.rects.composer.h)
	}
	if lines := strings.Split(grown.View(), "\n"); len(lines) != grown.height {
		t.Fatalf("frame overflowed: %d lines for height %d", len(lines), grown.height)
	}
	if got := grown.rects.feed.y + grown.rects.feed.h; got > grown.rects.composer.y {
		t.Errorf("the feed rect must not run into the composer: feed ends %d, composer starts %d", got, grown.rects.composer.y)
	}
}

// ── sidebar ─────────────────────────────────────────────────────────────

func TestSidebarClickOpensTheChannel(t *testing.T) {
	m := mouseModel(t, 100, 30, 2)
	m.focusedID = 0 // force openSelected to do the work
	m.view = viewTasks

	y := sidebarLineOf(t, m, "General")
	step, _ := m.Update(clickAt(3, y))
	model := step.(Model)

	if model.focusedID != 1 {
		t.Fatalf("a click must open the channel, focusedID=%d", model.focusedID)
	}
	if model.view != viewChat {
		t.Fatalf("opening a channel from another view must switch to chat, view=%d", model.view)
	}
	// Single click activates, matching enter — so the composer takes focus.
	if model.focus != focusComposer {
		t.Fatalf("an opened channel must leave the composer focused, focus=%d", model.focus)
	}
}

func TestNavRailClickSwitchesMode(t *testing.T) {
	m := mouseModel(t, 100, 30, 2)

	y := sidebarLineOf(t, m, "my work")
	step, _ := m.Update(clickAt(3, y))
	model := step.(Model)

	if model.view != viewTasks {
		t.Fatalf("clicking the rail must switch mode, view=%d", model.view)
	}
	if model.focusedID != 1 {
		t.Errorf("a rail row must not change the open channel, focusedID=%d", model.focusedID)
	}
}

// Group labels, spacers and the pinned header carry no item — clicking them must
// not fall through to a neighbouring row.
func TestSidebarClickOnChromeIsInert(t *testing.T) {
	m := mouseModel(t, 100, 30, 2)
	m.focusedID = 0

	for _, y := range []int{0, 1, sidebarLineOf(t, m, "CHANNELS")} {
		step, _ := m.Update(clickAt(3, y))
		if got := step.(Model).focusedID; got != 0 {
			t.Fatalf("y=%d is chrome but opened channel %d", y, got)
		}
	}
}

// ── feed ────────────────────────────────────────────────────────────────

func TestFeedClickSelectsTheMessage(t *testing.T) {
	m := mouseModel(t, 100, 30, 3)

	block := m.feedBlocks[1]
	y := m.rects.feed.y + block.Line - m.vp.YOffset
	step, _ := m.Update(clickAt(m.rects.feed.x+4, y))
	model := step.(Model)

	if model.feedSel != block.ID {
		t.Fatalf("click must select message %d, got %d", block.ID, model.feedSel)
	}
	if model.focus != focusFeed {
		t.Fatalf("selecting in the feed must move focus there, focus=%d", model.focus)
	}
}

// Clicking the already-selected message activates it, standing in for a
// double-click. Two embeds open the picker, which is observable state.
func TestSecondClickActivatesTheSelection(t *testing.T) {
	m := mouseModel(t, 100, 30, 0)
	m.store.MergeHistory(1, []api.Message{{
		ID: 200, ChannelID: 1, Body: "see [[task:one]] and [[doc:two]]",
		Sender: api.Sender{Type: "User", ID: 7, Name: "Me"},
	}})
	m.refreshFeed(true)
	if len(m.feedBlocks) != 1 {
		t.Fatalf("test needs exactly one message in the feed, got %d blocks", len(m.feedBlocks))
	}

	y := m.rects.feed.y + m.feedBlocks[0].Line - m.vp.YOffset
	step, _ := m.Update(clickAt(m.rects.feed.x+4, y))
	first := step.(Model)
	if first.embedPicker.active {
		t.Fatal("the first click selects; it must not activate")
	}

	step, _ = first.Update(clickAt(m.rects.feed.x+4, y))
	if !step.(Model).embedPicker.active {
		t.Fatal("a click on the selected message must follow its references")
	}
}

// The feed scrolls, so screen rows and content lines diverge. This is the bug a
// hand-rolled offset would ship.
func TestFeedClickAccountsForScroll(t *testing.T) {
	m := mouseModel(t, 100, 12, 30)
	if m.vp.YOffset == 0 {
		t.Fatal("test needs a scrolled feed")
	}

	// The row at the top of the viewport belongs to whichever message spans the
	// first visible content line.
	want := int64(0)
	for _, b := range m.feedBlocks {
		if m.vp.YOffset >= b.Line && m.vp.YOffset < b.Line+b.Rows {
			want = b.ID
		}
	}
	if want == 0 {
		t.Fatal("no message spans the first visible line")
	}
	if got := m.messageAt(m.rects.feed.y); got != want {
		t.Fatalf("the top visible row is message %d, hit test said %d", want, got)
	}
}

// Date rules, the NEW marker, and the empty space under a short feed belong to no
// message.
func TestFeedClickOffAMessageIsInert(t *testing.T) {
	m := mouseModel(t, 100, 30, 2)

	if got := m.messageAt(m.rects.feed.y + m.rects.feed.h - 1); got != 0 {
		t.Fatalf("blank space below the feed must select nothing, got %d", got)
	}
	step, _ := m.Update(clickAt(m.rects.feed.x+4, m.rects.feed.y+m.rects.feed.h-1))
	if got := step.(Model).feedSel; got != 0 {
		t.Fatalf("a click on nothing must not change the selection, got %d", got)
	}
}

func TestComposerClickTakesFocus(t *testing.T) {
	m := mouseModel(t, 100, 30, 3)
	m.focus = focusFeed
	m.feedSel = 101
	m.comp.blur()

	step, _ := m.Update(clickAt(m.rects.composer.x+2, m.rects.composer.y))
	model := step.(Model)

	if model.focus != focusComposer {
		t.Fatalf("clicking the composer must focus it, focus=%d", model.focus)
	}
	if model.feedSel != 0 {
		t.Errorf("leaving the feed must clear its cursor, feedSel=%d", model.feedSel)
	}
}

// ── home ────────────────────────────────────────────────────────────────

func TestHomeClickOpensTheUnreadChannel(t *testing.T) {
	m := bootModel(t)
	step, _ := m.Update(channelsLoadedMsg{channels: []api.Channel{unreadChannel(1, "general", 2), unreadChannel(2, "random", 1)}})
	step, _ = step.(Model).Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	model := step.(Model)
	model.focusedID = 0

	// Find the line "random" is drawn on — home's section headers emit a spacer
	// as well as a label, so its row index is not its line index.
	rows := model.homeRows()
	lines := model.homeLines(rows, model.rects.pane.w)
	y := -1
	for i, line := range lines.lines {
		if strings.Contains(line, "random") {
			y = i + model.rects.pane.y + panePadTop
		}
	}
	if y < 0 {
		t.Fatal("the unread channel must appear on home")
	}

	step, _ = model.Update(clickAt(model.rects.pane.x+4, y))
	clicked := step.(Model)
	if clicked.focusedID != 2 {
		t.Fatalf("clicking an unread row must open that channel, focusedID=%d", clicked.focusedID)
	}
	if clicked.view != viewChat {
		t.Fatalf("opening a channel must leave home, view=%d", clicked.view)
	}
}

func TestHomeClickOnSectionHeaderIsInert(t *testing.T) {
	m := bootModel(t)
	step, _ := m.Update(channelsLoadedMsg{channels: []api.Channel{unreadChannel(1, "general", 2)}})
	step, _ = step.(Model).Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	model := step.(Model)

	lines := model.homeLines(model.homeRows(), model.rects.pane.w)
	y := -1
	for i, line := range lines.lines {
		if strings.Contains(line, "unread channels") {
			y = i + model.rects.pane.y + panePadTop
		}
	}
	if y < 0 {
		t.Fatal("home must render the unread-channels header")
	}

	step, _ = model.Update(clickAt(model.rects.pane.x+4, y))
	if got := step.(Model).view; got != viewHome {
		t.Fatalf("clicking a header must not navigate, view=%d", got)
	}
}

// ── wheel ───────────────────────────────────────────────────────────────

func TestWheelScrollsTheFeed(t *testing.T) {
	m := mouseModel(t, 100, 12, 30)
	m.vp.GotoTop()

	step, _ := m.Update(wheelAt(m.rects.feed.x+4, m.rects.feed.y, false))
	down := step.(Model)
	if down.vp.YOffset == 0 {
		t.Fatal("wheel down must scroll the feed")
	}
	step, _ = down.Update(wheelAt(m.rects.feed.x+4, m.rects.feed.y, true))
	if step.(Model).vp.YOffset >= down.vp.YOffset {
		t.Fatal("wheel up must scroll back")
	}
}

// The sidebar window follows the cursor rather than scrolling on its own, so a
// wheel event there would have to move the selection — which opens channels.
func TestWheelOverTheSidebarIsInert(t *testing.T) {
	m := mouseModel(t, 100, 12, 30)
	before := m.vp.YOffset

	step, _ := m.Update(wheelAt(1, 5, false))
	model := step.(Model)
	if model.vp.YOffset != before {
		t.Fatalf("the sidebar must not scroll the feed, YOffset %d → %d", before, model.vp.YOffset)
	}
	if model.selected != m.selected {
		t.Fatalf("the wheel must not move the sidebar cursor, %d → %d", m.selected, model.selected)
	}
}

func TestWheelMovesTheDatabaseGridCursor(t *testing.T) {
	m := mouseModel(t, 100, 30, 2)
	m.view = viewDB
	m.db.level = dbLevelGrid
	m.db.database = &api.Database{Slug: "crm", Name: "CRM", Schema: &api.DBSchema{
		Columns: []api.DBColumn{{Key: "name", Type: "text"}},
	}}
	for i := 0; i < 20; i++ {
		m.db.rows = append(m.db.rows, api.DBRow{ID: int64(i + 1), Data: map[string]any{"name": fmt.Sprintf("row %d", i)}})
	}
	m.db.buildGrid()

	step, _ := m.Update(wheelAt(m.rects.pane.x+4, 5, false))
	model := step.(Model)
	if got := model.db.grid.Cursor(); got != wheelLines {
		t.Fatalf("bubbles/table has no mouse support, so the wheel must move its cursor: got %d, want %d", got, wheelLines)
	}
}

// ── gating ──────────────────────────────────────────────────────────────

// Before the first WindowSizeMsg there is no frame to hit-test against.
func TestMouseIgnoredBeforeTheFirstResize(t *testing.T) {
	m := testModel(t)
	if m.ready {
		t.Fatal("a fresh model must not be ready")
	}
	step, cmd := m.Update(clickAt(3, 3))
	if cmd != nil {
		t.Fatal("a click before layout must do nothing")
	}
	if step.(Model).focusedID != 0 {
		t.Fatal("a click before layout must not open anything")
	}
}

// Cell motion reports drags and releases too; acting on them would double-fire
// every click. Right and middle buttons are unwired.
func TestOnlyLeftPressActs(t *testing.T) {
	m := mouseModel(t, 100, 30, 3)
	y := m.rects.feed.y + m.feedBlocks[1].Line

	ignored := []tea.MouseMsg{
		{X: m.rects.feed.x + 4, Y: y, Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft},
		{X: m.rects.feed.x + 4, Y: y, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft},
		{X: m.rects.feed.x + 4, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonRight},
		{X: m.rects.feed.x + 4, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonMiddle},
	}
	for _, msg := range ignored {
		step, _ := m.Update(msg)
		if got := step.(Model).feedSel; got != 0 {
			t.Fatalf("%v must be ignored, but it selected %d", msg, got)
		}
	}
}

// Modal surfaces stay keyboard-only for now: clicking through to what is behind
// them would be worse than ignoring the click.
func TestClicksIgnoredWhileAnOverlayIsUp(t *testing.T) {
	m := mouseModel(t, 100, 30, 3)
	step, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlK})
	open := step.(Model)
	if !open.overlayActive() {
		t.Fatal("ctrl+k must raise an overlay")
	}

	step, _ = open.Update(clickAt(3, sidebarLineOf(t, open, "my work")))
	if got := step.(Model).view; got != viewChat {
		t.Fatalf("a click behind an overlay must not navigate, view=%d", got)
	}
}

// ── toggle ──────────────────────────────────────────────────────────────

// The toggle is the escape hatch for the one real cost of mouse tracking: the
// terminal's own drag-to-select stops working.
func TestMouseToggleFlipsAndEmitsACommand(t *testing.T) {
	m := mouseModel(t, 100, 30, 2)
	if !m.mouse {
		t.Fatal("mouse defaults on")
	}

	step, cmd := m.toggleMouse()
	off := step.(Model)
	if off.mouse {
		t.Fatal("the toggle must flip the flag")
	}
	if cmd == nil {
		t.Fatal("the toggle must tell the program to stop tracking")
	}

	step, cmd = off.toggleMouse()
	if !step.(Model).mouse || cmd == nil {
		t.Fatal("toggling back must re-enable tracking")
	}
}

// bubbletea's RestoreTerminal re-arms the alt screen and bracketed paste but not
// mouse tracking, so resuming from $EDITOR has to ask for it back. Without this
// one document edit kills clicking for the rest of the session.
func TestResumingFromTheEditorReArmsMouseTracking(t *testing.T) {
	m := mouseModel(t, 100, 30, 2)
	m.docs.editSlug = "notes"
	m.docs.editPath = t.TempDir() + "/doc-notes.md"
	m.docs.editBody = "before"

	if _, cmd := m.Update(editorFinishedMsg{err: nil}); cmd == nil {
		t.Fatal("resuming from the editor must re-enable mouse tracking")
	}
	// An editor error takes an early-return path — it must re-arm too.
	if _, cmd := m.Update(editorFinishedMsg{err: errEditorTest}); cmd == nil {
		t.Fatal("a failed editor run must still re-enable mouse tracking")
	}
	// With mouse off there is nothing to restore.
	m.mouse = false
	if _, cmd := m.Update(editorFinishedMsg{err: errEditorTest}); cmd != nil {
		t.Fatal("mouse off must stay off after an editor round trip")
	}
}

var errEditorTest = errors.New("editor exploded")

// With tracking off, events can still arrive from an in-flight sequence, and the
// palette label has to describe the state the user is in.
func TestMouseToggleLabelDescribesTheState(t *testing.T) {
	if got := mouseToggleLabel(true); !strings.Contains(got, "on") {
		t.Fatalf("label must read as on, got %q", got)
	}
	if got := mouseToggleLabel(false); !strings.Contains(got, "off") {
		t.Fatalf("label must read as off, got %q", got)
	}
}

// ── list panes ──────────────────────────────────────────────────────────

// paneLineOf is the screen row a list pane draws an item on. Tests ask the same
// builder View does rather than hardcoding offsets that shift the moment a pane
// grows a prompt line.
func paneLineOf(m Model, b *rowBuilder, item int) int {
	return b.lineOf(item) + m.rects.pane.y + panePadTop
}

// tasksClickModel is a sized tasks pane — mouseModel, not taskdetail_test's
// tasksModel, because hit tests need the rects a WindowSizeMsg creates.
func tasksClickModel(t *testing.T) Model {
	t.Helper()
	m := mouseModel(t, 100, 30, 0)
	m.view = viewTasks
	m.tasks.active = true
	m.tasks.tasks = []api.Task{
		{ID: 1, Slug: "alpha", Title: "Alpha task", State: "open"},
		{ID: 2, Slug: "beta", Title: "Beta task", State: "open"},
	}
	return m
}

// The hit map is only right if the row it names is the row View draws. panePadTop
// is an assumption about the pane style's padding, and this is what checks it
// against the real frame.
func TestPaneRowLinesMatchTheRenderedFrame(t *testing.T) {
	m := tasksClickModel(t)

	y := paneLineOf(m, m.tasks.listLines(m.rects.pane.w), 1)
	lines := strings.Split(m.View(), "\n")
	if y < 0 || y >= len(lines) {
		t.Fatalf("row 1 mapped to line %d, frame has %d", y, len(lines))
	}
	if !strings.Contains(lines[y], "Beta task") {
		t.Fatalf("line %d should draw the second task, got %q", y, lines[y])
	}
}

func TestTasksClickOpensTheDetail(t *testing.T) {
	m := tasksClickModel(t)

	y := paneLineOf(m, m.tasks.listLines(m.rects.pane.w), 1)
	step, _ := m.Update(clickAt(m.rects.pane.x+4, y))
	model := step.(Model)

	if model.tasks.sel != 1 {
		t.Fatalf("a click must move the cursor, sel=%d", model.tasks.sel)
	}
	if model.tasks.detail == nil || model.tasks.detail.Slug != "beta" {
		t.Fatalf("a click must open the detail the way enter does, detail=%+v", model.tasks.detail)
	}
}

func TestTasksClickOnAHintLineIsInert(t *testing.T) {
	m := tasksClickModel(t)

	// The key-hint line at the bottom of the pane belongs to no task.
	step, _ := m.Update(clickAt(m.rects.pane.x+4, m.rects.pane.y+m.rects.pane.h-1))
	model := step.(Model)

	if model.tasks.detail != nil {
		t.Fatalf("chrome must not open anything, detail=%+v", model.tasks.detail)
	}
}

func TestAgendaClickSkipsSectionHeaders(t *testing.T) {
	m := tasksClickModel(t)
	m.tasks.agenda = true
	m.tasks.rows = []agendaRow{
		{header: "overdue"},
		{task: &m.tasks.tasks[0]},
		{header: "today"},
		{task: &m.tasks.tasks[1]},
	}

	rows := m.tasks.listLines(m.rects.pane.w)
	step, _ := m.Update(clickAt(m.rects.pane.x+4, paneLineOf(m, rows, 3)))
	model := step.(Model)
	if model.tasks.sel != 3 || model.tasks.detail == nil {
		t.Fatalf("a click must open the task under it, sel=%d detail=%+v", model.tasks.sel, model.tasks.detail)
	}

	// A header draws on its own line and selects nothing.
	headerLine := rows.lineOf(2)
	if headerLine != -1 {
		t.Fatalf("a section header must not be a click target, it claimed line %d", headerLine)
	}
}

func TestProjectPickClickCreatesTheTask(t *testing.T) {
	m := tasksClickModel(t)
	m.tasks.projects = []api.Project{{ID: 7, Name: "Apollo"}, {ID: 9, Name: "Gemini"}}
	m.tasks.inputOpen = true
	m.tasks.pickOpen = true
	m.tasks.pendingTitle = "ship it"

	y := paneLineOf(m, m.tasks.listLines(m.rects.pane.w), 1)
	step, cmd := m.Update(clickAt(m.rects.pane.x+4, y))
	model := step.(Model)

	if cmd == nil {
		t.Fatal("picking a project must dispatch the create, as enter does")
	}
	if model.tasks.inputOpen || model.tasks.pickOpen {
		t.Fatal("picking a project must close the prompt")
	}
}

func TestDocsClickOpensTheReader(t *testing.T) {
	m := mouseModel(t, 100, 30, 0)
	m.view = viewDocs
	m.docs.list = []api.Document{
		{ID: 1, Slug: "spec", Title: "The Spec"},
		{ID: 2, Slug: "notes", Title: "Notes"},
	}

	y := paneLineOf(m, m.docs.listLines(m.rects.pane.w), 1)
	step, cmd := m.Update(clickAt(m.rects.pane.x+4, y))
	model := step.(Model)

	if model.docs.sel != 1 {
		t.Fatalf("a click must move the cursor, sel=%d", model.docs.sel)
	}
	if cmd == nil || !model.docs.loading {
		t.Fatal("a click must fetch the document the way enter does")
	}
}

// A prompt owns the pane's keys while it is up, so it owns the clicks too.
func TestListPaneClickIsInertWhileAPromptOwnsTheKeys(t *testing.T) {
	m := mouseModel(t, 100, 30, 0)
	m.view = viewDocs
	m.docs.list = []api.Document{{ID: 1, Slug: "spec", Title: "The Spec"}}
	rows := m.docs.listLines(m.rects.pane.w) // laid out before the prompt opens
	m.docs.inputOpen = true

	step, cmd := m.Update(clickAt(m.rects.pane.x+4, paneLineOf(m, rows, 0)))
	if cmd != nil || step.(Model).docs.loading {
		t.Fatal("a click must not reach the list while the title prompt is up")
	}
}

// Files has no enter action — the cursor is what d/x/y act on — so a click
// moves it and nothing more.
func TestFilesClickMovesTheCursorOnly(t *testing.T) {
	m := mouseModel(t, 100, 30, 0)
	m.view = viewFiles
	m.files.list = []api.Upload{
		{ID: 1, Slug: "q4-report", Title: "Q4 Report"},
		{ID: 2, Slug: "logo", Title: "Logo"},
	}

	y := paneLineOf(m, m.files.listLines(m.rects.pane.w), 1)
	step, cmd := m.Update(clickAt(m.rects.pane.x+4, y))
	model := step.(Model)

	if model.files.sel != 1 {
		t.Fatalf("a click must move the cursor, sel=%d", model.files.sel)
	}
	if cmd != nil {
		t.Fatal("a click must not act where enter does nothing")
	}
}

func TestDatabaseListClickOpensTheGrid(t *testing.T) {
	m := mouseModel(t, 100, 30, 0)
	m.view = viewDB
	m.db.list = []api.Database{
		{ID: 1, Slug: "people", Name: "People"},
		{ID: 2, Slug: "orders", Name: "Orders"},
	}

	y := paneLineOf(m, m.db.listLines(m.rects.pane.w), 1)
	step, cmd := m.Update(clickAt(m.rects.pane.x+4, y))
	model := step.(Model)

	if model.db.level != dbLevelGrid || model.db.pendingSlug != "orders" {
		t.Fatalf("a click must open the table, level=%d slug=%q", model.db.level, model.db.pendingSlug)
	}
	if cmd == nil {
		t.Fatal("opening a table must dispatch its fetches")
	}
}

// Enter opens a cell editor here, and a stray click shouldn't drop you inside a
// prompt — so a click moves the field cursor and stops.
func TestDatabaseRowDetailClickMovesTheFieldCursor(t *testing.T) {
	m := mouseModel(t, 100, 30, 0)
	m.view = viewDB
	m.db.level = dbLevelDetail
	m.db.database = &api.Database{ID: 1, Slug: "people", Name: "People", Schema: &api.DBSchema{
		Columns: []api.DBColumn{{Key: "id", Name: "ID"}, {Key: "name", Name: "Name"}},
	}}
	m.db.rows = []api.DBRow{{ID: 5, Data: map[string]any{"id": 5, "name": "Ada"}}}
	m.db.detailIdx = 0

	y := paneLineOf(m, m.db.detailLines(m.db.currentRow(), m.rects.pane.w), 1)
	step, cmd := m.Update(clickAt(m.rects.pane.x+4, y))
	model := step.(Model)

	if model.db.fieldSel != 1 {
		t.Fatalf("a click must move the field cursor, fieldSel=%d", model.db.fieldSel)
	}
	if cmd != nil || model.db.editOpen {
		t.Fatal("a click must not open the cell editor")
	}
}

// ── overlays ────────────────────────────────────────────────────────────

// paletteRowOf is the index of a filtered palette row by label, so a test says
// what it means instead of counting entries that shift when the list grows.
func paletteRowOf(t *testing.T, m Model, label string) int {
	t.Helper()
	for i, it := range m.pal.filtered {
		if strings.Contains(it.label, label) {
			return i
		}
	}
	t.Fatalf("palette has no row containing %q", label)
	return -1
}

func TestPaletteClickRunsTheItem(t *testing.T) {
	m := mouseModel(t, 100, 30, 2)
	step, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlK})
	open := step.(Model)

	idx := paletteRowOf(t, open, "documents")
	y := paneLineOf(open, open.pal.lines(open.rects.pane.w), idx)
	step, cmd := open.Update(clickAt(open.rects.pane.x+4, y))
	model := step.(Model)

	if model.pal.active {
		t.Fatal("running an item must close the palette, as enter does")
	}
	if model.view != viewDocs || cmd == nil {
		t.Fatalf("a click must run the item, view=%d cmd=%v", model.view, cmd != nil)
	}
}

func TestPaletteClickOnChromeIsInert(t *testing.T) {
	m := mouseModel(t, 100, 30, 2)
	step, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlK})
	open := step.(Model)

	// The title line, above every row.
	step, _ = open.Update(clickAt(open.rects.pane.x+4, open.rects.pane.y+panePadTop))
	if !step.(Model).pal.active {
		t.Fatal("clicking chrome must leave the palette up")
	}
}

func TestSearchResultClickOpensIt(t *testing.T) {
	m := mouseModel(t, 100, 30, 2)
	m.search.active = true
	m.search.ran = true
	m.search.rows = []searchRow{
		{header: "messages"},
		{message: &api.SearchMessage{Message: api.Message{ID: 101, ChannelID: 1, Body: "hello", Sender: api.Sender{Name: "Ada"}}}},
	}

	y := paneLineOf(m, m.search.lines(m.rects.pane.w), 1)
	step, _ := m.Update(clickAt(m.rects.pane.x+4, y))
	model := step.(Model)

	if model.search.active {
		t.Fatal("opening a result must close the search overlay")
	}
	if model.view != viewChat || model.feedSel != 101 {
		t.Fatalf("a click must jump to the hit, view=%d feedSel=%d", model.view, model.feedSel)
	}
}

func TestSearchHeaderClickIsInert(t *testing.T) {
	m := mouseModel(t, 100, 30, 2)
	m.search.active = true
	m.search.ran = true
	m.search.rows = []searchRow{
		{header: "messages"},
		{message: &api.SearchMessage{Message: api.Message{ID: 101, ChannelID: 1, Sender: api.Sender{Name: "Ada"}}}},
	}

	rows := m.search.lines(m.rects.pane.w)
	if rows.lineOf(0) != -1 {
		t.Fatal("a section header must not be a click target")
	}
	// The header still draws a line — clicking it must do nothing.
	headerY := rows.lineOf(1) - 1 + m.rects.pane.y + panePadTop
	step, _ := m.Update(clickAt(m.rects.pane.x+4, headerY))
	if !step.(Model).search.active {
		t.Fatal("clicking a header must leave the overlay up")
	}
}

func TestInboxClickJumpsToTheMessage(t *testing.T) {
	m := mouseModel(t, 100, 30, 2)
	m.focusedID = 0
	m.notify.active = true
	// The notification's message is an anonymous struct in the serializer's
	// shape — decoding a payload is how a test gets one.
	var notification api.Notification
	payload := `{"id":1,"action":"mentioned","message":{"channel_id":1,"channel_slug":"general","preview":"hi"}}`
	if err := json.Unmarshal([]byte(payload), &notification); err != nil {
		t.Fatal(err)
	}
	m.notify.items = []api.Notification{notification}

	y := paneLineOf(m, m.notify.lines(m.rects.pane.w), 0)
	step, _ := m.Update(clickAt(m.rects.pane.x+4, y))
	model := step.(Model)

	if model.notify.active {
		t.Fatal("jumping must close the inbox")
	}
	if model.focusedID != 1 || model.view != viewChat {
		t.Fatalf("a click must open the channel, focusedID=%d view=%d", model.focusedID, model.view)
	}
}

func TestAttachClickInsertsTheEmbed(t *testing.T) {
	m := mouseModel(t, 100, 30, 2)
	m.attach.active = true
	m.attach.results = []api.Upload{
		{ID: 1, Slug: "q4-report", Title: "Q4 Report"},
		{ID: 2, Slug: "logo", Title: "Logo"},
	}

	y := paneLineOf(m, m.attachLines(m.rects.pane.w), 1)
	step, _ := m.Update(clickAt(m.rects.pane.x+4, y))
	model := step.(Model)

	if model.attach.active {
		t.Fatal("inserting must close the attach prompt")
	}
	if !strings.Contains(model.comp.value(), "[[upload:logo]]") {
		t.Fatalf("a click must insert the embed, composer holds %q", model.comp.value())
	}
}

func TestThreadPickerClickOpensTheThread(t *testing.T) {
	m := mouseModel(t, 100, 30, 2)
	m.threadPicker.active = true
	m.threadPicker.threads = []api.Channel{
		{ID: 7, Slug: "t-one", Name: "One", Kind: "thread"},
		{ID: 8, Slug: "t-two", Name: "Two", Kind: "thread"},
	}

	y := paneLineOf(m, m.threadPickerLines(m.rects.pane.w), 1)
	step, _ := m.Update(clickAt(m.rects.pane.x+4, y))
	model := step.(Model)

	if model.threadPicker.active {
		t.Fatal("opening a thread must close the picker")
	}
	if model.focusedID != 8 {
		t.Fatalf("a click must open the clicked thread, focusedID=%d", model.focusedID)
	}
}

func TestEmbedPickerClickFollowsTheReference(t *testing.T) {
	m := mouseModel(t, 100, 30, 2)
	m.embedPicker.active = true
	m.embedPicker.refs = []embedRef{
		{kind: "doc", ref: "spec"},
		{kind: "doc", ref: "notes"},
	}

	y := paneLineOf(m, m.embedPickerLines(m.rects.pane.w), 1)
	step, cmd := m.Update(clickAt(m.rects.pane.x+4, y))
	model := step.(Model)

	if model.embedPicker.active {
		t.Fatal("following must close the picker")
	}
	if cmd == nil {
		t.Fatal("a click must follow the clicked reference")
	}
}

// ── status bar ──────────────────────────────────────────────────────────

// statusHintX is a column inside the named hint, found the way View lays it out.
func statusHintX(t *testing.T, m Model, label string) int {
	t.Helper()
	_, spans := m.statusBarLine()
	for i, hint := range statusHints {
		if !strings.Contains(hint.label, label) {
			continue
		}
		for _, span := range spans {
			if span.key == statusHints[i].key {
				return span.x + 1
			}
		}
		t.Fatalf("hint %q is not clickable", label)
	}
	t.Fatalf("status bar has no hint containing %q", label)
	return -1
}

// The spans are only right if they name the columns the line actually draws.
func TestStatusHintSpansMatchTheRenderedLine(t *testing.T) {
	m := mouseModel(t, 140, 30, 2)

	line, spans := m.statusBarLine()
	if len(spans) == 0 {
		t.Fatal("a 140-column terminal must have room for the hints")
	}
	// Cells, not bytes: the separator's middot is one column and two bytes.
	plain := []rune(ansi.Strip(line))
	for i, hint := range statusHints {
		if !hint.press {
			continue
		}
		span := spans[i]
		if span.x+span.w > len(plain) {
			t.Fatalf("hint %q spans past the line: %d+%d in %d", hint.label, span.x, span.w, len(plain))
		}
		if got := string(plain[span.x : span.x+span.w]); got != hint.label {
			t.Errorf("span for %q covers %q", hint.label, got)
		}
	}
}

func TestStatusHintClickOpensThePalette(t *testing.T) {
	m := mouseModel(t, 140, 30, 2)

	x := statusHintX(t, m, "palette")
	step, _ := m.Update(clickAt(x, m.rects.status.y))
	if !step.(Model).pal.active {
		t.Fatal("clicking the palette hint must open the palette")
	}
}

// The hints name global keys, so they answer from under an overlay exactly as
// the keys do.
func TestStatusHintClickWorksUnderAnOverlay(t *testing.T) {
	m := mouseModel(t, 140, 30, 2)
	step, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlK})
	open := step.(Model)

	x := statusHintX(t, open, "home")
	step, _ = open.Update(clickAt(x, open.rects.status.y))
	model := step.(Model)
	if model.view != viewHome || model.pal.active {
		t.Fatalf("ctrl+h from the status bar must go home and close the palette, view=%d", model.view)
	}
}

// Quit has no undo, so the hint is a label and nothing else.
func TestStatusQuitHintIsNotClickable(t *testing.T) {
	m := mouseModel(t, 140, 30, 2)

	_, spans := m.statusBarLine()
	for _, span := range spans {
		if span.key == tea.KeyCtrlC {
			t.Fatal("the quit hint must not be clickable")
		}
	}
}

func TestNarrowTerminalDropsTheHintsAndTheirSpans(t *testing.T) {
	m := mouseModel(t, 40, 30, 2)

	line, spans := m.statusBarLine()
	if spans != nil {
		t.Fatalf("hints that aren't drawn must not be clickable, got %d spans", len(spans))
	}
	if strings.Contains(ansi.Strip(line), "palette") {
		t.Fatal("the hint group must be dropped when it doesn't fit")
	}
	// A click where the hints would have been must be inert, not a wild press.
	step, cmd := m.Update(clickAt(m.width-4, m.rects.status.y))
	if cmd != nil || step.(Model).pal.active {
		t.Fatal("a click on an empty status bar must do nothing")
	}
}
