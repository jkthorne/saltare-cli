// Package ui is the Bubble Tea TUI: sidebar of channels, live message feed,
// status bar. Read-only in Phase 1 — it's a workspace monitor, not a composer.
package ui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jkthorne/saltare/cli/internal/api"
	"github.com/jkthorne/saltare/cli/internal/cable"
	"github.com/jkthorne/saltare/cli/internal/config"
	"github.com/jkthorne/saltare/cli/internal/store"
)

type connState int

const (
	connConnecting connState = iota
	connLive
	connRetrying
)

// Bubble Tea messages.
type channelsLoadedMsg struct{ channels []api.Channel }
type historyLoadedMsg struct {
	channelID int64
	messages  []api.Message
}
type cableEventMsg struct{ ev cable.Event }
type markedReadMsg struct{ channelID int64 }
type fatalErrMsg struct{ err error }
type softErrMsg struct{ err error }

type Model struct {
	cfg    *config.Config
	client *api.Client
	store  *store.Store

	ctx       context.Context
	events    chan cable.Event
	cableOnce bool // cable goroutine started

	ordered    []api.Channel // sidebar display order
	selected   int           // index into ordered
	focusedID  int64         // channel whose feed is shown
	focusFeed  bool          // false = sidebar has key focus
	conn       connState
	width      int
	height     int
	ready      bool // first WindowSizeMsg seen
	loadingMsg string
	softErr    string
	fatal      error

	vp       viewport.Model
	renderer *feedRenderer
}

func NewModel(ctx context.Context, cfg *config.Config, client *api.Client) Model {
	return Model{
		cfg:        cfg,
		client:     client,
		store:      store.New(),
		ctx:        ctx,
		events:     make(chan cable.Event, 64),
		loadingMsg: "connecting to " + cfg.ServerURL + " …",
		renderer:   newFeedRenderer(80),
	}
}

func (m Model) Init() tea.Cmd {
	return m.fetchChannels()
}

// ── Commands ────────────────────────────────────────────────────────────

func (m Model) fetchChannels() tea.Cmd {
	client := m.client
	ctx := m.ctx
	return func() tea.Msg {
		channels, err := client.Channels(ctx, api.ChannelsOpts{})
		if err != nil {
			return fatalErrMsg{err}
		}
		return channelsLoadedMsg{channels}
	}
}

func (m Model) fetchHistory(c api.Channel) tea.Cmd {
	client := m.client
	ctx := m.ctx
	return func() tea.Msg {
		msgs, err := client.Messages(ctx, c.Slug, 1, 50)
		if err != nil {
			return softErrMsg{err}
		}
		return historyLoadedMsg{channelID: c.ID, messages: msgs}
	}
}

func (m Model) markRead(c api.Channel) tea.Cmd {
	client := m.client
	ctx := m.ctx
	return func() tea.Msg {
		if _, err := client.MarkRead(ctx, c.Slug, 0); err != nil {
			return softErrMsg{err}
		}
		return markedReadMsg{channelID: c.ID}
	}
}

func (m Model) startCable(channelIDs []int64) tea.Cmd {
	server := m.cfg.ServerURL
	token := m.client.AccessToken()
	ctx := m.ctx
	events := m.events
	return func() tea.Msg {
		go cable.Run(ctx, server, token, channelIDs, events)
		return nil
	}
}

func (m Model) waitEvent() tea.Cmd {
	events := m.events
	return func() tea.Msg {
		ev, ok := <-events
		if !ok {
			return nil
		}
		return cableEventMsg{ev}
	}
}

// ── Update ──────────────────────────────────────────────────────────────

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layoutViewport()
		m.ready = true
		m.refreshFeed(false)
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case channelsLoadedMsg:
		m.store.SetChannels(msg.channels)
		m.ordered = flattenChannels(msg.channels)
		if len(m.ordered) == 0 {
			m.fatal = fmt.Errorf("no channels visible; join one on the web first")
			return m, tea.Quit
		}
		m.loadingMsg = ""
		m.selected = firstUnreadIndex(m.store, m.ordered)
		open := m.ordered[m.selected]
		m.focusedID = open.ID

		var ids []int64
		for _, c := range msg.channels {
			if c.Member {
				ids = append(ids, c.ID)
			}
		}
		cmds := []tea.Cmd{m.fetchHistory(open), m.waitEvent()}
		if !m.cableOnce {
			m.cableOnce = true
			cmds = append(cmds, m.startCable(ids))
		}
		if open.Member {
			cmds = append(cmds, m.markRead(open))
		}
		return m, tea.Batch(cmds...)

	case historyLoadedMsg:
		m.store.MergeHistory(msg.channelID, msg.messages)
		if msg.channelID == m.focusedID {
			m.refreshFeed(true)
		}
		return m, nil

	case cableEventMsg:
		return m.handleCable(msg.ev)

	case markedReadMsg:
		m.store.ClearUnread(msg.channelID)
		return m, nil

	case softErrMsg:
		m.softErr = msg.err.Error()
		return m, nil

	case fatalErrMsg:
		m.fatal = msg.err
		return m, tea.Quit
	}

	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "tab":
		m.focusFeed = !m.focusFeed
		return m, nil
	case "r":
		return m, m.fetchChannels()
	}

	if !m.focusFeed {
		switch msg.String() {
		case "j", "down":
			return m.moveSelection(1)
		case "k", "up":
			return m.moveSelection(-1)
		case "enter", "l", "right":
			return m.openSelected()
		}
		return m, nil
	}

	// Feed focus: the viewport owns scrolling keys (j/k/u/d/g/G/pgup…).
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return m, cmd
}

func (m Model) moveSelection(delta int) (tea.Model, tea.Cmd) {
	next := m.selected + delta
	if next < 0 || next >= len(m.ordered) {
		return m, nil
	}
	m.selected = next
	return m.openSelected()
}

func (m Model) openSelected() (tea.Model, tea.Cmd) {
	open := m.ordered[m.selected]
	if open.ID == m.focusedID {
		return m, nil
	}
	m.focusedID = open.ID
	m.refreshFeed(true)

	cmds := []tea.Cmd{m.fetchHistory(open)}
	if open.Member {
		cmds = append(cmds, m.markRead(open))
	}
	return m, tea.Batch(cmds...)
}

func (m Model) handleCable(ev cable.Event) (tea.Model, tea.Cmd) {
	cmds := []tea.Cmd{m.waitEvent()}

	switch ev.Type {
	case cable.EventConnected:
		reconnected := m.conn == connRetrying
		m.conn = connLive
		if reconnected {
			// Broadcasts during the outage are lost — refetch the world.
			if c, ok := m.store.Channel(m.focusedID); ok {
				cmds = append(cmds, m.fetchChannels(), m.fetchHistory(c))
			} else {
				cmds = append(cmds, m.fetchChannels())
			}
		}
	case cable.EventDisconnected:
		m.conn = connRetrying
	default:
		channelID, changed := m.store.Apply(ev)
		if changed && channelID == m.focusedID {
			m.store.ClearUnread(channelID)
			m.refreshFeed(false)
			if c, ok := m.store.Channel(channelID); ok && c.Member && ev.Type == cable.EventMessageCreated {
				cmds = append(cmds, m.markRead(c))
			}
		}
	}
	return m, tea.Batch(cmds...)
}

// ── Layout & view ───────────────────────────────────────────────────────

func (m *Model) layoutViewport() {
	feedWidth := m.width - sidebarWidth - 1
	if feedWidth < 20 {
		feedWidth = 20
	}
	feedHeight := m.height - 3 // title + status bar
	if feedHeight < 3 {
		feedHeight = 3
	}
	if m.vp.Width == 0 {
		m.vp = viewport.New(feedWidth, feedHeight)
	} else {
		m.vp.Width = feedWidth
		m.vp.Height = feedHeight
	}
	m.renderer.Resize(feedWidth)
}

// refreshFeed re-renders the focused channel into the viewport. jump forces
// scroll-to-bottom; otherwise it sticks only if already tailing.
func (m *Model) refreshFeed(jump bool) {
	wasAtBottom := m.vp.AtBottom()
	m.vp.SetContent(m.renderer.Render(m.store.Messages(m.focusedID)))
	if jump || wasAtBottom {
		m.vp.GotoBottom()
	}
}

func (m Model) View() string {
	if m.fatal != nil {
		return styleErrText.Render("sal: "+m.fatal.Error()) + "\n"
	}
	if !m.ready || m.loadingMsg != "" {
		return styleFeedTopic.Render(m.loadingMsg) + "\n"
	}

	sidebar := renderSidebar(m.store, m.cfg.WorkspaceName, m.ordered, m.selected, !m.focusFeed, m.height-1)

	title := ""
	if c, ok := m.store.Channel(m.focusedID); ok {
		title = styleFeedTitle.Render(kindGlyph(c.Kind) + " " + c.Name)
		if c.Description != nil && *c.Description != "" {
			title += "  " + styleFeedTopic.Render(truncate(*c.Description, m.vp.Width-lipgloss.Width(title)-2))
		}
	}
	feed := title + "\n" + m.vp.View()

	main := lipgloss.JoinHorizontal(lipgloss.Top, sidebar, feed)
	return main + "\n" + m.statusBar()
}

func (m Model) statusBar() string {
	var conn string
	switch m.conn {
	case connLive:
		conn = styleStatusLive.Render(" ● LIVE ")
	case connRetrying:
		conn = styleStatusRetry.Render(" ◌ RECONNECTING ")
	default:
		conn = styleStatusBar.Render(" ○ CONNECTING ")
	}

	left := conn + styleStatusBar.Render(" "+m.cfg.WorkspaceName+" · saltare cum machina")
	if m.softErr != "" {
		left += styleStatusDead.Render(" ! " + truncate(m.softErr, 40))
	}
	keys := styleStatusKeys.Render("tab focus · j/k move · enter open · r refresh · q quit ")

	gap := m.width - lipgloss.Width(left) - lipgloss.Width(keys)
	if gap < 1 {
		return left
	}
	return left + styleStatusBar.Render(strings.Repeat(" ", gap)) + keys
}

func firstUnreadIndex(s *store.Store, ordered []api.Channel) int {
	for i, c := range ordered {
		if s.Unread(c.ID) > 0 {
			return i
		}
	}
	return 0
}
