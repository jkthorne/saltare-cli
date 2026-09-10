package ui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jkthorne/saltare-cli/internal/api"
)

func TestExtractEmbeds(t *testing.T) {
	body := "See [[doc: handbook ]] and ![[task:fix-login-abc]], again [[doc:handbook]], plus [[msg:42]]."
	refs := extractEmbeds(body)
	want := []embedRef{
		{kind: "doc", ref: "handbook"},
		{kind: "task", ref: "fix-login-abc"},
		{kind: "msg", ref: "42"},
	}
	if len(refs) != len(want) {
		t.Fatalf("refs: %+v", refs)
	}
	for i := range want {
		if refs[i] != want[i] {
			t.Fatalf("ref %d = %+v, want %+v", i, refs[i], want[i])
		}
	}

	if got := extractEmbeds("no references here"); len(got) != 0 {
		t.Fatalf("plain text must yield nothing, got %+v", got)
	}
}

func embedFeedModel(t *testing.T, body string) Model {
	t.Helper()
	m := testModel(t)
	m.store.SetChannels([]api.Channel{{ID: 1, Slug: "general", Name: "General", Kind: "public_channel", Member: true}})
	m.store.MergeHistory(1, []api.Message{
		{ID: 10, ChannelID: 1, Body: body, Sender: api.Sender{Type: "User", ID: 8, Name: "Other"}, CreatedAt: time.Now()},
	})
	m.focusedID = 1
	m.focus = focusFeed
	m.feedSel = 10
	return m
}

func TestEnterOnSingleEmbedDispatchesFollow(t *testing.T) {
	m := embedFeedModel(t, "read [[doc:handbook]] please")

	step, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model := step.(Model)
	if model.view != viewDocs {
		t.Fatal("a doc embed must open the docs view")
	}
	if cmd == nil {
		t.Fatal("following a doc must dispatch the fetch")
	}
}

func TestEnterOnMultipleEmbedsOpensPicker(t *testing.T) {
	m := embedFeedModel(t, "compare [[doc:handbook]] with [[task:fix-login-abc]]")

	step, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model := step.(Model)
	if !model.embedPicker.active || len(model.embedPicker.refs) != 2 {
		t.Fatalf("two embeds must open the picker, got %+v", model.embedPicker)
	}

	step, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	step, cmd := step.(Model).Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = step.(Model)
	if model.embedPicker.active {
		t.Fatal("enter must close the picker")
	}
	if cmd == nil {
		t.Fatal("the second ref (task) must dispatch the task fetch")
	}

	// The view switches when the fetch resolves.
	step, _ = model.Update(taskDetailLoadedMsg{api.Task{ID: 1, Slug: "fix-login-abc", Title: "Fix login", State: "open"}})
	model = step.(Model)
	if model.view != viewTasks || model.tasks.detail == nil {
		t.Fatal("the resolved task must open the detail pane")
	}
}

func TestEnterWithoutEmbedsToasts(t *testing.T) {
	m := embedFeedModel(t, "just words")

	step, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if step.(Model).embedPicker.active {
		t.Fatal("no embeds must not open the picker")
	}
	if cmd == nil {
		t.Fatal("expected the no-references toast")
	}
}

func TestUnsupportedEmbedTypeToasts(t *testing.T) {
	m := embedFeedModel(t, "see [[project:launch]]")

	step, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model := step.(Model)
	if model.view != viewChat {
		t.Fatal("unsupported types must stay in chat")
	}
	if cmd == nil {
		t.Fatal("expected the opens-on-the-web toast")
	}
}

func TestUploadEmbedOpensFilesView(t *testing.T) {
	m := embedFeedModel(t, "grab [[upload:q4-report]]")

	step, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model := step.(Model)
	if model.view != viewFiles {
		t.Fatal("an upload embed must open the files view")
	}
	if model.files.pendingSelect != "q4-report" || cmd == nil {
		t.Fatalf("the slug must be pending selection, got %q", model.files.pendingSelect)
	}
}

func TestDBEmbedOpensGrid(t *testing.T) {
	m := embedFeedModel(t, "check [[db:crm]]")

	step, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model := step.(Model)
	if model.view != viewDB || model.db.level != dbLevelGrid {
		t.Fatalf("a db embed must open the grid, got view=%v level=%d", model.view, model.db.level)
	}
	if cmd == nil {
		t.Fatal("the grid open must dispatch schema+rows fetches")
	}
}

func TestMessageResolvedInUnknownChannelSoftErrors(t *testing.T) {
	m := embedFeedModel(t, "x")

	step, _ := m.Update(messageResolvedMsg{api.Message{ID: 99, ChannelID: 777}})
	model := step.(Model)
	if model.softErr == "" {
		t.Fatal("an unknown channel must surface the refresh hint")
	}
}
