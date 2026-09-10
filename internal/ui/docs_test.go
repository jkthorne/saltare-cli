package ui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jkthorne/saltare-cli/internal/api"
)

func TestCtrlOOpensDocsAndEscReturns(t *testing.T) {
	m := testModel(t)
	step, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	if step.(Model).view != viewDocs {
		t.Fatal("ctrl+o must open the docs view")
	}

	step, _ = step.(Model).Update(tea.KeyMsg{Type: tea.KeyEsc})
	if step.(Model).view != viewChat {
		t.Fatal("esc must return to chat")
	}
}

func TestDocsListNavigationAndReader(t *testing.T) {
	m := testModel(t)
	m.view = viewDocs
	m.docs.resize(80, 24)
	m.docs.list = []api.Document{
		{ID: 1, Slug: "handbook", Title: "Handbook", Body: "# Hello\n\nWorld", UpdatedAt: time.Now()},
		{ID: 2, Slug: "notes", Title: "Notes", UpdatedAt: time.Now()},
	}

	step, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	model := step.(Model)
	if doc, _ := model.docs.selected(); doc.Slug != "notes" {
		t.Fatalf("j must move the selection, got %s", doc.Slug)
	}

	doc := model.docs.list[0]
	model.docs.showDocument(&doc)
	if model.docs.viewing == nil {
		t.Fatal("showDocument must open the reader")
	}

	step, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = step.(Model)
	if model.docs.viewing != nil {
		t.Fatal("esc must close the reader back to the list")
	}
	if model.view != viewDocs {
		t.Fatal("closing the reader must stay in docs view")
	}
}

func TestEditorFinishedSkipsNoOpSaves(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := testModel(t)
	m.view = viewDocs

	path := filepath.Join(t.TempDir(), "doc-handbook.md")
	if err := os.WriteFile(path, []byte("unchanged body"), 0o600); err != nil {
		t.Fatal(err)
	}
	m.docs.editSlug = "handbook"
	m.docs.editPath = path
	m.docs.editBody = "unchanged body"

	step, cmd := m.Update(editorFinishedMsg{})
	model := step.(Model)
	if model.docs.editSlug != "" {
		t.Fatal("a no-op edit must clear the edit state")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("a no-op edit must remove the buffer file")
	}
	if cmd == nil {
		t.Fatal("expected the toast command")
	}
}

func TestConflictPromptKeepsFileOnEsc(t *testing.T) {
	m := testModel(t)
	m.view = viewDocs
	m.docs.conflict = &docConflict{slug: "handbook", path: "/tmp/doc-handbook.md", body: "mine"}

	step, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model := step.(Model)
	if model.docs.conflict != nil {
		t.Fatal("esc must dismiss the conflict prompt")
	}
	if cmd == nil {
		t.Fatal("dismissing must toast the preserved path")
	}
	if model.view != viewDocs {
		t.Fatal("dismissing must stay in docs view")
	}
}
