package ui

import "testing"

func TestRowBuilderTracksTargetsAcrossChrome(t *testing.T) {
	b := &rowBuilder{}
	b.chrome("title", "")
	b.row("first", 0)
	b.chrome("", "GROUP")
	b.row("second", 1)

	want := []int{noTarget, noTarget, 0, noTarget, noTarget, 1}
	for line, item := range want {
		if got := b.target(line); got != item {
			t.Fatalf("line %d: want target %d, got %d", line, item, got)
		}
	}
	if got := b.lineOf(1); got != 5 {
		t.Fatalf("item 1 draws on line 5, got %d", got)
	}
	if got := b.lineOf(9); got != -1 {
		t.Fatalf("an absent item has no line, got %d", got)
	}
}

// Out-of-range lookups are reachable from a stale cursor or a click below the
// last row, so they must answer "nothing" rather than panic.
func TestRowBuilderTargetOutOfRange(t *testing.T) {
	b := &rowBuilder{}
	b.row("only", 0)
	for _, line := range []int{-5, -1, 1, 100} {
		if got := b.target(line); got != noTarget {
			t.Fatalf("line %d must be untargeted, got %d", line, got)
		}
	}
}

func TestRowBuilderReplaceClearsTarget(t *testing.T) {
	b := &rowBuilder{}
	b.row("a", 0)
	b.replace(0, "↑ 3 more")
	if b.lines[0] != "↑ 3 more" {
		t.Fatalf("replace must swap the text, got %q", b.lines[0])
	}
	if got := b.target(0); got != noTarget {
		t.Fatalf("a repurposed line must stop being clickable, got %d", got)
	}
	b.replace(7, "ignored") // out of range: a no-op, not a panic
	if b.len() != 1 {
		t.Fatalf("replace must not grow the builder, got %d lines", b.len())
	}
}

func TestRowBuilderSliceKeepsLinesAndTargetsAligned(t *testing.T) {
	b := &rowBuilder{}
	for i := 0; i < 6; i++ {
		b.row(string(rune('a'+i)), i)
	}

	got := b.slice(2, 3)
	if got.len() != 3 || got.lines[0] != "c" {
		t.Fatalf("slice(2,3) must start at 'c', got %v", got.lines)
	}
	if got.target(0) != 2 || got.target(2) != 4 {
		t.Fatalf("targets must slide with the lines, got %d..%d", got.target(0), got.target(2))
	}
	if over := b.slice(4, 10); over.len() != 2 {
		t.Fatalf("an over-long slice must clamp, got %d lines", over.len())
	}
	if empty := b.slice(9, 3); empty.len() != 0 {
		t.Fatalf("an out-of-range slice must be empty, got %v", empty.lines)
	}
	if none := b.slice(0, 0); none.len() != 0 {
		t.Fatalf("zero height means zero lines, got %v", none.lines)
	}
}

func TestRowBuilderAppendAllCarriesTargets(t *testing.T) {
	head := &rowBuilder{}
	head.chrome("header")
	tail := &rowBuilder{}
	tail.row("row", 3)

	head.appendAll(tail)
	if head.len() != 2 {
		t.Fatalf("appendAll must concatenate, got %d lines", head.len())
	}
	if got := head.target(1); got != 3 {
		t.Fatalf("the appended target must survive, got %d", got)
	}
}
