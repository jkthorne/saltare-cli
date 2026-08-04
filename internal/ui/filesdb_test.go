package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jkthorne/saltare/cli/internal/api"
)

func TestFilesViewNavigationAndConfirm(t *testing.T) {
	m := testModel(t)
	m.view = viewFiles
	m.files.list = []api.Upload{
		{ID: 1, Slug: "q4-report", Title: "Q4 Report", FileSize: 2048},
		{ID: 2, Slug: "logo", Title: "Logo", FileSize: 512},
	}

	step, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	model := step.(Model)
	if u, _ := model.files.selected(); u.Slug != "logo" {
		t.Fatalf("j must move selection, got %s", u.Slug)
	}

	// x arms the delete confirm; any key but y cancels (q must not quit).
	step, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	model = step.(Model)
	if model.files.confirmRm == nil || model.files.confirmRm.Slug != "logo" {
		t.Fatalf("x must arm the confirm, got %+v", model.files.confirmRm)
	}
	step, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	model = step.(Model)
	if model.files.confirmRm != nil || cmd != nil {
		t.Fatal("any key but y must cancel without quitting")
	}

	step, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	step, cmd = step.(Model).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if step.(Model).files.confirmRm != nil || cmd == nil {
		t.Fatal("y must dispatch the delete")
	}

	step, _ = step.(Model).Update(uploadRemovedMsg{slug: "logo"})
	model = step.(Model)
	if len(model.files.list) != 1 || model.files.list[0].Slug != "q4-report" {
		t.Fatalf("removal must splice the list, got %+v", model.files.list)
	}
}

func TestFilesPendingSelectAfterLoad(t *testing.T) {
	m := testModel(t)
	m.view = viewFiles
	m.files.pendingSelect = "logo"

	step, _ := m.Update(uploadsLoadedMsg{uploads: []api.Upload{
		{ID: 1, Slug: "q4-report"}, {ID: 2, Slug: "logo"},
	}})
	model := step.(Model)
	if u, _ := model.files.selected(); u.Slug != "logo" {
		t.Fatalf("pending slug must be selected, got %s", u.Slug)
	}

	model.files.pendingSelect = "gone"
	step, _ = model.Update(uploadsLoadedMsg{uploads: []api.Upload{{ID: 1, Slug: "q4-report"}}})
	if step.(Model).softErr == "" {
		t.Fatal("a missing pending slug must surface a hint")
	}
}

func TestFilesUploadPromptExpandsAndValidates(t *testing.T) {
	m := testModel(t)
	m.view = viewFiles

	step, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("u")})
	model := step.(Model)
	if !model.files.inputOpen {
		t.Fatal("u must open the path prompt")
	}
	model.files.input.SetValue("/definitely/not/a/real/path.bin")
	step, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = step.(Model)
	if model.softErr == "" {
		t.Fatal("a missing path must surface an error")
	}
}

func dbGridModel(t *testing.T) Model {
	t.Helper()
	m := testModel(t)
	m.view = viewDB
	m.db.level = dbLevelGrid
	db := api.Database{ID: 1, Slug: "crm", Name: "CRM", RowsCount: 2, Schema: &api.DBSchema{Columns: []api.DBColumn{
		{Key: "name", Type: "text"},
		{Key: "deal_size", Type: "number"},
		{Key: "score", Type: "formula"},
	}}}
	m.db.database = &db
	m.db.rows = []api.DBRow{
		{ID: 10, Data: map[string]any{"name": "Acme", "deal_size": float64(50000)}},
		{ID: 11, Data: map[string]any{"name": "Globex", "deal_size": float64(100)}},
	}
	m.db.resize(80, 24)
	m.db.buildGrid()
	return m
}

func TestDBGridDetailAndEdit(t *testing.T) {
	m := dbGridModel(t)

	step, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model := step.(Model)
	if model.db.level != dbLevelDetail || model.db.detailIdx != 0 {
		t.Fatalf("enter must open the first row's detail, got level=%d idx=%d", model.db.level, model.db.detailIdx)
	}

	// Edit the second field (number column).
	step, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	step, _ = step.(Model).Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = step.(Model)
	if !model.db.editOpen || model.db.editKey != "deal_size" {
		t.Fatalf("enter on a field must open the edit, got %+v key=%q", model.db.editOpen, model.db.editKey)
	}
	if model.db.input.Value() != "50000" {
		t.Fatalf("edit input must prefill the rendered cell, got %q", model.db.input.Value())
	}

	model.db.input.SetValue("75000")
	step, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = step.(Model)
	if model.db.editOpen || cmd == nil {
		t.Fatal("saving must close the edit and dispatch the PATCH")
	}

	step, _ = model.Update(rowUpdatedMsg{row: api.DBRow{ID: 10, Data: map[string]any{"name": "Acme", "deal_size": float64(75000)}}})
	model = step.(Model)
	if model.db.rows[0].Data["deal_size"] != float64(75000) {
		t.Fatalf("the saved row must replace the local one, got %+v", model.db.rows[0].Data)
	}
}

func TestDBFormulaFieldsRefuseEditing(t *testing.T) {
	m := dbGridModel(t)
	m.db.level = dbLevelDetail
	m.db.detailIdx = 0
	m.db.fieldSel = 2 // the formula column

	step, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model := step.(Model)
	if model.db.editOpen {
		t.Fatal("formula columns must not open the editor")
	}
	if cmd == nil {
		t.Fatal("expected the computed-column toast")
	}
}

func TestDBColumnPanningClamps(t *testing.T) {
	m := dbGridModel(t)

	step, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("h")})
	model := step.(Model)
	if model.db.colOff != 0 {
		t.Fatalf("panning left at the edge must clamp, got %d", model.db.colOff)
	}
	step, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	if step.(Model).db.colOff != 1 {
		t.Fatalf("l must advance the window, got %d", step.(Model).db.colOff)
	}
}

func TestSearchResultsIncludeUploads(t *testing.T) {
	sv := newSearchView()
	sv.setResults(&api.SearchResults{
		Uploads: []api.Upload{{ID: 1, Slug: "q4-report", Title: "Q4 Report", FileSize: 2048}},
	})
	if row, ok := sv.selected(); !ok || row.upload == nil || row.upload.Slug != "q4-report" {
		t.Fatalf("uploads must be selectable rows, got %+v", sv.rows)
	}
}
