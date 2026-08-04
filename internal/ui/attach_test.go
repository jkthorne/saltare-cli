package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jkthorne/saltare/cli/internal/api"
)

func TestLooksLikePath(t *testing.T) {
	for path, want := range map[string]bool{
		"~/notes.md":  true,
		"~":           true,
		"/tmp/a.pdf":  true,
		"./a.pdf":     true,
		"../a.pdf":    true,
		"notes":       false,
		"q4.pdf":      false,
		"":            false,
		"report 2026": false,
	} {
		if got := looksLikePath(path); got != want {
			t.Fatalf("looksLikePath(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestCtrlYOpensAttachOnlyInChat(t *testing.T) {
	m := testModel(t)
	step, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlY})
	if !step.(Model).attach.active {
		t.Fatal("ctrl+y must open the attach prompt in chat")
	}

	m = testModel(t)
	m.view = viewTasks
	step, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlY})
	if step.(Model).attach.active {
		t.Fatal("ctrl+y must not fire outside chat")
	}
}

func TestAttachStaleResultsDropped(t *testing.T) {
	m := testModel(t)
	step, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlY})
	model := step.(Model)
	model.attach.gen = 5

	step, _ = model.Update(attachResultsMsg{gen: 4, uploads: []api.Upload{{ID: 1, Slug: "old"}}})
	model = step.(Model)
	if len(model.attach.results) != 0 {
		t.Fatal("a stale-generation result set must be ignored")
	}

	step, _ = model.Update(attachResultsMsg{gen: 5, uploads: []api.Upload{{ID: 2, Slug: "fresh"}}})
	model = step.(Model)
	if len(model.attach.results) != 1 || model.attach.results[0].Slug != "fresh" {
		t.Fatalf("the current generation must land, got %+v", model.attach.results)
	}
}

func TestAttachEnterInsertsSelectedUpload(t *testing.T) {
	m := testModel(t)
	step, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlY})
	model := step.(Model)
	model.attach.input.SetValue("q4")
	model.attach.results = []api.Upload{{ID: 1, Slug: "q4-report"}}

	step, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = step.(Model)
	if model.attach.active {
		t.Fatal("enter on a result must close the prompt")
	}
	if got := model.comp.value(); !strings.Contains(got, "[[upload:q4-report]]") {
		t.Fatalf("the embed must land in the composer, got %q", got)
	}
	if !model.comp.ta.Focused() {
		t.Fatal("the composer must be refocused")
	}
}

func TestAttachDoneInsertsAtCursorMidText(t *testing.T) {
	m := testModel(t)
	m.comp.setValue("before  after")
	// setValue leaves the cursor at the end — walk it back to mid-string.
	for i := 0; i < len(" after"); i++ {
		m.comp.ta, _ = m.comp.ta.Update(tea.KeyMsg{Type: tea.KeyLeft})
	}

	step, cmd := m.Update(attachDoneMsg{upload: api.Upload{ID: 1, Slug: "logo"}})
	model := step.(Model)
	got := model.comp.value()
	if got != "before [[upload:logo]] after" {
		t.Fatalf("insertion must be cursor-anchored, not end-anchored: %q", got)
	}
	if cmd == nil {
		t.Fatal("expected the attached toast")
	}
	if model.attach.active {
		t.Fatal("attach must close on completion")
	}
}

func TestAttachPathInputArmsNoSearch(t *testing.T) {
	m := testModel(t)
	step, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlY})
	model := step.(Model)

	var cmd tea.Cmd
	step, cmd = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("~")})
	model = step.(Model)
	if model.attach.loading {
		t.Fatal("path-looking input must not arm the search")
	}
	_ = cmd

	out := model.renderAttach(80, 24)
	if !strings.Contains(out, "enter uploads this file") {
		t.Fatalf("path input must render the upload hint, got %q", out[:min(len(out), 200)])
	}
}

func TestAttachEscRefocusesComposer(t *testing.T) {
	m := testModel(t)
	step, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlY})
	model := step.(Model)
	if model.comp.ta.Focused() {
		t.Fatal("opening attach must blur the composer")
	}
	step, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = step.(Model)
	if model.attach.active || !model.comp.ta.Focused() {
		t.Fatal("esc must close attach and refocus the composer")
	}
}

func TestPaletteAttachJumpsToChat(t *testing.T) {
	m := testModel(t)
	m.view = viewTasks
	step, _ := m.runPaletteItem(paletteItem{action: actionAttach})
	model := step.(Model)
	if model.view != viewChat || !model.attach.active {
		t.Fatalf("palette attach must jump to chat and open the prompt, got view=%d active=%v", model.view, model.attach.active)
	}
}
