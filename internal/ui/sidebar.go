package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/jkthorne/saltare/cli/internal/api"
	"github.com/jkthorne/saltare/cli/internal/store"
)

const sidebarWidth = 28

// sidebarGroups is the render order; kinds not listed are hidden.
var sidebarGroups = []struct {
	label string
	kinds []string
}{
	{"CHANNELS", []string{"public_channel", "private_channel"}},
	{"DIRECT", []string{"dm"}},
	{"AGENTS", []string{"agent_dm"}},
}

// flattenChannels returns channels in sidebar display order (group order,
// server order within a group). The selection index addresses this slice.
func flattenChannels(channels []api.Channel) []api.Channel {
	var out []api.Channel
	for _, g := range sidebarGroups {
		for _, c := range channels {
			for _, k := range g.kinds {
				if c.Kind == k {
					out = append(out, c)
				}
			}
		}
	}
	return out
}

func renderSidebar(s *store.Store, workspaceName string, ordered []api.Channel, selected int, focused bool, height int) string {
	var rows []string
	rows = append(rows, styleSidebarHeader.Render(truncate("◢ "+workspaceName, sidebarWidth-2)))
	rows = append(rows, "")

	// ordered is already in group order (see flattenChannels); emit a label
	// whenever the group changes.
	lastGroup := ""
	for idx, c := range ordered {
		group := groupLabel(c.Kind)
		if group != lastGroup {
			if lastGroup != "" {
				rows = append(rows, "")
			}
			rows = append(rows, styleGroupLabel.Render(group))
			lastGroup = group
		}
		rows = append(rows, sidebarRow(s, c, idx == selected, focused))
	}

	body := strings.Join(rows, "\n")
	col := lipgloss.NewStyle().Width(sidebarWidth).Height(height).MaxHeight(height).Render(body)
	return styleSidebarBorder.Render(col)
}

func sidebarRow(s *store.Store, c api.Channel, selected, focused bool) string {
	unread := s.Unread(c.ID)

	marker := "  "
	if selected {
		marker = "▸ "
	}

	badge := ""
	if unread > 0 {
		badge = styleUnreadBadge.Render(fmt.Sprintf(" %d", min(unread, 99)))
	}

	nameWidth := sidebarWidth - 2 - lipgloss.Width(marker) - lipgloss.Width(badge) - 2
	name := truncate(kindGlyph(c.Kind)+" "+c.Name, nameWidth)

	style := styleChannelRow
	switch {
	case selected && focused:
		style = styleChannelSel
	case unread > 0:
		style = styleChannelUnread
	case selected:
		style = styleChannelSel.Bold(false)
	}
	return marker + style.Render(name) + badge
}

func groupLabel(kind string) string {
	for _, g := range sidebarGroups {
		for _, k := range g.kinds {
			if k == kind {
				return g.label
			}
		}
	}
	return "OTHER"
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
