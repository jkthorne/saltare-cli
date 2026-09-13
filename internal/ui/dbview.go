package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/jkthorne/saltare-cli/internal/api"
	"github.com/jkthorne/saltare-cli/internal/tablefmt"
)

// dbView is the database browser (palette → databases): a table list, a
// read-mostly grid on bubbles/table with h/l column panning, and a row
// detail whose fields edit in place (server-side coercion — raw strings go
// up, typed values come back).
type dbView struct {
	level int // 0 list · 1 grid · 2 row detail

	list    []api.Database
	sel     int
	loading bool

	database    *api.Database // schema-bearing, grid open
	pendingSlug string        // the table the grid is opening/showing — stale fetches are dropped
	rows        []api.DBRow
	pages       int  // pages fetched so far
	more        bool // a full last page suggests more rows remain
	grid        table.Model
	built       bool // grid constructed at least once (styles applied)
	colOff      int  // first visible data column (id is always shown)

	detailIdx int // index into rows
	fieldSel  int

	editOpen bool
	editKey  string
	input    textinput.Model

	width, height int
}

const (
	dbLevelList = iota
	dbLevelGrid
	dbLevelDetail
)

const dbPageSize = 100

func newDBView() dbView {
	ti := textinput.New()
	ti.Prompt = "= "
	return dbView{input: ti, width: 80, height: 24}
}

func (d *dbView) move(delta int) {
	if len(d.list) == 0 {
		return
	}
	next := d.sel + delta
	if next >= 0 && next < len(d.list) {
		d.sel = next
	}
}

func (d *dbView) selected() (api.Database, bool) {
	if len(d.list) == 0 || d.sel >= len(d.list) {
		return api.Database{}, false
	}
	return d.list[d.sel], true
}

func (d *dbView) columns() []api.DBColumn {
	if d.database == nil || d.database.Schema == nil {
		return nil
	}
	return d.database.Schema.Columns
}

func (d *dbView) currentRow() *api.DBRow {
	if d.detailIdx < 0 || d.detailIdx >= len(d.rows) {
		return nil
	}
	return &d.rows[d.detailIdx]
}

// buildGrid (re)derives the bubbles table from the rows and the column
// window. The id column is always visible; h/l slides which data columns
// join it. Cursor position survives rebuilds.
func (d *dbView) buildGrid() {
	records := tablefmt.Records(d.columns(), d.rows)
	// Grid chrome is read by a person, so head it with the columns' human
	// names; the records below keep their key-addressed cells.
	header := tablefmt.Headings(d.columns(), d.rows)

	// lipgloss.Width, not len: a heading is a human name now, and a byte
	// count mismeasures anything outside ASCII.
	widths := make([]int, len(header))
	for c := range header {
		w := lipgloss.Width(header[c])
		for _, record := range records[1:] {
			if cw := lipgloss.Width(tablefmt.Flatten(record[c])); cw > w {
				w = cw
			}
		}
		widths[c] = clampInt(w, 6, 32)
	}

	// Visible window: id (column 0) + data columns from colOff, as many as
	// fit the pane (padding: ~3 cols per column of table chrome).
	maxOff := len(header) - 2 // last data column index - 1 keeps ≥1 visible
	if maxOff < 0 {
		maxOff = 0
	}
	d.colOff = clampInt(d.colOff, 0, maxOff)
	visible := []int{0}
	budget := d.width - 6 - widths[0]
	for c := 1 + d.colOff; c < len(header) && budget > widths[c]+3; c++ {
		visible = append(visible, c)
		budget -= widths[c] + 3
	}

	cols := make([]table.Column, len(visible))
	for i, c := range visible {
		cols[i] = table.Column{Title: header[c], Width: widths[c]}
	}
	rows := make([]table.Row, len(records)-1)
	for r, record := range records[1:] {
		cells := make(table.Row, len(visible))
		for i, c := range visible {
			cells[i] = tablefmt.Flatten(record[c])
		}
		rows[r] = cells
	}

	cursor := 0
	if d.built {
		cursor = d.grid.Cursor()
	}
	d.grid = table.New(
		table.WithColumns(cols),
		table.WithRows(rows),
		table.WithFocused(true),
		table.WithHeight(clampInt(d.height-6, 3, d.height)),
	)
	styles := table.DefaultStyles()
	styles.Header = stylePickerTitle
	styles.Selected = stylePickerSel
	styles.Cell = stylePickerRow
	d.grid.SetStyles(styles)
	if cursor < len(rows) {
		d.grid.SetCursor(cursor)
	}
	d.built = true
}

func (d *dbView) resize(width, height int) {
	d.width, d.height = width, height
	if d.level >= dbLevelGrid && d.database != nil {
		d.buildGrid()
	}
}

func (d *dbView) render(width, height int) string {
	switch d.level {
	case dbLevelGrid, dbLevelDetail:
		if d.level == dbLevelDetail {
			return d.renderDetail(width, height)
		}
		return d.renderGrid(width, height)
	default:
		return d.renderList(width, height)
	}
}

func (d *dbView) renderList(width, height int) string {
	body := d.listLines(width).join()
	return styleTasksPane.Width(width).Height(height).MaxHeight(height).Render(body)
}

// listLines lays the table list out, tagging each line with its index into
// d.list.
func (d *dbView) listLines(width int) *rowBuilder {
	b := &rowBuilder{}
	b.chrome(stylePickerTitle.Render("▦ databases"), "")
	switch {
	case d.loading:
		b.chrome(styleFeedTopic.Render("loading…"))
	case len(d.list) == 0:
		b.chrome(styleFeedTopic.Render("no databases yet — create one on the web"))
	}
	for i, db := range d.list {
		label := fmt.Sprintf("%s  %s  %s", db.Slug, styleFeedTopic.Render(fmt.Sprintf("%d rows", db.RowsCount)), db.Name)
		b.row(pickerLine(label, i == d.sel, width), i)
	}
	b.chrome("", styleFeedTopic.Render("enter open · r refresh · esc back"))
	return b
}

// dbGridTopLine is the line grid.View() starts on inside the pane: renderGrid
// draws the table's title and a blank line above it. TestDatabaseGridRowLines
// checks this against the real frame.
const dbGridTopLine = 2

// gridRowAt maps a screen line inside the grid to the row drawn there.
// bubbles/table keeps its scroll offset unexported, so the line can't be reached
// by arithmetic — but the id column is always the first one the grid shows and
// row ids are unique, so a rendered line identifies its own row.
func (d *dbView) gridRowAt(line int) int {
	lines := strings.Split(d.grid.View(), "\n")
	if line < 0 || line >= len(lines) {
		return -1
	}
	fields := strings.Fields(ansi.Strip(lines[line]))
	if len(fields) == 0 {
		return -1
	}
	for i, row := range d.grid.Rows() {
		if len(row) > 0 && row[0] == fields[0] {
			return i
		}
	}
	return -1
}

func (d *dbView) renderGrid(width, height int) string {
	// The schema fetch resolves after the first frame — render the loading
	// state until it lands (d.database nil until dbOpenedMsg).
	if d.database == nil {
		return styleFeedTitle.Render("▦ "+d.pendingSlug) + "\n\n" +
			styleFeedTopic.Render("loading…") + "\n" +
			styleFeedTopic.Render("esc back")
	}

	title := styleFeedTitle.Render("▦ " + d.database.Name)
	count := fmt.Sprintf("showing %d of %d", len(d.rows), d.database.RowsCount)
	if d.more {
		count += " · o loads more"
	}
	pan := ""
	if len(d.columns()) > 1 {
		pan = " · h/l pan columns"
	}
	body := d.grid.View()
	if d.loading {
		body = styleFeedTopic.Render("loading…")
	}
	hint := styleFeedTopic.Render("enter row detail · " + count + pan + " · y copy embed · esc back")
	return title + "\n\n" + body + "\n" + hint
}

func (d *dbView) renderDetail(width, height int) string {
	row := d.currentRow()
	if row == nil || d.database == nil {
		return styleFeedTopic.Render("row gone — esc")
	}
	body := d.detailLines(row, width).join()
	return styleTasksPane.Width(width).Height(height).MaxHeight(height).Render(body)
}

// detailLines lays a row's fields out, tagging each with its column index —
// the space fieldSel moves over.
func (d *dbView) detailLines(row *api.DBRow, width int) *rowBuilder {
	b := &rowBuilder{}
	b.chrome(stylePickerTitle.Render(fmt.Sprintf("▦ %s · row %d", d.database.Name, row.ID)), "")
	for i, col := range d.columns() {
		value := tablefmt.RenderCell(row.Data[col.Key])
		line := col.Label() + ": " + value
		if col.Type == "formula" {
			line += styleFeedTopic.Render("  (computed)")
		}
		if d.editOpen && d.editKey == col.Key {
			line = col.Label() + ": " + d.input.View()
		}
		b.row(pickerLine(line, i == d.fieldSel, width), i)
	}
	if row.Body != nil && *row.Body != "" {
		b.chrome("", styleFeedTopic.Render("body: "+truncate(*row.Body, width-12)))
	}
	b.chrome("", styleFeedTopic.Render("enter edit field · esc back"))
	return b
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
