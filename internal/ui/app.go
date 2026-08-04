// Package ui is the Bubble Tea TUI. Phase 3: chat (composer, mentions,
// threads, agent DMs) plus a tasks pane, command palette, message selection
// with reply-in-thread, and a notifications overlay.
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

type viewMode int

const (
	viewChat viewMode = iota
	viewTasks
)

// Bubble Tea messages.
type channelsLoadedMsg struct{ channels []api.Channel }
type agentsLoadedMsg struct{ agents []api.Agent }
type mentionablesLoadedMsg struct{ mentionables []api.Mentionable }
type historyLoadedMsg struct {
	channelID int64
	messages  []api.Message
	page      int
	older     bool // a load-older page: preserve scroll instead of jumping
}
type threadsLoadedMsg struct {
	parentID int64
	threads  []api.Channel
	rootID   int64 // >0: resolve the thread for this message instead of picking
}
type sentMsg struct {
	channelID int64 // channel the send targeted
	message   api.Message
}
type agentMessagedMsg struct {
	channelID int64
	message   api.Message
}
type tasksLoadedMsg struct{ tasks []api.Task }
type projectsLoadedMsg struct{ projects []api.Project }
type taskChangedMsg struct{ task api.Task }
type notificationsLoadedMsg struct{ items []api.Notification }
type notificationsClearedMsg struct{}
type assistEventMsg struct{ ev assistEvent }
type cableStartedMsg struct{ client *cable.Client }
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
	view     viewMode

	focusedID    int64
	pendingAgent *api.Agent
	threadReturn int64
	replyTo      *api.Message // composing a reply-in-thread to this message

	threadPicker struct {
		active  bool
		threads []api.Channel
		sel     int
	}
	pal    palette
	tasks  tasksView
	notify notifyView
	assist assistant

	feedSel    int64 // selected message id in the feed (0 = none)
	feedBlocks []msgBlock
	histPages  map[int64]int  // channel id → deepest history page loaded
	histDone   map[int64]bool // channel id → no older pages remain

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
		pal:        newPalette(),
		tasks:      newTasksView(),
		histPages:  map[int64]int{},
		histDone:   map[int64]bool{},
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.fetchChannels(), m.fetchAgents(), m.fetchMentionables(), m.fetchProjects(), m.comp.focus())
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
			return softErrMsg{err}
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

func (m Model) fetchProjects() tea.Cmd {
	client, ctx := m.client, m.ctx
	return func() tea.Msg {
		projects, err := client.Projects(ctx)
		if err != nil {
			return softErrMsg{err}
		}
		return projectsLoadedMsg{projects}
	}
}

func (m Model) fetchHistory(c api.Channel) tea.Cmd {
	return m.fetchHistoryPage(c, 1, false)
}

func (m Model) fetchHistoryPage(c api.Channel, page int, older bool) tea.Cmd {
	client, ctx := m.client, m.ctx
	return func() tea.Msg {
		msgs, err := client.Messages(ctx, c.Slug, page, 50)
		if err != nil {
			return softErrMsg{err}
		}
		return historyLoadedMsg{channelID: c.ID, messages: msgs, page: page, older: older}
	}
}

func (m Model) fetchThreads(parent api.Channel, rootID int64) tea.Cmd {
	client, ctx := m.client, m.ctx
	return func() tea.Msg {
		threads, err := client.Channels(ctx, api.ChannelsOpts{ParentChannelSlug: parent.Slug})
		if err != nil {
			return softErrMsg{err}
		}
		return threadsLoadedMsg{parentID: parent.ID, threads: threads, rootID: rootID}
	}
}

func (m Model) fetchTasks() tea.Cmd {
	client, ctx := m.client, m.ctx
	mine := m.tasks.mine
	return func() tea.Msg {
		tasks, err := client.Tasks(ctx, api.TasksOpts{Mine: mine})
		if err != nil {
			return softErrMsg{err}
		}
		return tasksLoadedMsg{tasks}
	}
}

func (m Model) toggleTask(task api.Task) tea.Cmd {
	client, ctx := m.client, m.ctx
	next := "completed"
	if task.State == "completed" || task.State == "cancelled" {
		next = "open"
	}
	return func() tea.Msg {
		updated, err := client.UpdateTaskState(ctx, task.Slug, next)
		if err != nil {
			return softErrMsg{err}
		}
		return taskChangedMsg{*updated}
	}
}

func (m Model) createTask(projectID int64, title string) tea.Cmd {
	client, ctx, userID := m.client, m.ctx, m.cfg.UserID
	return func() tea.Msg {
		task, err := client.CreateTask(ctx, projectID, title, userID)
		if err != nil {
			return softErrMsg{err}
		}
		return taskChangedMsg{*task}
	}
}

func (m Model) fetchNotifications() tea.Cmd {
	client, ctx := m.client, m.ctx
	return func() tea.Msg {
		items, err := client.Notifications(ctx, true)
		if err != nil {
			return softErrMsg{err}
		}
		return notificationsLoadedMsg{items}
	}
}

// startAssist launches one streaming completion; deltas cross into the tea
// loop over the assistant's event channel (same pattern as cable).
func (m *Model) startAssist(question string) tea.Cmd {
	m.assist.pushUser(question)
	m.assist.streaming = true
	m.assist.current = ""
	m.assist.events = make(chan assistEvent, 64)

	client, ctx := m.client, m.ctx
	events := m.assist.events
	req := api.InferenceRequest{
		Model:    assistDefaultModel,
		System:   assistSystemPrompt(m.cfg.WorkspaceName),
		Messages: append([]api.InferenceMessage(nil), m.assist.turns...),
	}
	stream := func() tea.Msg {
		go func() {
			full, usage, err := client.StreamInference(ctx, req, func(d string) {
				events <- assistEvent{delta: d}
			})
			events <- assistEvent{done: true, full: full, usage: usage, err: err}
		}()
		return nil
	}
	return tea.Batch(stream, m.waitAssist())
}

func (m Model) waitAssist() tea.Cmd {
	events := m.assist.events
	if events == nil {
		return nil
	}
	return func() tea.Msg {
		ev, ok := <-events
		if !ok {
			return nil
		}
		return assistEventMsg{ev}
	}
}

func (m Model) readAllNotifications() tea.Cmd {
	client, ctx := m.client, m.ctx
	return func() tea.Msg {
		if _, err := client.ReadAllNotifications(ctx); err != nil {
			return softErrMsg{err}
		}
		return notificationsClearedMsg{}
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

func (m Model) sendToChannel(c api.Channel, body string, replyTo int64) tea.Cmd {
	client, ctx := m.client, m.ctx
	return func() tea.Msg {
		var msg *api.Message
		var err error
		if replyTo > 0 {
			msg, err = client.SendReply(ctx, c.Slug, body, replyTo)
		} else {
			msg, err = client.SendMessage(ctx, c.Slug, body)
		}
		if err != nil {
			return sendFailedMsg{err}
		}
		return sentMsg{channelID: c.ID, message: *msg}
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

	case projectsLoadedMsg:
		m.tasks.projects = msg.projects
		return m, nil

	case historyLoadedMsg:
		return m.handleHistoryLoaded(msg)

	case threadsLoadedMsg:
		return m.handleThreadsLoaded(msg)

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
		return m, m.fetchChannels()

	case tasksLoadedMsg:
		m.tasks.loading = false
		m.tasks.tasks = msg.tasks
		if m.tasks.sel >= len(msg.tasks) {
			m.tasks.sel = 0
		}
		return m, nil

	case taskChangedMsg:
		return m, m.fetchTasks()

	case notificationsLoadedMsg:
		m.notify.active = true
		m.notify.items = msg.items
		m.notify.sel = 0
		return m, nil

	case notificationsClearedMsg:
		m.notify.items = nil
		return m, nil

	case assistEventMsg:
		return m.handleAssistEvent(msg.ev)

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

	// Cursor blink and other component messages.
	var cmds []tea.Cmd
	if m.pal.active {
		cmds = append(cmds, m.pal.update(msg))
	} else if m.tasks.inputOpen {
		var cmd tea.Cmd
		m.tasks.input, cmd = m.tasks.input.Update(msg)
		cmds = append(cmds, cmd)
	} else if m.focus == focusComposer {
		cmds = append(cmds, m.comp.update(msg))
	}
	return m, tea.Batch(cmds...)
}

func (m Model) handleAssistEvent(ev assistEvent) (tea.Model, tea.Cmd) {
	if ev.done {
		m.assist.streaming = false
		if ev.err != nil {
			m.softErr = "assistant: " + ev.err.Error()
			if ev.full != "" {
				m.assist.pushAssistant(ev.full) // keep the partial answer
			}
		} else {
			m.assist.pushAssistant(ev.full)
			if ev.usage != nil {
				m.assist.usageLine = fmt.Sprintf("· %d in → %d out tokens", ev.usage.InputTokens, ev.usage.OutputTokens)
			}
		}
		m.assist.events = nil
		m.refreshAssist()
		return m, nil
	}
	m.assist.current += ev.delta
	m.refreshAssist()
	return m, m.waitAssist()
}

func (m *Model) refreshAssist() {
	if !m.assist.active {
		return
	}
	feedWidth := m.width - sidebarWidth - 1
	m.vp.SetContent(m.assist.render(m.renderer, feedWidth))
	m.vp.GotoBottom()
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

func (m Model) handleHistoryLoaded(msg historyLoadedMsg) (tea.Model, tea.Cmd) {
	if msg.older {
		if len(msg.messages) == 0 {
			m.histDone[msg.channelID] = true
			m.softErr = "beginning of history"
			return m, nil
		}
		m.histPages[msg.channelID] = msg.page
		if msg.channelID != m.focusedID {
			m.store.MergeHistory(msg.channelID, msg.messages)
			return m, nil
		}
		// Prepending grows the content above the viewport — keep what the
		// user is looking at stationary.
		oldTotal := m.vp.TotalLineCount()
		oldOffset := m.vp.YOffset
		m.store.MergeHistory(msg.channelID, msg.messages)
		m.refreshFeed(false)
		m.vp.SetYOffset(oldOffset + (m.vp.TotalLineCount() - oldTotal))
		return m, nil
	}

	if m.histPages[msg.channelID] == 0 {
		m.histPages[msg.channelID] = 1
	}
	m.store.MergeHistory(msg.channelID, msg.messages)
	if msg.channelID == m.focusedID {
		m.refreshFeed(true)
	}
	return m, nil
}

func (m Model) handleThreadsLoaded(msg threadsLoadedMsg) (tea.Model, tea.Cmd) {
	if msg.rootID > 0 {
		// Resolving `t` on a message: open its thread, or arm reply-mode.
		for _, t := range msg.threads {
			if t.RootMessageID != nil && *t.RootMessageID == msg.rootID {
				return m.openThread(t)
			}
		}
		if root := m.findMessage(msg.rootID); root != nil {
			m.replyTo = root
			m.focus = focusComposer
			m.updatePlaceholder()
			return m, m.comp.focus()
		}
		return m, nil
	}

	if c, ok := m.store.Channel(m.focusedID); ok && c.ID == msg.parentID {
		m.threadPicker.active = true
		m.threadPicker.threads = msg.threads
		m.threadPicker.sel = 0
	}
	return m, nil
}

func (m Model) handleSent(msg sentMsg) (Model, tea.Cmd) {
	m.sending = false
	m.comp.reset()
	wasReply := m.replyTo != nil
	m.replyTo = nil

	actualChannel := msg.message.ChannelID
	m.store.MergeHistory(actualChannel, []api.Message{msg.message})

	var cmds []tea.Cmd
	if wasReply && actualChannel != msg.channelID {
		// The server routed the reply into a (possibly new) thread — resolve
		// and open it via the thread list.
		if m.cable != nil {
			m.cable.Subscribe(actualChannel)
		}
		if parent, ok := m.store.Channel(msg.channelID); ok {
			rootID := int64(0)
			if msg.message.ThreadRootMessageID != nil {
				rootID = *msg.message.ThreadRootMessageID
			}
			cmds = append(cmds, m.fetchThreads(parent, rootID))
		}
	} else if actualChannel == m.focusedID {
		m.refreshFeed(true)
	}

	if c, ok := m.store.Channel(actualChannel); ok {
		if c.Kind == "thread" && !c.Member {
			c.Member = true // the server auto-joined us on post
			m.store.Upsert(c)
		}
		if c.Member {
			cmds = append(cmds, m.markRead(c))
		}
	}
	m.updatePlaceholder()
	return m, tea.Batch(cmds...)
}

// ── Key handling ────────────────────────────────────────────────────────

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	switch key {
	case "ctrl+c":
		return m, tea.Quit
	case "ctrl+k":
		return m.openPalette()
	case "ctrl+n":
		return m, m.fetchNotifications()
	case "ctrl+g":
		return m.toggleAssistant()
	case "ctrl+r":
		return m, tea.Batch(m.fetchChannels(), m.fetchAgents(), m.fetchProjects())
	}

	if m.pal.active {
		return m.handlePaletteKey(msg)
	}
	if m.notify.active {
		return m.handleNotifyKey(key)
	}
	if m.view == viewTasks {
		return m.handleTasksKey(msg)
	}

	switch key {
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
		return m.handleFeedKey(msg)
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

// handleFeedKey is selection mode: j/k moves a message cursor, t opens or
// starts the selected message's thread.
func (m Model) handleFeedKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q":
		return m, tea.Quit
	case "j", "down":
		m.moveFeedSel(1)
		return m, nil
	case "k", "up":
		m.moveFeedSel(-1)
		return m, nil
	case "g":
		m.vp.GotoTop()
		return m, nil
	case "G":
		m.vp.GotoBottom()
		return m, nil
	case "t":
		return m.threadForSelection()
	case "o":
		return m.loadOlder()
	}
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return m, cmd
}

func (m Model) loadOlder() (tea.Model, tea.Cmd) {
	c, ok := m.store.Channel(m.focusedID)
	if !ok || m.histDone[c.ID] {
		return m, nil
	}
	page := m.histPages[c.ID]
	if page == 0 {
		page = 1
	}
	return m, m.fetchHistoryPage(c, page+1, true)
}

func (m Model) handleTasksKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	t := &m.tasks
	key := msg.String()

	if t.inputOpen && t.pickOpen {
		switch key {
		case "esc":
			t.closeInput()
		case "j", "down":
			if t.pickSel < len(t.projects)-1 {
				t.pickSel++
			}
		case "k", "up":
			if t.pickSel > 0 {
				t.pickSel--
			}
		case "enter":
			project := t.projects[t.pickSel]
			title := t.pendingTitle
			t.closeInput()
			return m, m.createTask(project.ID, title)
		}
		return m, nil
	}

	if t.inputOpen {
		switch key {
		case "esc":
			t.closeInput()
			return m, nil
		case "enter":
			title := strings.TrimSpace(t.input.Value())
			if title == "" {
				return m, nil
			}
			switch len(t.projects) {
			case 0:
				t.closeInput()
				m.softErr = "no projects yet — create one on the web first"
				return m, nil
			case 1:
				t.closeInput()
				return m, m.createTask(t.projects[0].ID, title)
			default:
				t.pendingTitle = title
				t.pickOpen = true
				t.pickSel = 0
				return m, nil
			}
		}
		var cmd tea.Cmd
		t.input, cmd = t.input.Update(msg)
		return m, cmd
	}

	switch key {
	case "esc", "q":
		m.view = viewChat
		m.focus = focusComposer
		return m, m.comp.focus()
	case "j", "down":
		t.move(1)
	case "k", "up":
		t.move(-1)
	case "x", "enter":
		if task, ok := t.selected(); ok {
			return m, m.toggleTask(task)
		}
	case "n":
		return m, t.openInput()
	case "m":
		t.mine = !t.mine
		t.loading = true
		return m, m.fetchTasks()
	case "r":
		t.loading = true
		return m, m.fetchTasks()
	}
	return m, nil
}

func (m Model) handlePaletteKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.pal.close()
		return m, m.focusCmd()
	case "up":
		m.pal.move(-1)
		return m, nil
	case "down":
		m.pal.move(1)
		return m, nil
	case "enter":
		item, ok := m.pal.selected()
		m.pal.close()
		if !ok {
			return m, m.focusCmd()
		}
		return m.runPaletteItem(item)
	}
	return m, m.pal.update(msg)
}

func (m Model) handleNotifyKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc", "q":
		m.notify.active = false
		return m, m.focusCmd()
	case "j", "down":
		m.notify.move(1)
	case "k", "up":
		m.notify.move(-1)
	case "R":
		return m, m.readAllNotifications()
	case "enter":
		item, ok := m.notify.selected()
		if !ok || item.Message == nil {
			return m, nil
		}
		m.notify.active = false
		m.view = viewChat
		if _, ok := m.store.Channel(item.Message.ChannelID); ok {
			return m.openChannelByID(item.Message.ChannelID)
		}
		m.softErr = "channel not loaded — refresh (ctrl+r) and retry"
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
	if m.assist.active {
		return m.toggleAssistant()
	}
	if m.replyTo != nil {
		m.replyTo = nil
		m.updatePlaceholder()
		return m, nil
	}
	if m.focus == focusFeed && m.feedSel != 0 {
		m.feedSel = 0
		m.refreshFeed(false)
		m.focus = focusComposer
		return m, m.focusCmd()
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
	if m.focus == focusFeed {
		m.selectLastMessage()
	} else {
		m.feedSel = 0
		m.refreshFeed(false)
	}
}

func (m *Model) focusCmd() tea.Cmd {
	if m.focus == focusComposer {
		return m.comp.focus()
	}
	m.comp.blur()
	return nil
}

// ── Palette ─────────────────────────────────────────────────────────────

func (m Model) openPalette() (tea.Model, tea.Cmd) {
	var items []paletteItem
	for _, it := range m.items {
		switch {
		case it.channel != nil:
			items = append(items, paletteItem{
				label:     kindGlyph(it.channel.Kind) + " " + it.channel.Name,
				action:    "channel",
				channelID: it.channel.ID,
			})
		case it.agent != nil:
			items = append(items, paletteItem{
				label:  "◇ " + it.agent.Name,
				hint:   "agent DM",
				action: "agent",
				agent:  it.agent,
			})
		}
	}
	items = append(items,
		paletteItem{label: "◆ assistant", hint: "local claude session", action: actionAssistant},
		paletteItem{label: "☑ tasks: mine", action: actionTasksMine},
		paletteItem{label: "☑ tasks: all open", action: actionTasksAll},
		paletteItem{label: "☐ new task…", action: actionNewTask},
		paletteItem{label: "◉ notifications", action: actionNotifications},
	)
	m.comp.blur()
	return m, m.pal.open(items)
}

func (m Model) runPaletteItem(item paletteItem) (tea.Model, tea.Cmd) {
	switch item.action {
	case "channel":
		m.view = viewChat
		return m.openChannelByID(item.channelID)
	case "agent":
		m.view = viewChat
		m.pendingAgent = item.agent
		m.focusedID = 0
		m.threadReturn = 0
		m.refreshFeed(true)
		m.updatePlaceholder()
		m.focus = focusComposer
		return m, m.comp.focus()
	case actionTasksMine, actionTasksAll:
		m.view = viewTasks
		m.tasks.active = true
		m.tasks.mine = item.action == actionTasksMine
		m.tasks.loading = true
		return m, m.fetchTasks()
	case actionNewTask:
		m.view = viewTasks
		m.tasks.loading = true
		return m, tea.Batch(m.fetchTasks(), m.tasks.openInput())
	case actionNotifications:
		return m, m.fetchNotifications()
	case actionAssistant:
		if !m.assist.active {
			return m.toggleAssistant()
		}
		m.focus = focusComposer
		return m, m.comp.focus()
	}
	return m, m.focusCmd()
}

func (m Model) openChannelByID(id int64) (tea.Model, tea.Cmd) {
	for i, it := range m.items {
		if it.channel != nil && it.channel.ID == id {
			m.selected = i
			return m.openSelected(true)
		}
	}
	if c, ok := m.store.Channel(id); ok { // thread or non-sidebar channel
		return m.openThread(c)
	}
	return m, nil
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
	m.replyTo = nil
	m.feedSel = 0

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
	return m, m.fetchThreads(c, 0)
}

func (m Model) openThread(thread api.Channel) (tea.Model, tea.Cmd) {
	if thread.Kind == "thread" && thread.ParentChannelID != nil {
		m.threadReturn = *thread.ParentChannelID
	} else {
		m.threadReturn = 0
	}
	m.store.Upsert(thread)
	m.focusedID = thread.ID
	m.pendingAgent = nil
	m.replyTo = nil
	m.feedSel = 0
	m.refreshFeed(true)
	if m.cable != nil {
		m.cable.Subscribe(thread.ID)
	}
	m.updatePlaceholder()
	m.focus = focusComposer
	cmds := []tea.Cmd{m.fetchHistory(thread), m.comp.focus()}
	if thread.Member {
		cmds = append(cmds, m.markRead(thread))
	}
	return m, tea.Batch(cmds...)
}

func (m Model) returnFromThread() (tea.Model, tea.Cmd) {
	parentID := m.threadReturn
	m.threadReturn = 0
	m.feedSel = 0
	if c, ok := m.store.Channel(parentID); ok {
		m.focusedID = c.ID
		m.refreshFeed(true)
		m.updatePlaceholder()
		return m, m.fetchHistory(c)
	}
	m.updatePlaceholder()
	return m, nil
}

// ── Feed selection ──────────────────────────────────────────────────────

func (m *Model) selectLastMessage() {
	msgs := m.store.Messages(m.focusedID)
	if len(msgs) == 0 {
		m.feedSel = 0
		return
	}
	m.feedSel = msgs[len(msgs)-1].ID
	m.refreshFeed(false)
	m.scrollToSelection()
}

func (m *Model) moveFeedSel(delta int) {
	if len(m.feedBlocks) == 0 {
		return
	}
	idx := -1
	for i, b := range m.feedBlocks {
		if b.ID == m.feedSel {
			idx = i
			break
		}
	}
	if idx == -1 {
		idx = len(m.feedBlocks) - 1
	} else {
		idx += delta
		if idx < 0 {
			idx = 0
		}
		if idx >= len(m.feedBlocks) {
			idx = len(m.feedBlocks) - 1
		}
	}
	m.feedSel = m.feedBlocks[idx].ID
	m.refreshFeed(false)
	m.scrollToSelection()
}

func (m *Model) scrollToSelection() {
	for _, b := range m.feedBlocks {
		if b.ID != m.feedSel {
			continue
		}
		if b.Line < m.vp.YOffset {
			m.vp.SetYOffset(b.Line)
		} else if end := b.Line + b.Rows; end > m.vp.YOffset+m.vp.Height {
			m.vp.SetYOffset(end - m.vp.Height)
		}
		return
	}
}

func (m Model) findMessage(id int64) *api.Message {
	msgs := m.store.Messages(m.focusedID)
	for i := range msgs {
		if msgs[i].ID == id {
			return &msgs[i]
		}
	}
	return nil
}

// threadForSelection opens the selected message's thread if one exists, or
// arms reply-in-new-thread mode.
func (m Model) threadForSelection() (tea.Model, tea.Cmd) {
	if m.feedSel == 0 {
		return m, nil
	}
	c, ok := m.store.Channel(m.focusedID)
	if !ok || c.Kind == "thread" {
		return m, nil // no nested threads
	}
	return m, m.fetchThreads(c, m.feedSel)
}

// ── Sending ─────────────────────────────────────────────────────────────

// toggleAssistant flips the local Claude pane; the transcript survives
// toggling, and the feed re-renders on the way back.
func (m Model) toggleAssistant() (tea.Model, tea.Cmd) {
	m.assist.active = !m.assist.active
	if m.assist.active {
		m.view = viewChat
		m.updatePlaceholder()
		m.refreshAssist()
		m.focus = focusComposer
		return m, tea.Batch(m.comp.focus(), m.waitAssist())
	}
	m.updatePlaceholder()
	m.refreshFeed(true)
	return m, nil
}

func (m Model) send() (tea.Model, tea.Cmd) {
	body := strings.TrimSpace(m.comp.value())
	if body == "" || m.sending {
		return m, nil
	}

	if m.assist.active {
		if m.assist.streaming {
			return m, nil
		}
		m.comp.reset()
		cmd := m.startAssist(body)
		m.refreshAssist()
		return m, cmd
	}

	if m.pendingAgent != nil {
		m.sending = true
		return m, m.sendToAgent(*m.pendingAgent, body)
	}
	if c, ok := m.store.Channel(m.focusedID); ok {
		m.sending = true
		replyTo := int64(0)
		if m.replyTo != nil {
			replyTo = m.replyTo.ID
		}
		return m, m.sendToChannel(c, body, replyTo)
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
	case m.assist.active:
		m.comp.setPlaceholder("ask claude — enter sends · esc back to chat · conversation is metered")
	case m.replyTo != nil:
		who := m.replyTo.Sender.Name
		if who == "" {
			who = "message"
		}
		m.comp.setPlaceholder("reply in thread to " + who + " — esc cancels")
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
	if m.assist.active {
		// The viewport belongs to the assistant right now; chat re-renders
		// when the pane toggles back.
		m.refreshAssist()
		return
	}
	wasAtBottom := m.vp.AtBottom()
	if m.pendingAgent != nil {
		m.vp.SetContent(styleFeedTopic.Render("no conversation yet — say hello to " + m.pendingAgent.Name))
		return
	}
	content, blocks := m.renderer.Render(m.store.Messages(m.focusedID), m.feedSel)
	m.feedBlocks = blocks
	m.vp.SetContent(content)
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
	switch {
	case m.pal.active:
		pane = lipgloss.NewStyle().Width(feedWidth).Height(m.height-1).Padding(1, 2).Render(m.pal.render(feedWidth))
	case m.notify.active:
		pane = m.notify.render(feedWidth, m.height-1)
	case m.view == viewTasks:
		pane = m.tasks.render(feedWidth, m.height-1)
	case m.threadPicker.active:
		pane = m.renderThreadPicker(feedWidth)
	default:
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
	if m.assist.active {
		return styleFeedTitle.Render("◆ assistant") + "  " + styleFeedTopic.Render(assistDefaultModel)
	}
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
			rows = append(rows, stylePickerSel.Render("▸ "+truncate(row, width-4)))
		} else {
			rows = append(rows, stylePickerRow.Render("  "+truncate(row, width-4)))
		}
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
	keys := styleStatusKeys.Render("ctrl+k palette · ctrl+n inbox · ctrl+t threads · ctrl+c quit ")

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
