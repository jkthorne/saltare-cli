package ui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jkthorne/saltare/cli/internal/api"
)

func agendaFixture() []api.Task {
	today := time.Now().Format("2006-01-02")
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	return []api.Task{
		{ID: 1, Slug: "late", Title: "Late", State: "open", DueDate: &yesterday},
		{ID: 2, Slug: "now", Title: "Now", State: "open", DueDate: &today},
	}
}

func TestPaletteAgendaEntersAgendaMode(t *testing.T) {
	m := testModel(t)
	step, cmd := m.runPaletteItem(paletteItem{action: actionAgenda})
	model := step.(Model)
	if model.view != viewTasks || !model.tasks.agenda || !model.tasks.mine {
		t.Fatalf("agenda action must open the tasks pane in agenda mode, got view=%d agenda=%v", model.view, model.tasks.agenda)
	}
	if cmd == nil {
		t.Fatal("agenda action must dispatch the fetch")
	}
}

func TestAgendaRowsSkipHeaders(t *testing.T) {
	m := testModel(t)
	step, _ := m.runPaletteItem(paletteItem{action: actionAgenda})
	model := step.(Model)

	step, _ = model.Update(tasksLoadedMsg{gen: model.tasks.fetchGen, tasks: agendaFixture()})
	model = step.(Model)
	// overdue header, late, today header, now
	if len(model.tasks.rows) != 4 {
		t.Fatalf("expected 4 rows, got %d", len(model.tasks.rows))
	}
	if !model.tasks.rows[model.tasks.sel].selectable() {
		t.Fatal("cursor must land on a task row, not a header")
	}
	if task, ok := model.tasks.selected(); !ok || task.Slug != "late" {
		t.Fatalf("first selectable row must be the overdue task, got %+v", task)
	}

	step, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	model = step.(Model)
	if task, ok := model.tasks.selected(); !ok || task.Slug != "now" {
		t.Fatalf("j must skip the header between sections, got %+v", task)
	}
}

func TestAgendaStaleFetchDropped(t *testing.T) {
	m := testModel(t)
	step, _ := m.runPaletteItem(paletteItem{action: actionAgenda})
	model := step.(Model)

	step, _ = model.Update(tasksLoadedMsg{gen: model.tasks.fetchGen - 1, tasks: agendaFixture()})
	model = step.(Model)
	if len(model.tasks.tasks) != 0 || !model.tasks.loading {
		t.Fatal("a stale-generation response must be ignored")
	}
}

func TestAgendaEnterOpensDetail(t *testing.T) {
	m := testModel(t)
	step, _ := m.runPaletteItem(paletteItem{action: actionAgenda})
	step, _ = step.(Model).Update(tasksLoadedMsg{gen: step.(Model).tasks.fetchGen, tasks: agendaFixture()})
	step, _ = step.(Model).Update(tea.KeyMsg{Type: tea.KeyEnter})
	model := step.(Model)
	if model.tasks.detail == nil || model.tasks.detail.Slug != "late" {
		t.Fatalf("enter must open the selected task's detail, got %+v", model.tasks.detail)
	}
}

func TestTasksListActionsClearAgendaMode(t *testing.T) {
	m := testModel(t)
	step, _ := m.runPaletteItem(paletteItem{action: actionAgenda})
	step, _ = step.(Model).runPaletteItem(paletteItem{action: actionTasksMine})
	if step.(Model).tasks.agenda {
		t.Fatal("the plain task-list actions must leave agenda mode")
	}
}

func TestAgendaMKeyIsNoOp(t *testing.T) {
	m := testModel(t)
	step, _ := m.runPaletteItem(paletteItem{action: actionAgenda})
	step, _ = step.(Model).Update(tasksLoadedMsg{gen: step.(Model).tasks.fetchGen, tasks: agendaFixture()})
	step, cmd := step.(Model).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("m")})
	model := step.(Model)
	if !model.tasks.agenda || !model.tasks.mine || cmd != nil {
		t.Fatal("m must not toggle scope while in agenda mode")
	}
}
