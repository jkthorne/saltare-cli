package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/jkthorne/saltare-cli/internal/api"
	"github.com/jkthorne/saltare-cli/internal/store"
)

func navIndex(items []sidebarItem, key string) int {
	for i, it := range items {
		if it.nav == key {
			return i
		}
	}
	return -1
}

func lineOf(lines []string, want string) int {
	for i, l := range lines {
		if strings.Contains(l, want) {
			return i
		}
	}
	return -1
}

// rowsFrom makes each line its own item, so a windowed builder's targets can be
// checked against the line letters.
func rowsFrom(lines []string) *rowBuilder {
	b := &rowBuilder{}
	for i, l := range lines {
		b.row(l, i)
	}
	return b
}

func alphabet(n int) []string {
	var lines []string
	for i := 0; i < n; i++ {
		lines = append(lines, string(rune('a'+i)))
	}
	return lines
}

func TestSidebarWindowKeepsCursorVisible(t *testing.T) {
	visible := sidebarWindow(rowsFrom(alphabet(20)), 15, 10)
	if visible.len() != 10 {
		t.Fatalf("window must fill the available height; got %d lines", visible.len())
	}
	if lineOf(visible.lines, "p") < 0 { // lines[15]
		t.Fatal("the cursor's row must be inside the window")
	}
	if !strings.Contains(visible.lines[0], "↑ 8 more") {
		t.Fatalf("hidden rows above must be counted; got %q", visible.lines[0])
	}
	if !strings.Contains(visible.lines[9], "↓ 2 more") {
		t.Fatalf("hidden rows below must be counted; got %q", visible.lines[9])
	}
}

// A "more" marker covers a row, so it must not inherit that row's click target —
// otherwise clicking the count opens whatever channel it is hiding.
func TestSidebarWindowMoreMarkersAreNotClickable(t *testing.T) {
	visible := sidebarWindow(rowsFrom(alphabet(20)), 15, 10)
	if got := visible.target(0); got != noTarget {
		t.Fatalf("the ↑ marker must have no target; got %d", got)
	}
	if got := visible.target(9); got != noTarget {
		t.Fatalf("the ↓ marker must have no target; got %d", got)
	}
	// The window starts at line 8, so its second visible line is item 9.
	if got := visible.target(1); got != 9 {
		t.Fatalf("windowed rows must keep their item index; got %d", got)
	}
}

func TestSidebarWindowNeverCoversTheCursor(t *testing.T) {
	// Cursor on the last row: the window bottoms out, so no "more" count may
	// take its line.
	visible := sidebarWindow(rowsFrom(alphabet(20)), 19, 10)
	if got := visible.lines[9]; got != "t" {
		t.Fatalf("last row must survive the window; got %q", got)
	}
	if got := visible.target(9); got != 19 {
		t.Fatalf("the cursor row must stay clickable; got %d", got)
	}
	// A one-row window degenerates to the cursor alone rather than an indicator.
	if one := sidebarWindow(rowsFrom(alphabet(20)), 5, 1); one.len() != 1 || one.lines[0] != "f" {
		t.Fatalf("single-row window must show the cursor; got %v", one.lines)
	}
}

func TestSidebarWindowPassesShortListsThrough(t *testing.T) {
	lines := []string{"a", "b", "c"}
	if got := sidebarWindow(rowsFrom(lines), 1, 10); got.len() != 3 {
		t.Fatalf("a list that fits must not scroll; got %v", got.lines)
	}
	if got := sidebarWindow(rowsFrom(lines), 1, 0); got.len() != 0 {
		t.Fatalf("no height means no rows; got %v", got.lines)
	}
}

func TestRecentGroupHoldsOpenedThreads(t *testing.T) {
	parentID := int64(1)
	older := time.Now().Add(-time.Hour)
	channels := []api.Channel{
		{ID: 1, Slug: "general", Name: "General", Kind: "public_channel", Member: true},
		{ID: 2, Slug: "thread-old", Name: "Old thread", Kind: "thread", ParentChannelID: &parentID, UpdatedAt: older},
		{ID: 3, Slug: "thread-new", Name: "New thread", Kind: "thread", ParentChannelID: &parentID, UpdatedAt: time.Now()},
		{ID: 4, Slug: "task-talk", Name: "Task discussion", Kind: "discussion", UpdatedAt: older.Add(-time.Hour)},
	}

	items := buildSidebar(channels, nil)
	var recent []string
	for _, it := range items {
		if it.group() == "RECENT" {
			recent = append(recent, it.channel.Slug)
		}
	}
	want := []string{"thread-new", "thread-old", "task-talk"}
	if len(recent) != len(want) {
		t.Fatalf("threads and discussions must reach the sidebar; got %v", recent)
	}
	for i := range want {
		if recent[i] != want[i] {
			t.Fatalf("RECENT must run freshest first; got %v want %v", recent, want)
		}
	}
}

func TestFirstUnreadItemSkipsTheRail(t *testing.T) {
	s := store.New()
	three := 3
	s.SetChannels([]api.Channel{
		{ID: 1, Slug: "general", Name: "General", Kind: "public_channel", Member: true},
		{ID: 2, Slug: "random", Name: "Random", Kind: "public_channel", Member: true, UnreadCount: &three},
	})
	items := append(navItems(), buildSidebar(s.Channels(), nil)...)

	idx := firstUnreadItem(s, items)
	if items[idx].channel == nil || items[idx].channel.ID != 2 {
		t.Fatalf("boot cursor must land on the unread channel, not a rail row (idx %d)", idx)
	}

	// With everything read it still has to pick a channel, never the rail.
	s.ClearUnread(2)
	idx = firstUnreadItem(s, items)
	if items[idx].channel == nil {
		t.Fatalf("boot cursor must be a channel row; got rail row %q", items[idx].nav)
	}
}

func TestHasConversation(t *testing.T) {
	if hasConversation(navItems()) {
		t.Fatal("the rail alone is not a conversation — boot must still fail loudly")
	}
	items := append(navItems(), sidebarItem{agent: &api.Agent{Name: "Ada", Status: "active"}})
	if !hasConversation(items) {
		t.Fatal("an agent stub is an openable target")
	}
}

// railModel is a chat model with two channels loaded and the sidebar focused.
func railModel(t *testing.T, unread *int) Model {
	t.Helper()
	m := testModel(t)
	m.store.SetChannels([]api.Channel{
		{ID: 1, Slug: "general", Name: "General", Kind: "public_channel", Member: true},
		{ID: 2, Slug: "random", Name: "Random", Kind: "public_channel", Member: true, UnreadCount: unread},
	})
	m.rebuildSidebar()
	m.focus = focusSidebar
	m.selected = len(navItems()) // the first channel row sits under the rail
	return m
}

func TestRailEnterSwitchesMode(t *testing.T) {
	m := railModel(t, nil)
	m.selected = navIndex(m.items, navDocs)

	step, _ := m.openSelected(true)
	if step.(Model).view != viewDocs {
		t.Fatalf("enter on the documents rail row must open docs; view=%v", step.(Model).view)
	}

	m.selected = navIndex(m.items, navTasks)
	step, _ = m.openSelected(true)
	model := step.(Model)
	if model.view != viewTasks || !model.tasks.mine {
		t.Fatalf("enter on the my-work row must open the mine tasks pane; view=%v mine=%v", model.view, model.tasks.mine)
	}
}

func TestCursorOntoRailKeepsTheOpenChannel(t *testing.T) {
	m := railModel(t, nil)
	step, _ := m.openSelected(false)
	model := step.(Model)
	if model.focusedID != 1 {
		t.Fatalf("setup: expected channel 1 open, got %d", model.focusedID)
	}

	step, _ = model.moveSelection(-1) // up, off the channel list onto the rail
	model = step.(Model)
	if !model.items[model.selected].isNav() {
		t.Fatal("moving up from the first channel must land on a rail row")
	}
	if model.focusedID != 1 {
		t.Fatalf("passing the cursor over the rail must not switch channels; focused=%d", model.focusedID)
	}
	if model.view != viewChat {
		t.Fatalf("passing the cursor over the rail must not switch mode; view=%v", model.view)
	}
}

func TestJumpUnread(t *testing.T) {
	three := 3
	m := railModel(t, &three)

	step, _ := m.jumpUnread(1)
	model := step.(Model)
	if model.focusedID != 2 {
		t.Fatalf("n must jump to the unread channel; focused=%d", model.focusedID)
	}

	// Nothing unread left (opening it marked it read locally): report, don't move.
	model.store.ClearUnread(2)
	step, _ = model.jumpUnread(1)
	after := step.(Model)
	if after.selected != model.selected {
		t.Fatalf("with no unread the cursor must hold still; %d → %d", model.selected, after.selected)
	}
	if after.toast == "" {
		t.Fatal("with no unread the jump must say so")
	}
}

func TestActiveNavKeyTracksTheScreen(t *testing.T) {
	m := railModel(t, nil)
	if got := m.activeNavKey(); got != "" {
		t.Fatalf("chat has no rail row; got %q", got)
	}
	m.view = viewFiles
	if got := m.activeNavKey(); got != navFiles {
		t.Fatalf("files view must light its rail row; got %q", got)
	}
	m.notify.active = true
	if got := m.activeNavKey(); got != navInbox {
		t.Fatalf("the inbox overlay outranks the view behind it; got %q", got)
	}
}

func TestRailRowRendersActiveAndBadged(t *testing.T) {
	s := store.New()
	s.SetChannels([]api.Channel{{ID: 1, Slug: "general", Name: "General", Kind: "public_channel"}})
	st := sidebarState{
		workspace: "Test",
		items:     append(navItems(), buildSidebar(s.Channels(), nil)...),
		selected:  navIndex(navItems(), navHome),
		activeNav: navDocs,
		counts:    navCounts{inbox: 4, overdue: 2},
		height:    40,
	}

	body := sidebarBody(s, st)
	lines := body.lines
	if body.lineOf(st.selected) < 0 {
		t.Fatal("the cursor line must be findable for the scroll window")
	}
	if i := lineOf(lines, "inbox"); i < 0 || !strings.Contains(lines[i], "4") {
		t.Fatalf("inbox row must carry the unread count; got %q", lines[max(i, 0)])
	}
	if i := lineOf(lines, "my work"); i < 0 || !strings.Contains(lines[i], "2") {
		t.Fatalf("my-work row must carry the overdue count; got %q", lines[max(i, 0)])
	}
	if lineOf(lines, "CHANNELS") < 0 {
		t.Fatal("the channel group label must survive the rail")
	}
	if lineOf(lines, "# General") < 0 {
		t.Fatal("channels must still render below the rail")
	}
}
