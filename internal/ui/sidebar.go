package ui

import (
	"fmt"
	"sort"

	"github.com/charmbracelet/lipgloss"

	"github.com/jkthorne/saltare-cli/internal/api"
	"github.com/jkthorne/saltare-cli/internal/store"
)

const sidebarWidth = 28

// Nav-rail keys. The rail sits above the channel list and reaches the modes
// that were otherwise palette-only — the pane is navigation, not just a
// channel list.
const (
	navHome  = "nav_home"
	navInbox = "nav_inbox"
	navTasks = "nav_tasks"
	navDocs  = "nav_docs"
	navFiles = "nav_files"
	navDB    = "nav_db"
)

var navRail = []string{navHome, navInbox, navTasks, navDocs, navFiles, navDB}

// navCounts are the rail's live badges. Zero renders no badge.
type navCounts struct {
	inbox   int // unread notifications
	overdue int // overdue tasks in the my-work list
}

func (c navCounts) of(key string) int {
	switch key {
	case navInbox:
		return c.inbox
	case navTasks:
		return c.overdue
	}
	return 0
}

func (c navCounts) badgeStyle(key string) lipgloss.Style {
	if key == navTasks {
		return styleOverdueBadge
	}
	return styleUnreadBadge
}

func navLabel(key string) string {
	switch key {
	case navHome:
		return "⌂ home"
	case navInbox:
		return "◉ inbox"
	case navTasks:
		return "☑ my work"
	case navDocs:
		return "▤ documents"
	case navFiles:
		return "⇱ files"
	case navDB:
		return "▦ databases"
	}
	return key
}

// navItems is the rail, in display order.
func navItems() []sidebarItem {
	items := make([]sidebarItem, 0, len(navRail))
	for _, key := range navRail {
		items = append(items, sidebarItem{nav: key})
	}
	return items
}

// sidebarItem is one selectable row: a nav-rail target, an actual channel, or
// an agent with no DM channel yet (first message bootstraps one).
type sidebarItem struct {
	channel *api.Channel
	agent   *api.Agent // set only for channel-less agents
	nav     string     // set only for nav-rail rows
}

func (it sidebarItem) isNav() bool { return it.nav != "" }

func (it sidebarItem) isAgentStub() bool { return it.channel == nil && it.agent != nil }

func (it sidebarItem) channelID() int64 {
	if it.channel != nil {
		return it.channel.ID
	}
	return 0
}

func (it sidebarItem) title() string {
	switch {
	case it.channel != nil:
		return it.channel.Title()
	case it.agent != nil:
		return it.agent.Name
	}
	return navLabel(it.nav)
}

func (it sidebarItem) glyph() string {
	switch {
	case it.channel != nil:
		return kindGlyph(it.channel.Kind)
	case it.agent != nil:
		return kindGlyph("agent_dm")
	}
	return ""
}

// label is the row text: glyph + name for conversations, and the rail's own
// label (which carries its glyph) for nav rows.
func (it sidebarItem) label() string {
	if it.isNav() {
		return navLabel(it.nav)
	}
	return it.glyph() + " " + it.title()
}

func (it sidebarItem) group() string {
	switch {
	case it.isNav():
		return ""
	case it.isAgentStub():
		return "AGENTS"
	}
	switch it.channel.Kind {
	case "public_channel", "private_channel":
		return "CHANNELS"
	case "dm":
		return "DIRECT"
	case "agent_dm":
		return "AGENTS"
	default:
		return "RECENT"
	}
}

// buildSidebar merges the channel list with the agent roster: agent-DM
// channels represent their agent; active agents without one become stubs.
// Contextual channels the default listing never returns — threads and task
// discussions the user opened this session — trail in RECENT.
func buildSidebar(channels []api.Channel, agents []api.Agent) []sidebarItem {
	var items []sidebarItem
	agentHasChannel := map[int64]bool{}

	for _, group := range []string{"CHANNELS", "DIRECT", "AGENTS"} {
		for i := range channels {
			c := &channels[i]
			it := sidebarItem{channel: c}
			if it.group() != group {
				continue
			}
			if c.Kind == "agent_dm" && c.HostType != nil && *c.HostType == "Agent" && c.HostID != nil {
				agentHasChannel[*c.HostID] = true
			}
			items = append(items, it)
		}
		if group == "AGENTS" {
			for i := range agents {
				a := &agents[i]
				if a.Status == "active" && !agentHasChannel[a.ID] {
					items = append(items, sidebarItem{agent: a})
				}
			}
		}
	}
	return append(items, recentItems(channels)...)
}

// recentItems are the contextual channels in the store — threads and
// discussions, which only arrive by being opened — most recently active first,
// so a thread you were just in is one keypress away instead of unreachable.
func recentItems(channels []api.Channel) []sidebarItem {
	var recent []*api.Channel
	for i := range channels {
		c := &channels[i]
		if (sidebarItem{channel: c}).group() == "RECENT" {
			recent = append(recent, c)
		}
	}
	sort.SliceStable(recent, func(i, j int) bool {
		return recent[i].UpdatedAt.After(recent[j].UpdatedAt)
	})

	items := make([]sidebarItem, 0, len(recent))
	for _, c := range recent {
		items = append(items, sidebarItem{channel: c})
	}
	return items
}

// sidebarState is everything the pane draws besides the store: the rows, the
// cursor, which mode is live (so its rail row reads as active), and the badges.
type sidebarState struct {
	workspace string
	items     []sidebarItem
	selected  int
	focused   bool
	activeNav string
	counts    navCounts
	height    int
}

// sidebarHeaderLines is the pinned block above the scrolling row list: the
// workspace name and a blank line. A click's y maps into the window past it.
const sidebarHeaderLines = 2

func renderSidebar(s *store.Store, st sidebarState) string {
	rows := sidebarVisible(s, st)
	col := lipgloss.NewStyle().Width(sidebarWidth).Height(st.height).MaxHeight(st.height).
		Render(rows.join())
	return styleSidebarBorder.Render(col)
}

// sidebarVisible is the whole rendered column — pinned header plus the scrolled
// window — with each line's item recorded. renderSidebar draws it; the mouse hit
// test indexes it. Deriving both from one builder is what keeps a click on a
// scrolled list landing on the row the user is actually looking at.
func sidebarVisible(s *store.Store, st sidebarState) *rowBuilder {
	out := &rowBuilder{}
	out.chrome(styleSidebarHeader.Render(truncate("◢ "+st.workspace, sidebarWidth-2)), "")

	body := sidebarBody(s, st)
	out.appendAll(sidebarWindow(body, body.lineOf(st.selected), st.height-sidebarHeaderLines))
	return out
}

// sidebarBody renders every row below the pinned header, tagging each line with
// the item index it draws so the window and the hit test can follow it.
func sidebarBody(s *store.Store, st sidebarState) *rowBuilder {
	b := &rowBuilder{}
	lastGroup := ""
	for idx, it := range st.items {
		if g := it.group(); g != lastGroup {
			if b.len() > 0 {
				b.chrome("")
			}
			if g != "" {
				b.chrome(styleGroupLabel.Render(g))
			}
			lastGroup = g
		}
		b.row(sidebarRow(s, it, idx == st.selected, st.focused, st.activeNav, st.counts), idx)
	}
	return b
}

// sidebarWindow scrolls the row list so the cursor stays visible, replacing the
// edge lines with hidden-row counts. Without this the pane silently clipped
// everything past the terminal height — the cursor could sit off-screen.
func sidebarWindow(b *rowBuilder, selLine, avail int) *rowBuilder {
	if avail <= 0 {
		return &rowBuilder{}
	}
	if b.len() <= avail {
		return b
	}

	// Scroll only once the cursor nears the bottom edge, so short lists stay
	// anchored at the top and long ones follow the cursor a line at a time.
	margin := 3
	if avail < 6 {
		margin = 1
	}
	offset := 0
	if selLine > avail-margin {
		offset = selLine - avail + margin
	}
	if maxOffset := b.len() - avail; offset > maxOffset {
		offset = maxOffset
	}

	visible := b.slice(offset, avail)
	// The counts overwrite an edge row, so never one holding the cursor. replace
	// clears the target too — a "↑ 3 more" line must not open the channel whose
	// row it covers.
	if offset > 0 && offset != selLine {
		visible.replace(0, styleSidebarMore.Render(fmt.Sprintf("  ↑ %d more", offset)))
	}
	if below := b.len() - offset - avail; below > 0 && offset+avail-1 != selLine {
		visible.replace(avail-1, styleSidebarMore.Render(fmt.Sprintf("  ↓ %d more", below)))
	}
	return visible
}

func sidebarRow(s *store.Store, it sidebarItem, selected, focused bool, activeNav string, counts navCounts) string {
	marker := "  "
	if selected {
		marker = "▸ "
	}

	count := 0
	badge := ""
	switch {
	case it.channel != nil:
		count = s.Unread(it.channel.ID)
		if count > 0 {
			badge = styleUnreadBadge.Render(fmt.Sprintf(" %d", min(count, 99)))
		}
	case it.isNav():
		count = counts.of(it.nav)
		if count > 0 {
			badge = counts.badgeStyle(it.nav).Render(fmt.Sprintf(" %d", min(count, 99)))
		}
	}

	nameWidth := sidebarWidth - 2 - lipgloss.Width(marker) - lipgloss.Width(badge) - 2
	name := truncate(it.label(), nameWidth)

	style := styleChannelRow
	switch {
	case selected && focused:
		style = styleChannelSel
	case it.isNav() && it.nav == activeNav:
		style = styleNavActive
	case it.channel != nil && count > 0:
		style = styleChannelUnread
	case selected:
		style = styleChannelSel.Bold(false)
	case it.isAgentStub():
		style = styleAgentStub
	}
	return marker + style.Render(name) + badge
}

func truncate(s string, width int) string {
	if width <= 1 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	runes := []rune(s)
	if len(runes) <= width-1 {
		return s
	}
	return string(runes[:width-1]) + "…"
}
