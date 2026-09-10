package ui

import (
	"context"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jkthorne/saltare-cli/internal/api"
	"github.com/jkthorne/saltare-cli/internal/config"
)

// testModel is a model already dropped into chat (the pre-home default most
// tests assume). Boot-state tests use bootModel instead.
func testModel(t *testing.T) Model {
	t.Helper()
	m := bootModel(t)
	m.view = viewChat
	_ = m.comp.focus()
	return m
}

// bootModel is the pristine NewModel state: view == viewHome, composer blurred.
func bootModel(t *testing.T) Model {
	t.Helper()
	cfg := &config.Config{ServerURL: "http://example.test", WorkspaceName: "Test", WorkspaceSlug: "test"}
	client, err := api.New(cfg.ServerURL, config.Tokens{AccessToken: "t", RefreshToken: "r"})
	if err != nil {
		t.Fatal(err)
	}
	return NewModel(context.Background(), cfg, client)
}

func TestCtrlKOpensPalette(t *testing.T) {
	m := testModel(t)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlK})
	if !updated.(Model).pal.active {
		t.Fatal("ctrl+k must open the command palette")
	}
}

func TestPaletteFilterAndRun(t *testing.T) {
	m := testModel(t)
	step, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlK})
	step, _ = step.(Model).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("task")})
	pal := step.(Model).pal
	if !pal.active || len(pal.filtered) == 0 {
		t.Fatalf("typing must filter palette items; filtered=%d", len(pal.filtered))
	}
	step, _ = step.(Model).Update(tea.KeyMsg{Type: tea.KeyEnter})
	if step.(Model).view != viewTasks {
		t.Fatal("enter on a tasks action must switch to the tasks view")
	}
	if step.(Model).pal.active {
		t.Fatal("palette must close after running an item")
	}
}
