package ui

import "strings"

// noTarget marks a line that belongs to no selectable item — a title, a group
// label, a blank spacer, a key hint.
const noTarget = -1

// rowBuilder assembles a pane's lines while recording which item each line
// belongs to. Panes render the lines; mouse hit-testing reads the targets.
//
// The two are built together because the offset between a pane's item indexes
// and its screen lines is invisible from the outside: group labels and blank
// spacers push rows down, a variable preamble (an open input, a loading line)
// shifts everything, and a scrolled window slides the whole block. Every pane
// grew its own version of that arithmetic for the keyboard cursor; recomputing
// it by hand in a click handler is how the two silently disagree.
type rowBuilder struct {
	lines   []string
	targets []int // parallel to lines
}

// chrome appends lines that aren't selectable.
func (b *rowBuilder) chrome(lines ...string) {
	for _, line := range lines {
		b.lines = append(b.lines, line)
		b.targets = append(b.targets, noTarget)
	}
}

// row appends one line belonging to item.
func (b *rowBuilder) row(line string, item int) {
	b.lines = append(b.lines, line)
	b.targets = append(b.targets, item)
}

// appendAll concatenates another builder, keeping its targets attached.
func (b *rowBuilder) appendAll(other *rowBuilder) {
	b.lines = append(b.lines, other.lines...)
	b.targets = append(b.targets, other.targets...)
}

func (b *rowBuilder) len() int { return len(b.lines) }

// target reports the item drawn on a line, or noTarget for chrome and for any
// line outside the pane.
func (b *rowBuilder) target(line int) int {
	if line < 0 || line >= len(b.targets) {
		return noTarget
	}
	return b.targets[line]
}

// lineOf is the inverse: the first line an item occupies, or -1.
func (b *rowBuilder) lineOf(item int) int {
	for i, t := range b.targets {
		if t == item {
			return i
		}
	}
	return -1
}

// replace overwrites a line and drops its target, so a row repurposed as chrome
// (a "↑ 3 more" marker taking over an edge line) stops being clickable.
func (b *rowBuilder) replace(line int, text string) {
	if line < 0 || line >= len(b.lines) {
		return
	}
	b.lines[line] = text
	b.targets[line] = noTarget
}

// slice takes n lines from off, keeping lines and targets in lockstep. Out-of
// -range requests clamp rather than panic — callers derive off from a cursor
// position, and a stale cursor shouldn't crash a render.
func (b *rowBuilder) slice(off, n int) *rowBuilder {
	if off < 0 {
		off = 0
	}
	if off > len(b.lines) {
		off = len(b.lines)
	}
	end := off + n
	if end > len(b.lines) {
		end = len(b.lines)
	}
	if n <= 0 || off >= end {
		return &rowBuilder{}
	}
	out := &rowBuilder{
		lines:   make([]string, end-off),
		targets: make([]int, end-off),
	}
	copy(out.lines, b.lines[off:end])
	copy(out.targets, b.targets[off:end])
	return out
}

func (b *rowBuilder) join() string { return strings.Join(b.lines, "\n") }

// pickerLine is the list-row idiom every pane shares: a cursor marker, the
// label truncated to the pane, and the selected/unselected style. The width
// budget (6) covers the pane's own padding plus the marker.
func pickerLine(label string, selected bool, width int) string {
	if selected {
		return stylePickerSel.Render("▸ " + truncate(label, width-6))
	}
	return stylePickerRow.Render("  " + truncate(label, width-6))
}
