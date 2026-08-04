package ui

import "github.com/charmbracelet/lipgloss"

// NieR HUD tokens, lifted from app/assets/tailwind/application.css. The TUI is
// dark-only for R1 — the brand's terminal aesthetic assumes a dark background.
var (
	cArc       = lipgloss.Color("#00c8f0")
	cArcDim    = lipgloss.Color("#0094b2")
	cArcBright = lipgloss.Color("#60dcf8")
	cMateria   = lipgloss.Color("#00e880")
	cLimit     = lipgloss.Color("#f0a000")
	cPhoenix   = lipgloss.Color("#f03050")
	cSilver    = lipgloss.Color("#8888a0")
	cMist      = lipgloss.Color("#9a9aad")
	cFrost     = lipgloss.Color("#cdcddd")
	cIce       = lipgloss.Color("#eae8e2")
	cGraphite  = lipgloss.Color("#1b1b24")
	cSteel     = lipgloss.Color("#262630")
	cChrome    = lipgloss.Color("#3b3b4d")
)

var (
	styleSidebarHeader = lipgloss.NewStyle().Foreground(cArc).Bold(true)
	styleGroupLabel    = lipgloss.NewStyle().Foreground(cSilver).Bold(true)
	styleChannelRow    = lipgloss.NewStyle().Foreground(cMist)
	styleChannelSel    = lipgloss.NewStyle().Foreground(cArcBright).Bold(true)
	styleChannelUnread = lipgloss.NewStyle().Foreground(cFrost).Bold(true)
	styleUnreadBadge   = lipgloss.NewStyle().Foreground(cLimit).Bold(true)
	styleSidebarBorder = lipgloss.NewStyle().BorderStyle(lipgloss.NormalBorder()).
				BorderRight(true).BorderForeground(cSteel)

	styleSenderUser  = lipgloss.NewStyle().Foreground(cFrost).Bold(true)
	styleSenderAgent = lipgloss.NewStyle().Foreground(cArc).Bold(true)
	styleTimestamp   = lipgloss.NewStyle().Foreground(cSilver)
	styleEditedTag   = lipgloss.NewStyle().Foreground(cSilver).Italic(true)
	styleSystemEvent = lipgloss.NewStyle().Foreground(cSilver).Italic(true)
	styleDateRule    = lipgloss.NewStyle().Foreground(cChrome)
	styleDateLabel   = lipgloss.NewStyle().Foreground(cSilver)

	styleStatusBar   = lipgloss.NewStyle().Foreground(cMist).Background(cGraphite)
	styleStatusLive  = lipgloss.NewStyle().Foreground(cMateria).Background(cGraphite).Bold(true)
	styleStatusRetry = lipgloss.NewStyle().Foreground(cLimit).Background(cGraphite).Bold(true)
	styleStatusDead  = lipgloss.NewStyle().Foreground(cPhoenix).Background(cGraphite).Bold(true)
	styleStatusKeys  = lipgloss.NewStyle().Foreground(cSilver).Background(cGraphite)

	styleFeedTitle = lipgloss.NewStyle().Foreground(cIce).Bold(true)
	styleFeedTopic = lipgloss.NewStyle().Foreground(cSilver)
	styleErrText   = lipgloss.NewStyle().Foreground(cPhoenix)

	styleSelGutter   = lipgloss.NewStyle().Foreground(cArc)
	styleEmbedChip   = lipgloss.NewStyle().Foreground(cArcBright).Background(cSteel)
	styleTasksPane   = lipgloss.NewStyle().Padding(1, 2)
	styleTaskOverdue = lipgloss.NewStyle().Foreground(cPhoenix).Bold(true)
	styleTaskDone    = lipgloss.NewStyle().Foreground(cChrome).Strikethrough(true)
	styleAgentStub   = lipgloss.NewStyle().Foreground(cArcDim)
	styleMentionRow  = lipgloss.NewStyle().Foreground(cMist).Background(cGraphite)
	styleMentionSel  = lipgloss.NewStyle().Foreground(cArcBright).Background(cSteel).Bold(true)
	styleComposerBar = lipgloss.NewStyle().Foreground(cChrome)
	stylePickerTitle = lipgloss.NewStyle().Foreground(cArc).Bold(true)
	stylePickerRow   = lipgloss.NewStyle().Foreground(cMist)
	stylePickerSel   = lipgloss.NewStyle().Foreground(cArcBright).Bold(true)
)

// kindGlyph marks channel kinds in the sidebar. Geometric glyphs only —
// the design system bans emoji.
func kindGlyph(kind string) string {
	switch kind {
	case "public_channel":
		return "#"
	case "private_channel":
		return "◆"
	case "dm":
		return "@"
	case "agent_dm":
		return "◇"
	case "thread":
		return "↳"
	default:
		return "·"
	}
}
