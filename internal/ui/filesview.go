package ui

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"

	"github.com/jkthorne/saltare-cli/internal/api"
	"github.com/jkthorne/saltare-cli/internal/tablefmt"
)

// filesView is the workspace file cabinet (palette → files): browse
// uploads, download to the cwd, upload by typed path, delete.
type filesView struct {
	list    []api.Upload
	sel     int
	loading bool

	inputOpen bool // upload path prompt
	input     textinput.Model

	confirmRm     *api.Upload
	pendingSelect string // slug to select once the list loads (follow-embed)
}

func newFilesView() filesView {
	ti := textinput.New()
	ti.Placeholder = "path to upload… (~ works)"
	ti.Prompt = "⇱ "
	return filesView{input: ti}
}

func (f *filesView) move(delta int) {
	if len(f.list) == 0 {
		return
	}
	next := f.sel + delta
	if next >= 0 && next < len(f.list) {
		f.sel = next
	}
}

func (f *filesView) selected() (api.Upload, bool) {
	if len(f.list) == 0 || f.sel >= len(f.list) {
		return api.Upload{}, false
	}
	return f.list[f.sel], true
}

// setList installs a fresh listing, honoring a pending follow-embed target.
func (f *filesView) setList(uploads []api.Upload) (found bool) {
	f.loading = false
	f.list = uploads
	if f.sel >= len(uploads) {
		f.sel = 0
	}
	if f.pendingSelect == "" {
		return true
	}
	target := f.pendingSelect
	f.pendingSelect = ""
	for i, u := range uploads {
		if u.Slug == target {
			f.sel = i
			return true
		}
	}
	return false
}

func (f *filesView) render(width, height int) string {
	body := f.listLines(width).join()
	return styleTasksPane.Width(width).Height(height).MaxHeight(height).Render(body)
}

// listLines lays the cabinet out, tagging each line with its index into f.list.
func (f *filesView) listLines(width int) *rowBuilder {
	b := &rowBuilder{}
	b.chrome(stylePickerTitle.Render("⇱ files"), "")
	if f.inputOpen {
		b.chrome(f.input.View(), "")
	}
	switch {
	case f.loading:
		b.chrome(styleFeedTopic.Render("loading…"))
	case len(f.list) == 0:
		b.chrome(styleFeedTopic.Render("no uploads yet — u uploads a file"))
	}
	for i, u := range f.list {
		category := "pending"
		if u.Category != nil {
			category = *u.Category
		}
		label := u.Slug + "  " + styleFeedTopic.Render(category+" · "+tablefmt.HumanSize(u.FileSize)) + "  " + u.Title
		b.row(pickerLine(label, i == f.sel, width), i)
	}
	b.chrome("", styleFeedTopic.Render("d download · u upload · x delete · y copy embed · r refresh · esc back"))
	return b
}

// expandHome resolves a leading ~/ in a typed upload path.
func expandHome(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(path, "~"), "/"))
		}
	}
	return path
}
