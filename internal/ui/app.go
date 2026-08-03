// Package ui is the Bubble Tea TUI. Phase 2: sidebar (channels + agents),
// live feed, composer with @-mention autocomplete, thread browsing, agent DMs.
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

type focusZone int

const (
	focusComposer focusZone = iota
	focusSidebar
	focusFeed
)

// Bubble Tea messages.
type channelsLoadedMsg struct{ channels []api.Channel }
type agentsLoadedMsg struct{ agents []api.Agent }
type mentionablesLoadedMsg struct{ mentionables []api.Mentionable }
type historyLoadedMsg struct {
	channelID int64
	messages  []api.Message
}
type threadsLoadedMsg struct {
	parentID int64
	threads  []api.Channel
}
type sentMsg struct {
	channelID int64
	message   api.Message
	inThread  bool
}
type agentMessagedMsg struct {
	channelID int64
	message   api.Message
}
type cableEventMsg struct{ ev cable.Event }
type markedReadMsg struct{ channelID int64 }
type fatalErrMsg struct{ err error }
type softErrMsg struct{ err error }
type sendFailedMsg struct{ err error }

type Model struct {
	cfg    *config.Config
	client *api.Client
	store  *store.Store

	ctx       context.Context
	cable     *cable.Client
	cableOnce bool

	items    []sidebarItem
	agents   []api.Agent
	selected int
	focus    focusZone

	focusedID    int64      // channel whose feed is shown (0 while agent stub)
	pendingAgent *api.Agent // composing the first DM to this agent
	threadReturn int64      // parent channel to return to on esc (0 = not in thread)

	threadPicker struct {
		active  bool
		threads []api.Channel
		sel     int
	}

	conn       connState
	width      int
	height     int
	ready      bool
	loadingMsg string
	softErr    string
	fatal      error
	sending    bool

	vp       viewport.Model
	comp     composer
	renderer *feedRenderer
}

func NewModel(ctx context.Context, cfg *config.Config, client *api.Client) Model {
	return Model{
		cfg:        cfg,
		client:     client,
		store:      store.New(),
		ctx:        ctx,
		loadingMsg: "connecting to " + cfg.ServerURL + " …",
		renderer:   newFeedRenderer(80),
		comp:       newComposer(),
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.fetchChannels(), m.fetchAgents(), m.fetchMentionables(), m.comp.focus())
}

// ── Commands ────────────────────────────────────────────────────────────

func (m Model) fetchChannels() tea.Cmd {
	client, ctx := m.client, m.ctx
	return func() tea.Msg {
		channels, err := client.Channels(ctx, api.ChannelsOpts{})
		if err != nil {
			return fatalErrMsg{err}
		}
		return channelsLoadedMsg{channels}
	}
}

func (m Model) fetchAgents() tea.Cmd {
	client, ctx := m.client, m.ctx
	return func() tea.Msg {
		agents, err := client.Agents(ctx)
		if err != nil {
			return softErrMsg{err} // agents:read may be missing; sidebar still works
		}
		return agentsLoadedMsg{agents}
	}
}

func (m Model) fetchMentionables() tea.Cmd {
	client, ctx := m.client, m.ctx
	return func() tea.Msg {
		mentionables, err := client.Mentionables(ctx)
		if err != nil {
			return softErrMsg{err}
		}
		return mentionablesLoadedMsg{mentionables}
	}
}

func (m Model) fetchHistory(c api.Channel) tea.Cmd {
	client, ctx := m.client, m.ctx
	return func() tea.Msg {
		msgs, err := client.Messages(ctx, c.Slug, 1, 50)
		if err != nil {
			return softErrMsg{err}
		}
		return historyLoadedMsg{channelID: c.ID, messages: msgs}
	}
}

func (m Model) fetchThreads(parent api.Channel) tea.Cmd {
	client, ctx := m.client, m.ctx
	return func() tea.Msg {
		threads, err := client.Channels(ctx, api.ChannelsOpts{ParentChannelSlug: parent.Slug})
		if err != nil {
			return softErrMsg{err}
		}
		return threadsLoadedMsg{parentID: parent.ID, threads: threads}
	}
}

func (m Model) markRead(c api.Channel) tea.Cmd {
	client, ctx := m.client, m.ctx
	return func() tea.Msg {
		if _, err := client.MarkRead(ctx, c.Slug, 0); err != nil {
			return softErrMsg{err}
		}
		return markedReadMsg{channelID: c.ID}
	}
}

func (m Model) sendToChannel(c api.Channel, body string) tea.Cmd {
	client, ctx := m.client, m.ctx
	inThread := c.Kind == "thread"
	return func() tea.Msg {
		msg, err := client.SendMessage(ctx, c.Slug, body)
		if err != nil {
			return sendFailedMsg{err}
		}
		return sentMsg{channelID: c.ID, message: *msg, inThread: inThread}
	}
}

func (m Model) sendToAgent(agent api.Agent, body string) tea.Cmd {
	client, ctx := m.client, m.ctx
	return func() tea.Msg {
		msg, channelID, err := client.MessageAgent(ctx, agent.Slug, body)
		if err != nil {
			return sendFailedMsg{err}
		}
		return agentMessagedMsg{channelID: channelID, message: *msg}
	}
}

func (m Model) startCable(channelIDs []int64) tea.Cmd {
	client := cable.NewClient(m.cfg.ServerURL, m.client.AccessToken(), channelIDs)
	ctx := m.ctx
	return func() tea.Msg {
		go client.Run(ctx)
		return cableStartedMsg{client}
	}
}

type cableStartedMsg struct{ client *cable.Client }

func (m Model) waitEvent() tea.Cmd {
	if m.cable == nil {
		return nil
	}
	events := m.cable.Events()
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
		m.layout()
		m.ready = true
		m.refreshFeed(false)
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case channelsLoadedMsg:
		return m.handleChannelsLoaded(msg)

	case agentsLoadedMsg:
		m.agents = msg.agents
		m.rebuildSidebar()
		return m, nil

	case mentionablesLoadedMsg:
		m.comp.mentionables = msg.mentionables
		return m, nil

	case historyLoadedMsg:
		m.store.MergeHistory(msg.channelID, msg.messages)
		if msg.channelID == m.focusedID {
			m.refreshFeed(true)
		}
		return m, nil

	case threadsLoadedMsg:
		if c, ok := m.store.Channel(m.focusedID); ok && c.ID == msg.parentID {
			m.threadPicker.active = true
			m.threadPicker.threads = msg.threads
			m.threadPicker.sel = 0
		}
		return m, nil

	case sentMsg:
		return m.handleSent(msg)

	case agentMessagedMsg:
		m.sending = false
		m.comp.reset()
		m.pendingAgent = nil
		m.focusedID = msg.channelID
		m.store.MergeHistory(msg.channelID, []api.Message{msg.message})
		if m.cable != nil {
			m.cable.Subscribe(msg.channelID)
		}
		m.refreshFeed(true)
		// Reload channels so the fresh DM channel gets its full record.
		return m, m.fetchChannels()

	case cableStartedMsg:
		m.cable = msg.client
		return m, m.waitEvent()

	case cableEventMsg:
		return m.handleCable(msg.ev)

	case markedReadMsg:
		m.store.ClearUnread(msg.channelID)
		return m, nil

	case sendFailedMsg:
		m.sending = false
		m.softErr = "send failed: " + msg.err.Error()
		return m, nil

	case softErrMsg:
		m.softErr = msg.err.Error()
		return m, nil

	case fatalErrMsg:
		m.fatal = msg.err
		return m, tea.Quit
	}

	// Cursor blink and other component messages flow to the composer.
	if m.focus == focusComposer {
		cmd := m.comp.update(msg)
		return m, cmd
	}
	return m, nil
}

func (m Model) handleChannelsLoaded(msg channelsLoadedMsg) (Model, tea.Cmd) {
	m.store.SetChannels(msg.channels)
	m.rebuildSidebar()
	if len(m.items) == 0 {
		m.fatal = fmt.Errorf("no channels visible; join one on the web first")
		return m, tea.Quit
	}
	first := m.loadingMsg != ""
	m.loadingMsg = ""

	var cmds []tea.Cmd
	if first {
		m.selected = firstUnreadItem(m.store, m.items)
		open := m.items[m.selected]
		if open.channel != nil {
			m.focusedID = open.channel.ID
			cmds = append(cmds, m.fetchHistory(*open.channel))
			if open.channel.Member {
				cmds = append(cmds, m.markRead(*open.channel))
			}
		}
	}

	// Subscribe to every visible channel — MessagesChannel is policy-gated.
	var ids []int64
	for _, c := range msg.channels {
		ids = append(ids, c.ID)
	}
	if !m.cableOnce {
		m.cableOnce = true
		cmds = append(cmds, m.startCable(ids))
	} else if m.cable != nil {
		for _, id := range ids {
			m.cable.Subscribe(id)
		}
	}
	m.updatePlaceholder()
	return m, tea.Batch(cmds...)
}

func (m Model) handleSent(msg sentMsg) (Model, tea.Cmd) {
	m.sending = false
	m.comp.reset()
	m.store.MergeHistory(msg.channelID, []api.Message{msg.message})
	if msg.channelID == m.focusedID {
		m.refreshFeed(true)
	}
	var cmds []tea.Cmd
	if c, ok := m.store.Channel(msg.channelID); ok {
		if msg.inThread && !c.Member {
			c.Member = true // the server auto-joined us on post
			m.store.Upsert(c)
		}
		if c.Member {
			cmds = append(cmds, m.markRead(c))
		}
	}
	return m, tea.Batch(cmds...)
}

// ── Key handling ────────────────────────────────────────────────────────

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// Always-available controls.
	switch key {
	case "ctrl+c":
		return m, tea.Quit
	case "ctrl+r":
		return m, tea.Batch(m.fetchChannels(), m.fetchAgents())
	case "pgup", "pgdown":
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		return m, cmd
	case "ctrl+t":
		return m.openThreadPicker()
	}

	if m.threadPicker.active {
		return m.handlePickerKey(key)
	}

	switch key {
	case "tab":
		if m.focus == focusComposer && m.comp.mentionOpen {
			m.comp.completeMention()
			return m, nil
		}
		m.cycleFocus()
		return m, m.focusCmd()
	case "esc":
		return m.handleEsc()
	}

	switch m.focus {
	case focusSidebar:
		return m.handleSidebarKey(key)
	case focusFeed:
		switch key {
		case "q":
			return m, tea.Quit
		case "t":
			return m.openThreadPicker()
		}
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		return m, cmd
	default:
		return m.handleComposerKey(msg)
	}
}

func (m Model) handleComposerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		if m.comp.mentionOpen {
			m.comp.completeMention()
			return m, nil
		}
		return m.send()
	case "up", "down":
		if m.comp.mentionOpen {
			delta := 1
			if msg.String() == "up" {
				delta = -1
			}
			m.comp.moveMention(delta)
			return m, nil
		}
	}
	cmd := m.comp.update(msg)
	return m, cmd
}

func (m Model) handleSidebarKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "q":
		return m, tea.Quit
	case "j", "down":
		return m.moveSelection(1)
	case "k", "up":
		return m.moveSelection(-1)
	case "enter", "l", "right":
		return m.openSelected(true)
	}
	return m, nil
}

func (m Model) handlePickerKey(key string) (tea.Model, tea.Cmd) {
	p := &m.threadPicker
	switch key {
	case "esc", "q":
		p.active = false
	case "j", "down":
		if p.sel < len(p.threads)-1 {
			p.sel++
		}
	case "k", "up":
		if p.sel > 0 {
			p.sel--
		}
	case "enter":
		if len(p.threads) == 0 {
			p.active = false
			return m, nil
		}
		thread := p.threads[p.sel]
		p.active = false
		return m.openThread(thread)
	}
	return m, nil
}

func (m Model) handleEsc() (tea.Model, tea.Cmd) {
	if m.comp.mentionOpen {
		m.comp.closeMention()
		return m, nil
	}
	if m.threadReturn != 0 {
		return m.returnFromThread()
	}
	if m.focus != focusComposer {
		m.focus = focusComposer
		return m, m.focusCmd()
	}
	return m, nil
}

func (m *Model) cycleFocus() {
	m.focus = (m.focus + 1) % 3
}

func (m *Model) focusCmd() tea.Cmd {
	if m.focus == focusComposer {
		return m.comp.focus()
	}
	m.comp.blur()
	return nil
}

// ── Navigation ──────────────────────────────────────────────────────────

func (m Model) moveSelection(delta int) (tea.Model, tea.Cmd) {
	next := m.selected + delta
	if next < 0 || next >= len(m.items) {
		return m, nil
	}
	m.selected = next
	return m.openSelected(false)
}

func (m Model) openSelected(focusComposerAfter bool) (tea.Model, tea.Cmd) {
	it := m.items[m.selected]
	m.threadReturn = 0

	var cmds []tea.Cmd
	if it.isAgentStub() {
		m.pendingAgent = it.agent
		m.focusedID = 0
		m.refreshFeed(true)
	} else if it.channel.ID != m.focusedID {
		m.pendingAgent = nil
		m.focusedID = it.channel.ID
		m.refreshFeed(true)
		cmds = append(cmds, m.fetchHistory(*it.channel))
		if it.channel.Member {
			cmds = append(cmds, m.markRead(*it.channel))
		}
	}
	if focusComposerAfter {
		m.focus = focusComposer
		cmds = append(cmds, m.comp.focus())
	}
	m.updatePlaceholder()
	return m, tea.Batch(cmds...)
}

func (m Model) openThreadPicker() (tea.Model, tea.Cmd) {
	c, ok := m.store.Channel(m.focusedID)
	if !ok || c.Kind == "thread" {
		return m, nil
	}
	return m, m.fetchThreads(c)
}

func (m Model) openThread(thread api.Channel) (tea.Model, tea.Cmd) {
	m.threadReturn = m.focusedID
	m.store.Upsert(thread)
	m.focusedID = thread.ID
	m.pendingAgent = nil
	m.refreshFeed(true)
	if m.cable != nil {
		m.cable.Subscribe(thread.ID)
	}
	m.updatePlaceholder()
	cmds := []tea.Cmd{m.fetchHistory(thread)}
	if thread.Member {
		cmds = append(cmds, m.markRead(thread))
	}
	return m, tea.Batch(cmds...)
}

func (m Model) returnFromThread() (tea.Model, tea.Cmd) {
	parentID := m.threadReturn
	m.threadReturn = 0
	if c, ok := m.store.Channel(parentID); ok {
		m.focusedID = c.ID
		m.refreshFeed(true)
		m.updatePlaceholder()
		return m, m.fetchHistory(c)
	}
	m.updatePlaceholder()
	return m, nil
}

// ── Sending ─────────────────────────────────────────────────────────────

func (m Model) send() (tea.Model, tea.Cmd) {
	body := strings.TrimSpace(m.comp.value())
	if body == "" || m.sending {
		return m, nil
	}

	if m.pendingAgent != nil {
		m.sending = true
		return m, m.sendToAgent(*m.pendingAgent, body)
	}
	if c, ok := m.store.Channel(m.focusedID); ok {
		m.sending = true
		return m, m.sendToChannel(c, body)
	}
	return m, nil
}

// ── Cable ───────────────────────────────────────────────────────────────

func (m Model) handleCable(ev cable.Event) (tea.Model, tea.Cmd) {
	cmds := []tea.Cmd{m.waitEvent()}

	switch ev.Type {
	case cable.EventConnected:
		reconnected := m.conn == connRetrying
		m.conn = connLive
		if reconnected {
			// Broadcasts during the outage are lost — refetch the world.
			cmds = append(cmds, m.fetchChannels())
			if c, ok := m.store.Channel(m.focusedID); ok {
				cmds = append(cmds, m.fetchHistory(c))
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

func (m *Model) rebuildSidebar() {
	m.items = buildSidebar(m.store.Channels(), m.agents)
	if m.selected >= len(m.items) {
		m.selected = 0
	}
}

func (m *Model) updatePlaceholder() {
	switch {
	case m.pendingAgent != nil:
		m.comp.setPlaceholder("message ◇ " + m.pendingAgent.Name + " — first message opens the DM")
	default:
		if c, ok := m.store.Channel(m.focusedID); ok {
			m.comp.setPlaceholder("message " + kindGlyph(c.Kind) + " " + c.Name + " — enter sends · ctrl+j newline")
		}
	}
}

func (m *Model) layout() {
	feedWidth := m.width - sidebarWidth - 1
	if feedWidth < 20 {
		feedWidth = 20
	}
	// title + composer rule + composer + popup + status bar
	feedHeight := m.height - 3 - m.comp.height() - m.comp.popupHeight()
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
	m.comp.setWidth(feedWidth)
}

func (m *Model) refreshFeed(jump bool) {
	m.layout()
	wasAtBottom := m.vp.AtBottom()
	if m.pendingAgent != nil {
		m.vp.SetContent(styleFeedTopic.Render("no conversation yet — say hello to " + m.pendingAgent.Name))
		return
	}
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

	sidebar := renderSidebar(m.store, m.cfg.WorkspaceName, m.items, m.selected, m.focus == focusSidebar, m.height-1)

	feedWidth := m.width - sidebarWidth - 1
	var pane string
	if m.threadPicker.active {
		pane = m.renderThreadPicker(feedWidth)
	} else {
		pane = m.renderFeedPane(feedWidth)
	}

	main := lipgloss.JoinHorizontal(lipgloss.Top, sidebar, pane)
	return main + "\n" + m.statusBar()
}

func (m Model) renderFeedPane(width int) string {
	title := m.feedTitle(width)

	var parts []string
	parts = append(parts, title, m.vp.View())
	if popup := m.comp.mentionPopup(width); popup != "" {
		parts = append(parts, popup)
	}
	parts = append(parts, styleComposerBar.Render(strings.Repeat("─", max(width-1, 1))))
	parts = append(parts, m.comp.view())
	return strings.Join(parts, "\n")
}

func (m Model) feedTitle(width int) string {
	if m.pendingAgent != nil {
		return styleFeedTitle.Render("◇ " + m.pendingAgent.Name)
	}
	c, ok := m.store.Channel(m.focusedID)
	if !ok {
		return ""
	}
	title := styleFeedTitle.Render(kindGlyph(c.Kind) + " " + c.Name)
	if m.threadReturn != 0 {
		if parent, ok := m.store.Channel(m.threadReturn); ok {
			title = styleFeedTopic.Render(kindGlyph(parent.Kind)+" "+parent.Name+" ▸ ") + styleFeedTitle.Render("↳ "+c.Name)
		}
	} else if c.Description != nil && *c.Description != "" {
		title += "  " + styleFeedTopic.Render(truncate(*c.Description, width-lipgloss.Width(title)-2))
	}
	return title
}

func (m Model) renderThreadPicker(width int) string {
	var rows []string
	rows = append(rows, stylePickerTitle.Render("↳ threads"), "")
	if len(m.threadPicker.threads) == 0 {
		rows = append(rows, stylePickerRow.Render("no threads in this channel yet"))
	}
	for i, t := range m.threadPicker.threads {
		row := fmt.Sprintf("↳ %s  ·  %d msgs", t.Name, t.MessagesCount)
		if i == m.threadPicker.sel {
			row = stylePickerSel.Render("▸ " + truncate(row, width-4))
		} else {
			row = stylePickerRow.Render("  " + truncate(row, width-4))
		}
		rows = append(rows, row)
	}
	rows = append(rows, "", styleFeedTopic.Render("enter open · esc close"))
	body := strings.Join(rows, "\n")
	return lipgloss.NewStyle().Width(width).Height(m.height-1).Padding(1, 2).Render(body)
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
	if m.sending {
		left += styleStatusBar.Render(" · sending…")
	}
	if m.softErr != "" {
		left += styleStatusDead.Render(" ! " + truncate(m.softErr, 40))
	}
	keys := styleStatusKeys.Render("tab focus · ctrl+t threads · enter send · ctrl+c quit ")

	gap := m.width - lipgloss.Width(left) - lipgloss.Width(keys)
	if gap < 1 {
		return left
	}
	return left + styleStatusBar.Render(strings.Repeat(" ", gap)) + keys
}

func firstUnreadItem(s *store.Store, items []sidebarItem) int {
	for i, it := range items {
		if it.channel != nil && s.Unread(it.channel.ID) > 0 {
			return i
		}
	}
	return 0
}
