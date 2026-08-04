package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/jkthorne/saltare/cli/internal/api"
)

func twoChannelModel(t *testing.T) Model {
	t.Helper()
	t.Setenv("HOME", t.TempDir()) // drafts.json must not touch the real config dir
	m := testModel(t)
	m.cfg.UserID = 7
	channels := []api.Channel{
		{ID: 1, Slug: "general", Name: "General", Kind: "public_channel", Member: true},
		{ID: 2, Slug: "random", Name: "Random", Kind: "public_channel", Member: true},
	}
	m.store.SetChannels(channels)
	m.items = buildSidebar(m.store.Channels(), nil)
	m.focusedID = 1
	return m
}

func TestDraftSurvivesChannelSwitch(t *testing.T) {
	m := twoChannelModel(t)
	m.comp.setValue("half-typed thought")

	m.selected = 1 // switch to random
	step, _ := m.openSelected(false)
	model := step.(Model)
	if got := model.comp.value(); got != "" {
		t.Fatalf("random must start with an empty composer, got %q", got)
	}

	model.selected = 0 // back to general
	step, _ = model.openSelected(false)
	model = step.(Model)
	if got := model.comp.value(); got != "half-typed thought" {
		t.Fatalf("the general draft must come back, got %q", got)
	}
}

func TestEditPreservesAndRestoresDraft(t *testing.T) {
	m := twoChannelModel(t)
	m.store.MergeHistory(1, []api.Message{
		{ID: 10, ChannelID: 1, Body: "posted", Sender: api.Sender{Type: "User", ID: 7, Name: "Me"}, CreatedAt: time.Now()},
	})
	m.focus = focusFeed
	m.feedSel = 10
	m.comp.setValue("draft in progress")

	step, _ := m.beginEdit()
	model := step.(Model)
	if got := model.comp.value(); got != "posted" {
		t.Fatalf("editing must show the message body, got %q", got)
	}

	step, _ = model.handleEsc()
	model = step.(Model)
	if got := model.comp.value(); got != "draft in progress" {
		t.Fatalf("cancelling the edit must restore the draft, got %q", got)
	}
}

func TestUnreadSeparatorPosition(t *testing.T) {
	m := twoChannelModel(t)
	cursor := time.Now().Add(-10 * time.Minute)
	unread := 2
	channel := api.Channel{ID: 3, Slug: "busy", Name: "Busy", Kind: "public_channel", Member: true, LastReadAt: &cursor, UnreadCount: &unread}
	m.store.Upsert(channel)
	m.store.MergeHistory(3, []api.Message{
		{ID: 20, ChannelID: 3, Body: "read already", Sender: api.Sender{Type: "User", ID: 8, Name: "Other"}, CreatedAt: cursor.Add(-time.Hour)},
		{ID: 21, ChannelID: 3, Body: "my own late reply", Sender: api.Sender{Type: "User", ID: 7, Name: "Me"}, CreatedAt: cursor.Add(time.Minute)},
		{ID: 22, ChannelID: 3, Body: "fresh from other", Sender: api.Sender{Type: "User", ID: 8, Name: "Other"}, CreatedAt: cursor.Add(2 * time.Minute)},
	})

	m.snapshotUnread(channel)
	m.focusedID = 3
	if got := m.firstUnreadID(); got != 22 {
		t.Fatalf("the rule belongs above the first foreign unread, got %d", got)
	}

	content, _ := m.renderer.Render(m.store.Messages(3), 0, 22)
	if !strings.Contains(content, "NEW") {
		t.Fatal("the rendered feed must contain the NEW rule")
	}
	lines := strings.Split(content, "\n")
	newIdx, freshIdx := -1, -1
	for i, l := range lines {
		if strings.Contains(l, "NEW") {
			newIdx = i
		}
		// glamour interleaves ANSI codes mid-sentence; match a short run.
		if strings.Contains(l, "fresh from") {
			freshIdx = i
		}
	}
	if newIdx == -1 || freshIdx == -1 || newIdx > freshIdx {
		t.Fatalf("NEW rule (line %d) must sit above the unread message (line %d)", newIdx, freshIdx)
	}

	// A channel with no unreads never snapshots a mark.
	m.snapshotUnread(api.Channel{ID: 3, Member: true, LastReadAt: &cursor})
	if _, ok := m.unreadMark[3]; ok {
		t.Fatal("no unreads → no mark")
	}
}
