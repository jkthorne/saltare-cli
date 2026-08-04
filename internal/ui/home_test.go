package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jkthorne/saltare/cli/internal/api"
	"github.com/jkthorne/saltare/cli/internal/cable"
)

func unreadChannel(id int64, slug string, unread int) api.Channel {
	lastRead := time.Now().Add(-time.Hour)
	count := unread
	return api.Channel{
		ID: id, Slug: slug, Name: slug, Kind: "public_channel",
		Member: true, LastReadAt: &lastRead, UnreadCount: &count,
	}
}

func TestBootLandsOnHome(t *testing.T) {
	m := bootModel(t)
	if m.view != viewHome {
		t.Fatalf("boot must land on home, got view=%d", m.view)
	}

	step, _ := m.Update(channelsLoadedMsg{channels: []api.Channel{unreadChannel(1, "general", 2)}})
	model := step.(Model)
	if model.view != viewHome {
		t.Fatal("channelsLoaded must not flip the view off home")
	}
	if model.loadingMsg != "" || model.focusedID != 1 {
		t.Fatalf("the feed must still warm behind home, loadingMsg=%q focusedID=%d", model.loadingMsg, model.focusedID)
	}
	if len(model.unreadMark) != 0 {
		t.Fatal("boot on home must not snapshot the read cursor yet (markRead is deferred)")
	}
}

func TestHomeEscSnapshotsAndEntersChat(t *testing.T) {
	m := bootModel(t)
	ch := unreadChannel(1, "general", 2)
	step, _ := m.Update(channelsLoadedMsg{channels: []api.Channel{ch}})
	step, cmd := step.(Model).Update(tea.KeyMsg{Type: tea.KeyEsc})
	model := step.(Model)
	if model.view != viewChat {
		t.Fatalf("esc must drop into chat, got view=%d", model.view)
	}
	if cmd == nil {
		t.Fatal("leaving home must dispatch the deferred markRead")
	}
	mark, ok := model.unreadMark[1]
	if !ok || !mark.Equal(*ch.LastReadAt) {
		t.Fatalf("the NEW rule must be armed from the pre-markRead cursor, got %v ok=%v", mark, ok)
	}
}

func TestHomeNotifCountDoesNotOpenOverlay(t *testing.T) {
	m := bootModel(t)
	step, _ := m.Update(notifCountMsg{count: 3})
	model := step.(Model)
	if model.notify.active {
		t.Fatal("the home badge fetch must not pop the notifications overlay")
	}
	if model.home.notifCount != 3 || !model.home.notifLoaded {
		t.Fatalf("count must land on home, got %d loaded=%v", model.home.notifCount, model.home.notifLoaded)
	}
}

func TestHomeEnterOnChannelRowJumps(t *testing.T) {
	m := bootModel(t)
	step, _ := m.Update(channelsLoadedMsg{channels: []api.Channel{unreadChannel(1, "general", 2)}})
	step, _ = step.(Model).Update(notifCountMsg{count: 0})
	model := step.(Model)

	// Row 0 is the dim "no unread notifications" line — j lands on the channel.
	step, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	step, cmd := step.(Model).Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = step.(Model)
	if model.view != viewChat {
		t.Fatalf("enter on a channel row must jump to chat, got view=%d", model.view)
	}
	if cmd == nil {
		t.Fatal("the focused-channel jump must dispatch the deferred markRead")
	}
	if _, ok := model.unreadMark[1]; !ok {
		t.Fatal("jumping into the boot channel must arm the NEW rule")
	}
}

func TestHomeEnterOnTaskRowOpensDetailAndReturnsHome(t *testing.T) {
	m := bootModel(t)
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	m.tasks.tasks = []api.Task{{ID: 7, Slug: "late", Title: "Late", State: "open", DueDate: &yesterday}}
	m.home.notifLoaded = true

	step, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	step, _ = step.(Model).Update(tea.KeyMsg{Type: tea.KeyEnter})
	model := step.(Model)
	if model.view != viewTasks || model.tasks.detail == nil || model.tasks.detail.Slug != "late" {
		t.Fatalf("enter on a task row must open its detail, got view=%d detail=%+v", model.view, model.tasks.detail)
	}
	if !model.tasks.returnHome {
		t.Fatal("a home-launched detail must remember to return home")
	}

	step, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = step.(Model)
	if model.view != viewHome || model.tasks.detail != nil || model.tasks.returnHome {
		t.Fatalf("esc must return to home, got view=%d", model.view)
	}
}

func TestHomeSections(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	str := func(s string) *string { return &s }
	tasks := []api.Task{
		{ID: 1, State: "open", DueDate: str("2026-08-01")},      // overdue
		{ID: 2, State: "open", DueDate: str("2026-08-04")},      // today
		{ID: 3, State: "open", DueDate: str("2026-08-10")},      // week edge (today+6)
		{ID: 4, State: "open", DueDate: str("2026-08-11")},      // beyond
		{ID: 5, State: "completed", DueDate: str("2026-08-04")}, // done
		{ID: 6, State: "open"},                                  // undated
	}
	overdue, today, week := homeSections(tasks, now)
	if len(overdue) != 1 || overdue[0].ID != 1 {
		t.Fatalf("overdue: %+v", overdue)
	}
	if len(today) != 1 || today[0].ID != 2 {
		t.Fatalf("today: %+v", today)
	}
	if len(week) != 1 || week[0].ID != 3 {
		t.Fatalf("week must include today+6 and drop later/done/undated: %+v", week)
	}
}

func TestPaletteHomeAction(t *testing.T) {
	m := testModel(t)
	step, cmd := m.runPaletteItem(paletteItem{action: actionHome})
	model := step.(Model)
	if model.view != viewHome {
		t.Fatalf("home action must open home, got view=%d", model.view)
	}
	if cmd == nil {
		t.Fatal("home entry must refresh tasks and the notification count")
	}
}

func TestCtrlHReturnsHome(t *testing.T) {
	m := testModel(t)
	step, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlH})
	model := step.(Model)
	if model.view != viewHome {
		t.Fatalf("ctrl+h must return home, got view=%d", model.view)
	}
	if cmd == nil {
		t.Fatal("home entry must refresh tasks and the notification count")
	}

	// From an overlay too: ctrl+h closes it on the way home.
	m = testModel(t)
	step, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlK})
	step, _ = step.(Model).Update(tea.KeyMsg{Type: tea.KeyCtrlH})
	model = step.(Model)
	if model.view != viewHome || model.pal.active {
		t.Fatalf("ctrl+h from the palette must close it and go home, got view=%d pal=%v", model.view, model.pal.active)
	}
}

// Regression (caught live): the cable handler used to treat "focused" as
// "on screen" — a message landing in the warm boot channel while home was
// up cleared its unread and advanced the server read cursor.
func TestCableEventOnHomeKeepsFocusedChannelUnread(t *testing.T) {
	m := bootModel(t)
	step, _ := m.Update(channelsLoadedMsg{channels: []api.Channel{unreadChannel(1, "general", 2)}})
	model := step.(Model)

	ev := cable.Event{Type: cable.EventMessageCreated, ChannelID: 1,
		Message: &api.Message{ID: 99, ChannelID: 1, Body: "ping", CreatedAt: time.Now()}}
	step, cmd := model.Update(cableEventMsg{ev: ev})
	model = step.(Model)
	if got := model.store.Unread(1); got != 3 {
		t.Fatalf("a message arriving while on home must increment unread, got %d", got)
	}
	if cmd != nil {
		t.Fatal("no markRead may fire while the feed is hidden")
	}

	// In chat the same event is consumed: unread clears, markRead fires.
	step, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	chat := step.(Model)
	ev.Message = &api.Message{ID: 100, ChannelID: 1, Body: "pong", CreatedAt: time.Now()}
	step, cmd = chat.Update(cableEventMsg{ev: ev})
	if got := step.(Model).store.Unread(1); got != 0 {
		t.Fatalf("in chat the focused feed consumes the message, got unread=%d", got)
	}
	if cmd == nil {
		t.Fatal("in chat the arrival must advance the server read cursor")
	}
}

// Companion regression: with a clean boot (serializer count 0), a message
// arriving during home lives only in the store's unread map — the NEW rule
// must still arm from it when dropping into chat.
func TestHomeArrivalWithCleanBootStillArmsNewRule(t *testing.T) {
	m := bootModel(t)
	ch := unreadChannel(1, "general", 0)
	step, _ := m.Update(channelsLoadedMsg{channels: []api.Channel{ch}})
	model := step.(Model)

	ev := cable.Event{Type: cable.EventMessageCreated, ChannelID: 1,
		Message: &api.Message{ID: 99, ChannelID: 1, Body: "ping", CreatedAt: time.Now()}}
	step, _ = model.Update(cableEventMsg{ev: ev})
	step, _ = step.(Model).Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = step.(Model)
	if model.view != viewChat {
		t.Fatalf("esc must enter chat, got view=%d", model.view)
	}
	mark, ok := model.unreadMark[1]
	if !ok || !mark.Equal(*ch.LastReadAt) {
		t.Fatalf("a cable-only unread must arm the NEW rule from the stale cursor, got %v ok=%v", mark, ok)
	}
}

func TestHomeRenderSmoke(t *testing.T) {
	m := bootModel(t)
	m.home.notifCount = 2
	m.home.notifLoaded = true
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	m.tasks.tasks = []api.Task{{ID: 7, Slug: "late", Title: "Late task", State: "open", DueDate: &yesterday}}

	out := m.renderHome(80, 24)
	for _, want := range []string{"⌂ home", "2 unread notifications", "overdue", "Late task"} {
		if !strings.Contains(out, want) {
			t.Fatalf("render missing %q", want)
		}
	}
}
