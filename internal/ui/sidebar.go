package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/jkthorne/saltare/cli/internal/api"
	"github.com/jkthorne/saltare/cli/internal/store"
)

const sidebarWidth = 28

// sidebarItem is one selectable row: an actual channel, or an agent with no
// DM channel yet (first message bootstraps one).
type sidebarItem struct {
	channel *api.Channel
	agent   *api.Agent // set only for channel-less agents
}

func (it sidebarItem) isAgentStub() bool { return it.channel == nil }

func (it sidebarItem) channelID() int64 {
	if it.channel != nil {
		return it.channel.ID
	}
	return 0
}

func (it sidebarItem) title() string {
	if it.channel != nil {
		return it.channel.Title()
	}
	return it.agent.Name
}

func (it sidebarItem) glyph() string {
	if it.channel != nil {
		return kindGlyph(it.channel.Kind)
	}
	return kindGlyph("agent_dm")
}

func (it sidebarItem) group() string {
	if it.isAgentStub() {
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
		return "OTHER"
	}
}

// buildSidebar merges the channel list with the agent roster: agent-DM
// channels represent their agent; active agents without one become stubs.
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
	return items
}

func renderSidebar(s *store.Store, workspaceName string, items []sidebarItem, selected int, focused bool, height int) string {
	var rows []string
	rows = append(rows, styleSidebarHeader.Render(truncate("◢ "+workspaceName, sidebarWidth-2)))
	rows = append(rows, "")

	lastGroup := ""
	for idx, it := range items {
		if g := it.group(); g != lastGroup {
			if lastGroup != "" {
				rows = append(rows, "")
			}
			rows = append(rows, styleGroupLabel.Render(g))
			lastGroup = g
		}
		rows = append(rows, sidebarRow(s, it, idx == selected, focused))
	}

	body := strings.Join(rows, "\n")
	col := lipgloss.NewStyle().Width(sidebarWidth).Height(height).MaxHeight(height).Render(body)
	return styleSidebarBorder.Render(col)
}

func sidebarRow(s *store.Store, it sidebarItem, selected, focused bool) string {
	unread := 0
	if it.channel != nil {
		unread = s.Unread(it.channel.ID)
	}

	marker := "  "
	if selected {
		marker = "▸ "
	}

	badge := ""
	if unread > 0 {
		badge = styleUnreadBadge.Render(fmt.Sprintf(" %d", min(unread, 99)))
	}

	nameWidth := sidebarWidth - 2 - lipgloss.Width(marker) - lipgloss.Width(badge) - 2
	name := truncate(it.glyph()+" "+it.title(), nameWidth)

	style := styleChannelRow
	switch {
	case selected && focused:
		style = styleChannelSel
	case unread > 0:
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
