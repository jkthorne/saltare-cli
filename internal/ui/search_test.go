package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jkthorne/saltare/cli/internal/api"
)

func TestCtrlFOpensSearchAndEscCloses(t *testing.T) {
	m := testModel(t)
	step, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlF})
	if !step.(Model).search.active {
		t.Fatal("ctrl+f must open search")
	}

	step, _ = step.(Model).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("bi")})
	model := step.(Model)
	if got := model.search.input.Value(); got != "bi" {
		t.Fatalf("typing must land in the search input, got %q", got)
	}
	if model.search.gen == 0 {
		t.Fatal("typing must arm the debounce generation")
	}

	step, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if step.(Model).search.active {
		t.Fatal("esc must close search")
	}
}

func TestStaleSearchResultsAreDropped(t *testing.T) {
	m := testModel(t)
	step, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlF})
	model := step.(Model)
	model.search.gen = 5

	step, _ = model.Update(searchResultsMsg{gen: 3, results: &api.SearchResults{
		Messages: []api.SearchMessage{{Message: api.Message{ID: 1, Body: "old"}}},
	}})
	if rows := step.(Model).search.rows; len(rows) != 0 {
		t.Fatalf("stale generation must be ignored, got %d rows", len(rows))
	}

	step, _ = step.(Model).Update(searchResultsMsg{gen: 5, results: &api.SearchResults{
		Messages: []api.SearchMessage{{Message: api.Message{ID: 2, Body: "fresh"}}},
	}})
	rows := step.(Model).search.rows
	if len(rows) != 2 || rows[0].header == "" || rows[1].message == nil {
		t.Fatalf("current generation must render header+row, got %+v", rows)
	}
}

func TestSearchRowNavigationSkipsHeaders(t *testing.T) {
	sv := newSearchView()
	sv.setResults(&api.SearchResults{
		Messages: []api.SearchMessage{{Message: api.Message{ID: 1}}},
		Tasks:    []api.Task{{Slug: "t-1", Title: "One"}},
	})
	if row, ok := sv.selected(); !ok || row.message == nil {
		t.Fatalf("selection must start on the first message, got %+v", row)
	}
	sv.move(1)
	if row, ok := sv.selected(); !ok || row.task == nil {
		t.Fatalf("move must skip the tasks header, got %+v", row)
	}
	sv.move(-1)
	if row, ok := sv.selected(); !ok || row.message == nil {
		t.Fatalf("moving back must skip the header again, got %+v", row)
	}
}
