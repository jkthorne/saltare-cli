package ui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jkthorne/saltare/cli/internal/api"
)

func seededModel(t *testing.T) Model {
	t.Helper()
	m := testModel(t)
	m.cfg.UserID = 7
	m.store.SetChannels([]api.Channel{{ID: 1, Slug: "general", Name: "General", Kind: "public_channel", Member: true}})
	m.store.MergeHistory(1, []api.Message{
		{ID: 10, ChannelID: 1, Body: "mine", Sender: api.Sender{Type: "User", ID: 7, Name: "Me"}, CreatedAt: time.Now().Add(-time.Minute)},
		{ID: 11, ChannelID: 1, Body: "theirs", Sender: api.Sender{Type: "User", ID: 8, Name: "Other"}, CreatedAt: time.Now()},
	})
	m.focusedID = 1
	m.focus = focusFeed
	return m
}

func TestEditOwnMessagePrefillsComposer(t *testing.T) {
	m := seededModel(t)
	m.feedSel = 10

	step, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	model := step.(Model)
	if model.editing == nil || model.editing.ID != 10 {
		t.Fatalf("e must arm editing, got %+v", model.editing)
	}
	if model.comp.value() != "mine" {
		t.Fatalf("composer must hold the body, got %q", model.comp.value())
	}
	if model.focus != focusComposer {
		t.Fatal("editing must focus the composer")
	}

	step, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = step.(Model)
	if model.editing != nil || model.comp.value() != "" {
		t.Fatalf("esc must cancel the edit, editing=%v value=%q", model.editing, model.comp.value())
	}
}

func TestEditForeignMessageRefused(t *testing.T) {
	m := seededModel(t)
	m.feedSel = 11

	step, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	model := step.(Model)
	if model.editing != nil {
		t.Fatal("editing another user's message must be refused client-side")
	}
	if model.softErr == "" {
		t.Fatal("a refusal must surface a hint")
	}
}

func TestDeleteConfirmInterceptsQuit(t *testing.T) {
	m := seededModel(t)
	m.feedSel = 10

	step, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	model := step.(Model)
	if model.confirmDelete == nil {
		t.Fatal("d must arm the delete confirmation")
	}

	// q normally quits feed mode — while armed it only cancels.
	step, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	model = step.(Model)
	if model.confirmDelete != nil {
		t.Fatal("any key but y must cancel the confirmation")
	}
	if cmd != nil {
		t.Fatal("cancelling the confirmation must not quit")
	}

	step, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	step, cmd = step.(Model).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if step.(Model).confirmDelete != nil {
		t.Fatal("y must disarm the confirmation")
	}
	if cmd == nil {
		t.Fatal("y must dispatch the delete command")
	}
}

func TestMessageDeletedMsgSplicesStore(t *testing.T) {
	m := seededModel(t)
	m.feedSel = 10

	step, _ := m.Update(messageDeletedMsg{channelID: 1, id: 10})
	model := step.(Model)
	if got := len(model.store.Messages(1)); got != 1 {
		t.Fatalf("store must drop the deleted message, %d remain", got)
	}
	if model.feedSel == 10 {
		t.Fatal("selection must leave the deleted message")
	}
}
