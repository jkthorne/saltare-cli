package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jkthorne/saltare-cli/internal/api"
)

func tasksModel(t *testing.T) Model {
	t.Helper()
	m := testModel(t)
	m.view = viewTasks
	desc := "Fix the login flow"
	slug := "task-fix-login-abc"
	m.tasks.tasks = []api.Task{
		{ID: 1, Slug: "fix-login-abc", Title: "Fix login", State: "open", Description: &desc, DiscussionChannelSlug: &slug},
		{ID: 2, Slug: "ship-docs-def", Title: "Ship docs", State: "in_progress"},
	}
	return m
}

func TestEnterOpensTaskDetailAndEscCloses(t *testing.T) {
	m := tasksModel(t)

	step, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model := step.(Model)
	if model.tasks.detail == nil || model.tasks.detail.Slug != "fix-login-abc" {
		t.Fatalf("enter must open the selected task's detail, got %+v", model.tasks.detail)
	}

	step, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = step.(Model)
	if model.tasks.detail != nil {
		t.Fatal("esc must close the detail back to the list")
	}
	if model.view != viewTasks {
		t.Fatal("closing the detail must stay in the tasks view")
	}
}

func TestDetailDiscussionKeyDispatches(t *testing.T) {
	m := tasksModel(t)
	task := m.tasks.tasks[0]
	m.tasks.detail = &task

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("o")})
	if cmd == nil {
		t.Fatal("o must dispatch the discussion find-or-create command")
	}
}

func TestDiscussionLoadedSwitchesToChat(t *testing.T) {
	m := tasksModel(t)
	task := m.tasks.tasks[0]
	m.tasks.detail = &task

	channel := api.Channel{ID: 40, Slug: "task-fix-login-abc", Name: "task-fix-login-abc", Kind: "discussion", Member: true}
	step, _ := m.Update(discussionLoadedMsg{channel})
	model := step.(Model)
	if model.view != viewChat {
		t.Fatal("opening the discussion must switch to chat")
	}
	if model.focusedID != 40 {
		t.Fatalf("the discussion channel must be focused, got %d", model.focusedID)
	}
	if model.tasks.detail != nil {
		t.Fatal("the detail pane must close")
	}
}

func TestNextTaskStateCycle(t *testing.T) {
	want := map[string]string{
		"open": "in_progress", "in_progress": "waiting", "waiting": "completed",
		"completed": "open", "cancelled": "open",
	}
	for from, to := range want {
		if got := nextTaskState(from); got != to {
			t.Fatalf("nextTaskState(%s) = %s, want %s", from, got, to)
		}
	}
}
