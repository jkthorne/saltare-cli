package ui

// Screen geometry. View assembles the frame from these, and mouse hit-testing
// reads them back, so the two can't disagree about where anything is drawn.
//
// The frame is: a sidebar column (plus its 1-column right border) beside a pane,
// with a single status-bar line underneath.
//
//	0                    sidebarWidth+1                     m.width
//	├── sidebar ─────────┤├── pane ──────────────────────────┤
//	│                     │ feed title                       │  y = 0
//	│                     │ ┌ viewport ──────────────────────┐│
//	│                     │ └────────────────────────────────┘│
//	│                     │ [mention popup]                   │
//	│                     │ ────────────── composer bar       │
//	│                     │ ┃ composer                        │
//	├── status bar ───────────────────────────────────────────┤  y = m.height-1
const (
	// feedTopLine is the feed title, drawn above the chat viewport.
	feedTopLine = 1
	// panePadTop is the blank line styleTasksPane's Padding(1, 2) puts above a
	// list pane's first line. Panes using it: tasks, home, files, notifications,
	// docs browser, database list and row detail.
	panePadTop = 1
)

type rect struct {
	x, y, w, h int
}

// contains is exclusive of the far edges. An empty rect contains nothing, which
// is what keeps clicks inert before the first WindowSizeMsg sizes the frame.
func (r rect) contains(x, y int) bool {
	if r.w <= 0 || r.h <= 0 {
		return false
	}
	return x >= r.x && x < r.x+r.w && y >= r.y && y < r.y+r.h
}

// paneRects is the frame View draws into.
type paneRects struct {
	sidebar  rect // channel list and nav rail, including the border column
	pane     rect // everything right of the sidebar, above the status bar
	feed     rect // the chat viewport inside pane
	composer rect // the textarea inside pane, below the popup and the bar
	status   rect // the bottom line
}
