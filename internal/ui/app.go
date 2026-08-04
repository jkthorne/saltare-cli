// Package ui is the Bubble Tea TUI. Phase 3: chat (composer, mentions,
// threads, agent DMs) plus a tasks pane, command palette, message selection
// with reply-in-thread, and a notifications overlay.
package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jkthorne/saltare/cli/internal/api"
	"github.com/jkthorne/saltare/cli/internal/assist"
	"github.com/jkthorne/saltare/cli/internal/cable"
	"github.com/jkthorne/saltare/cli/internal/config"
	"github.com/jkthorne/saltare/cli/internal/store"
	"github.com/jkthorne/saltare/cli/internal/tablefmt"
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
	viewDocs
	viewFiles
	viewDB
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
type discussionLoadedMsg struct{ channel api.Channel }
type uploadsLoadedMsg struct{ uploads []api.Upload }
type uploadDoneMsg struct{ upload api.Upload }
type uploadRemovedMsg struct{ slug string }
type downloadDoneMsg struct {
	target string
	bytes  int64
}
type databasesLoadedMsg struct{ databases []api.Database }
type dbOpenedMsg struct{ database api.Database }
type dbRowsLoadedMsg struct {
	slug string
	page int
	rows []api.DBRow
}
type rowUpdatedMsg struct{ row api.DBRow }
type taskDetailLoadedMsg struct{ task api.Task }
type channelResolvedMsg struct{ channel api.Channel }
type messageResolvedMsg struct{ message api.Message }
type docsLoadedMsg struct{ docs []api.Document }
type docLoadedMsg struct {
	doc  api.Document
	edit bool // open the editor straight after (new-document flow)
}
type docSavedMsg struct{ doc api.Document }
type docConflictMsg struct{ conflict docConflict }
type editorFinishedMsg struct{ err error }
type messageEditedMsg struct{ message api.Message }
type messageDeletedMsg struct {
	channelID int64
	id        int64
}
type searchDebounceMsg struct{ gen int }
type searchResultsMsg struct {
	gen     int
	results *api.SearchResults
	err     error
}
type toastClearMsg struct{ gen int }
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

	focusedID     int64
	pendingAgent  *api.Agent
	threadReturn  int64
	replyTo       *api.Message // composing a reply-in-thread to this message
	editing       *api.Message // composer holds an edit of this message
	confirmDelete *api.Message // armed y/n delete confirmation

	threadPicker struct {
		active  bool
		threads []api.Channel
		sel     int
	}
	embedPicker struct {
		active bool
		refs   []embedRef
		sel    int
	}
	pal    palette
	tasks  tasksView
	notify notifyView
	assist assistant
	search searchView
	docs   docsView
	files  filesView
	db     dbView

	toast    string
	toastGen int

	feedSel    int64 // selected message id in the feed (0 = none)
	feedBlocks []msgBlock
	histPages  map[int64]int  // channel id → deepest history page loaded
	histDone   map[int64]bool // channel id → no older pages remain

	drafts     map[string]string   // "{ws}/{channel-slug}" → unsent composer text
	unreadMark map[int64]time.Time // read cursor snapshotted at channel open

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
		search:     newSearchView(),
		docs:       newDocsView(),
		files:      newFilesView(),
		db:         newDBView(),
		histPages:  map[int64]int{},
		histDone:   map[int64]bool{},
		drafts:     config.LoadDrafts(),
		unreadMark: map[int64]time.Time{},
	}
}

func (m Model) Init() tea.Cmd {
	// The composer is already focused (set in newComposer — mutations here on
	// the value receiver would be discarded); textarea.Blink starts the cursor.
	return tea.Batch(m.fetchChannels(), m.fetchAgents(), m.fetchMentionables(), m.fetchProjects(), textarea.Blink)
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

func (m Model) setTaskState(task api.Task, state string) tea.Cmd {
	client, ctx := m.client, m.ctx
	return func() tea.Msg {
		updated, err := client.UpdateTaskState(ctx, task.Slug, state)
		if err != nil {
			return softErrMsg{err}
		}
		return taskChangedMsg{*updated}
	}
}

// openTaskDiscussion goes through the find-or-create endpoint every time —
// it's idempotent and joins the caller, so unread tracking works even for
// tasks that predate discussion channels.
func (m Model) openTaskDiscussion(task api.Task) (tea.Model, tea.Cmd) {
	client, ctx := m.client, m.ctx
	slug := task.Slug
	return m, func() tea.Msg {
		channel, err := client.TaskDiscussion(ctx, slug)
		if err != nil {
			return softErrMsg{err}
		}
		return discussionLoadedMsg{*channel}
	}
}

func (m Model) createTask(projectID int64, title string) tea.Cmd {
	client, ctx, userID := m.client, m.ctx, m.cfg.UserID
	return func() tea.Msg {
		task, err := client.CreateTask(ctx, projectID, title, userID, api.TaskCreateOpts{})
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

// startAssist launches one tool-loop turn; text deltas and tool traces
// cross into the tea loop over the assistant's event channel (same pattern
// as cable). Tool execution happens in the goroutine, against the REST API
// as the signed-in user.
func (m *Model) startAssist(question string) tea.Cmd {
	m.assist.pushUser(question)
	m.assist.streaming = true
	m.assist.current = ""
	m.assist.events = make(chan assistEvent, 64)

	client, ctx := m.client, m.ctx
	events := m.assist.events
	exec := &assist.Executor{Client: client, UserID: m.cfg.UserID}
	history := append([]api.InferenceMessage(nil), m.assist.turns...)
	opts := assist.LoopOpts{
		Model:  assistDefaultModel,
		System: assist.SystemPrompt(m.cfg.WorkspaceName, m.cfg.UserName, true),
		Tools:  assist.Tools(),
		OnText: func(d string) { events <- assistEvent{delta: d} },
		OnTool: func(name string, input map[string]any) {
			events <- assistEvent{tool: toolCallLine(api.ContentBlock{Name: name, Input: input})}
		},
	}
	stream := func() tea.Msg {
		go func() {
			turns, res, err := assist.RunLoop(ctx, client, exec, history, opts)
			ev := assistEvent{done: true, err: err, turns: turns}
			if res != nil {
				ev.full = res.Text
				ev.usage = &res.Usage
			}
			events <- ev
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
		m.docs.resize(m.width-sidebarWidth-1, m.height-1)
		if m.docs.viewing != nil {
			m.docs.showDocument(m.docs.viewing)
		}
		m.db.resize(m.width-sidebarWidth-1, m.height-1)
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
		if m.tasks.detail != nil && m.tasks.detail.ID == msg.task.ID {
			changed := msg.task
			m.tasks.detail = &changed
		}
		return m, m.fetchTasks()

	case discussionLoadedMsg:
		m.view = viewChat
		m.tasks.detail = nil
		return m.openThread(msg.channel)

	case taskDetailLoadedMsg:
		m.view = viewTasks
		m.tasks.active = true
		task := msg.task
		m.tasks.detail = &task
		if len(m.tasks.tasks) == 0 {
			m.tasks.loading = true
			return m, m.fetchTasks()
		}
		return m, nil

	case channelResolvedMsg:
		m.view = viewChat
		return m.openThread(msg.channel)

	case messageResolvedMsg:
		m.view = viewChat
		hit := msg.message
		if _, ok := m.store.Channel(hit.ChannelID); ok || m.sidebarHas(hit.ChannelID) {
			next, cmd := m.openChannelByID(hit.ChannelID)
			if model, isModel := next.(Model); isModel {
				model.feedSel = hit.ID
				return model, cmd
			}
			return next, cmd
		}
		m.softErr = "channel not loaded — refresh (ctrl+r) and retry"
		return m, nil

	case notificationsLoadedMsg:
		m.notify.active = true
		m.notify.items = msg.items
		m.notify.sel = 0
		return m, nil

	case notificationsClearedMsg:
		m.notify.items = nil
		return m, nil

	case docsLoadedMsg:
		m.docs.loading = false
		m.docs.list = msg.docs
		if m.docs.sel >= len(msg.docs) {
			m.docs.sel = 0
		}
		return m, nil

	case docLoadedMsg:
		m.docs.loading = false
		doc := msg.doc
		m.docs.upsert(doc)
		m.docs.showDocument(&doc)
		if msg.edit {
			return m.startDocEdit(doc)
		}
		return m, nil

	case docSavedMsg:
		m.docs.upsert(msg.doc)
		if m.docs.editPath != "" {
			_ = os.Remove(m.docs.editPath)
		}
		m.docs.editSlug, m.docs.editPath, m.docs.editBody = "", "", ""
		m.docs.conflict = nil
		doc := msg.doc
		m.docs.showDocument(&doc)
		return m, m.showToast("document saved")

	case docConflictMsg:
		conflict := msg.conflict
		m.docs.conflict = &conflict
		return m, nil

	case editorFinishedMsg:
		return m.handleEditorFinished(msg)

	case uploadsLoadedMsg:
		pending := m.files.pendingSelect
		if !m.files.setList(msg.uploads) {
			m.softErr = "upload not found: " + pending
		}
		return m, nil

	case uploadDoneMsg:
		m.files.loading = false
		m.files.list = append([]api.Upload{msg.upload}, m.files.list...)
		m.files.sel = 0
		return m, m.showToast("uploaded — [[upload:" + msg.upload.Slug + "]]")

	case uploadRemovedMsg:
		kept := m.files.list[:0]
		for _, u := range m.files.list {
			if u.Slug != msg.slug {
				kept = append(kept, u)
			}
		}
		m.files.list = kept
		if m.files.sel >= len(kept) && m.files.sel > 0 {
			m.files.sel--
		}
		return m, m.showToast("deleted " + msg.slug)

	case downloadDoneMsg:
		return m, m.showToast("wrote " + msg.target + " (" + tablefmt.HumanSize(msg.bytes) + ")")

	case databasesLoadedMsg:
		m.db.loading = false
		m.db.list = msg.databases
		if m.db.sel >= len(msg.databases) {
			m.db.sel = 0
		}
		return m, nil

	case dbOpenedMsg:
		database := msg.database
		m.db.database = &database
		if m.db.rows != nil || database.RowsCount == 0 {
			m.db.loading = false
			m.db.buildGrid()
		}
		return m, nil

	case dbRowsLoadedMsg:
		if m.db.database != nil && m.db.database.Slug != msg.slug {
			return m, nil // stale fetch from a previous table
		}
		if msg.page == 1 {
			m.db.rows = msg.rows
		} else {
			m.db.rows = append(m.db.rows, msg.rows...)
		}
		m.db.pages = msg.page
		m.db.more = len(msg.rows) == dbPageSize
		if m.db.database != nil {
			m.db.loading = false
			m.db.buildGrid()
		}
		return m, nil

	case rowUpdatedMsg:
		if m.db.detailIdx >= 0 && m.db.detailIdx < len(m.db.rows) && m.db.rows[m.db.detailIdx].ID == msg.row.ID {
			m.db.rows[m.db.detailIdx] = msg.row
		}
		m.db.buildGrid()
		return m, m.showToast("saved")

	case messageEditedMsg:
		m.sending = false
		m.editing = nil
		m.restoreDraft(m.focusedID) // bring back whatever the edit interrupted
		m.updatePlaceholder()
		// The cable event carries the same payload; Apply dedupes by ID.
		m.store.Apply(cable.Event{Type: cable.EventMessageUpdated, ChannelID: msg.message.ChannelID, Message: &msg.message})
		m.refreshFeed(false)
		return m, nil

	case messageDeletedMsg:
		m.store.Apply(cable.Event{Type: cable.EventMessageDeleted, ChannelID: msg.channelID, MessageID: msg.id})
		if m.feedSel == msg.id {
			m.feedSel = 0
		}
		m.refreshFeed(false)
		return m, nil

	case searchDebounceMsg:
		if m.search.active && msg.gen == m.search.gen {
			return m, m.runSearch()
		}
		return m, nil

	case searchResultsMsg:
		if !m.search.active || msg.gen != m.search.gen {
			return m, nil // a newer query superseded this response
		}
		if msg.err != nil {
			m.search.loading = false
			m.softErr = "search: " + msg.err.Error()
			return m, nil
		}
		m.search.setResults(msg.results)
		return m, nil

	case toastClearMsg:
		if msg.gen == m.toastGen {
			m.toast = ""
		}
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
	} else if m.search.active {
		cmds = append(cmds, m.updateSearchInput(msg))
	} else if m.docs.inputOpen {
		var cmd tea.Cmd
		m.docs.input, cmd = m.docs.input.Update(msg)
		cmds = append(cmds, cmd)
	} else if m.files.inputOpen {
		var cmd tea.Cmd
		m.files.input, cmd = m.files.input.Update(msg)
		cmds = append(cmds, cmd)
	} else if m.db.editOpen {
		var cmd tea.Cmd
		m.db.input, cmd = m.db.input.Update(msg)
		cmds = append(cmds, cmd)
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
		// The loop returns the full history (assistant turns, tool calls,
		// and tool results included) — adopt it, pair-aware-trimmed.
		if ev.turns != nil {
			m.assist.turns = assist.TrimTurns(ev.turns, assistMaxTurns)
		}
		if ev.err != nil {
			m.softErr = "assistant: " + ev.err.Error()
			if ev.full != "" {
				m.assist.pushAssistant(ev.full) // keep the partial answer
			}
		} else if ev.usage != nil {
			m.assist.usageLine = fmt.Sprintf("· %d in → %d out tokens", ev.usage.InputTokens, ev.usage.OutputTokens)
		}
		m.assist.events = nil
		m.refreshAssist()
		return m, nil
	}
	if ev.tool != "" {
		if m.assist.current != "" && !strings.HasSuffix(m.assist.current, "\n") {
			m.assist.current += "\n"
		}
		m.assist.current += "◇ " + ev.tool + "\n"
		m.refreshAssist()
		return m, m.waitAssist()
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
	m.dropDraft(msg.channelID)
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
	case "ctrl+f":
		return m.openSearch("", "")
	case "ctrl+o":
		return m.openDocs()
	case "ctrl+r":
		return m, tea.Batch(m.fetchChannels(), m.fetchAgents(), m.fetchProjects())
	}

	if m.pal.active {
		return m.handlePaletteKey(msg)
	}
	if m.search.active {
		return m.handleSearchKey(msg)
	}
	if m.notify.active {
		return m.handleNotifyKey(key)
	}
	if m.view == viewTasks {
		return m.handleTasksKey(msg)
	}
	if m.view == viewDocs {
		return m.handleDocsKey(msg)
	}
	if m.view == viewFiles {
		return m.handleFilesKey(msg)
	}
	if m.view == viewDB {
		return m.handleDBKey(msg)
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
	if m.embedPicker.active {
		return m.handleEmbedPickerKey(key)
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
// starts the selected message's thread, e/d edit or delete an own message,
// y copies a permalink.
func (m Model) handleFeedKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// An armed delete confirmation eats the next key — before "q" quits.
	if m.confirmDelete != nil {
		target := *m.confirmDelete
		m.confirmDelete = nil
		if msg.String() == "y" {
			return m, m.deleteMessage(target)
		}
		return m, nil
	}

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
	case "e":
		return m.beginEdit()
	case "d":
		return m.armDelete()
	case "y":
		return m.copySelectionPermalink()
	case "enter":
		return m.followSelectionEmbeds()
	case "/":
		if c, ok := m.store.Channel(m.focusedID); ok {
			return m.openSearch(c.Slug, c.Title())
		}
		return m.openSearch("", "")
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

// ── Message edit/delete ─────────────────────────────────────────────────

func (m Model) ownMessage(msg api.Message) bool {
	return msg.Sender.Type == "User" && msg.Sender.ID == m.cfg.UserID
}

// clearEditState abandons any in-flight edit/delete when the composer's
// target changes (channel switch, assistant toggle).
func (m *Model) clearEditState() {
	if m.editing != nil {
		m.comp.reset()
	}
	m.editing = nil
	m.confirmDelete = nil
}

// ── Drafts ──────────────────────────────────────────────────────────────

func (m *Model) draftKey(channelID int64) string {
	c, ok := m.store.Channel(channelID)
	if !ok {
		return ""
	}
	return m.cfg.WorkspaceSlug + "/" + c.Slug
}

// stashDraft saves the composer's unsent text for the current channel and
// persists the draft file (tiny; a channel switch is rare enough to write
// through — drafts then survive crashes, not just clean exits).
func (m *Model) stashDraft() {
	if m.editing != nil || m.assist.active || m.focusedID == 0 {
		return
	}
	key := m.draftKey(m.focusedID)
	if key == "" {
		return
	}
	text := m.comp.value()
	if strings.TrimSpace(text) == "" {
		if _, had := m.drafts[key]; !had {
			return
		}
		delete(m.drafts, key)
	} else {
		m.drafts[key] = text
	}
	_ = config.SaveDrafts(m.drafts)
}

// restoreDraft fills the composer with the target channel's saved draft.
func (m *Model) restoreDraft(channelID int64) {
	m.comp.reset()
	key := m.draftKey(channelID)
	if key == "" {
		return
	}
	if draft, ok := m.drafts[key]; ok {
		m.comp.setValue(draft)
	}
}

// dropDraft forgets a channel's draft after a successful send.
func (m *Model) dropDraft(channelID int64) {
	key := m.draftKey(channelID)
	if key == "" {
		return
	}
	if _, had := m.drafts[key]; !had {
		return
	}
	delete(m.drafts, key)
	_ = config.SaveDrafts(m.drafts)
}

// snapshotUnread pins the read cursor before markRead advances it, so the
// feed can draw the NEW rule where the unreads began.
func (m *Model) snapshotUnread(c api.Channel) {
	if c.Member && c.LastReadAt != nil && c.Unread() > 0 {
		m.unreadMark[c.ID] = *c.LastReadAt
	} else {
		delete(m.unreadMark, c.ID)
	}
}

// firstUnreadID finds the message the NEW rule sits above: the oldest
// message from someone else that postdates the snapshotted cursor.
func (m *Model) firstUnreadID() int64 {
	mark, ok := m.unreadMark[m.focusedID]
	if !ok {
		return 0
	}
	for _, msg := range m.store.Messages(m.focusedID) {
		if msg.CreatedAt.After(mark) && !m.ownMessage(msg) && !msg.IsSystemEvent() {
			return msg.ID
		}
	}
	return 0
}

// editableSelection returns the selected message when the user may mutate
// it (the server enforces policy regardless — this is UX, not security).
func (m *Model) editableSelection() *api.Message {
	target := m.findMessage(m.feedSel)
	if target == nil || target.IsSystemEvent() {
		return nil
	}
	if !m.ownMessage(*target) {
		m.softErr = "not your message"
		return nil
	}
	return target
}

func (m Model) beginEdit() (tea.Model, tea.Cmd) {
	target := m.editableSelection()
	if target == nil {
		return m, nil
	}
	m.stashDraft() // the composer may hold an unsent draft — keep it
	m.editing = target
	m.replyTo = nil
	m.comp.setValue(target.Body)
	m.focus = focusComposer
	m.feedSel = 0
	m.refreshFeed(false)
	m.updatePlaceholder()
	return m, m.comp.focus()
}

func (m Model) armDelete() (tea.Model, tea.Cmd) {
	target := m.editableSelection()
	if target == nil {
		return m, nil
	}
	m.confirmDelete = target
	return m, nil
}

func (m Model) copySelectionPermalink() (tea.Model, tea.Cmd) {
	target := m.findMessage(m.feedSel)
	if target == nil {
		return m, nil
	}
	c, ok := m.store.Channel(target.ChannelID)
	if !ok {
		return m, nil
	}
	if err := copyToClipboard(m.messagePermalink(c.Kind, c.Slug, target.ID)); err != nil {
		m.softErr = "copy failed: " + err.Error()
		return m, nil
	}
	return m, m.showToast("permalink copied")
}

func (m Model) editMessage(target api.Message, body string) tea.Cmd {
	client, ctx := m.client, m.ctx
	return func() tea.Msg {
		updated, err := client.EditMessage(ctx, target.ID, body)
		if err != nil {
			return sendFailedMsg{err}
		}
		return messageEditedMsg{*updated}
	}
}

func (m Model) deleteMessage(target api.Message) tea.Cmd {
	client, ctx := m.client, m.ctx
	return func() tea.Msg {
		if err := client.DeleteMessage(ctx, target.ID); err != nil {
			return softErrMsg{err}
		}
		return messageDeletedMsg{channelID: target.ChannelID, id: target.ID}
	}
}

func (m Model) handleTasksKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	t := &m.tasks
	key := msg.String()

	if t.detail != nil {
		task := *t.detail
		switch key {
		case "esc", "q":
			t.detail = nil
		case "enter", "o":
			return m.openTaskDiscussion(task)
		case "x":
			return m, m.toggleTask(task)
		case "s":
			return m, m.setTaskState(task, nextTaskState(task.State))
		case "y":
			if err := copyToClipboard("[[task:" + task.Slug + "]]"); err == nil {
				return m, m.showToast("embed copied")
			}
		}
		return m, nil
	}

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
	case "enter":
		if task, ok := t.selected(); ok {
			selected := task
			t.detail = &selected
		}
	case "x":
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
	if m.confirmDelete != nil {
		m.confirmDelete = nil
		return m, nil
	}
	if m.assist.active {
		return m.toggleAssistant()
	}
	if m.editing != nil {
		m.editing = nil
		m.restoreDraft(m.focusedID)
		m.updatePlaceholder()
		return m, nil
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
				label:     kindGlyph(it.channel.Kind) + " " + it.channel.Title(),
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
		paletteItem{label: "⌕ search workspace", action: actionSearch},
		paletteItem{label: "▤ documents", action: actionDocs},
		paletteItem{label: "⇱ files", action: actionFiles},
		paletteItem{label: "▦ databases", action: actionDB},
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
		m.stashDraft()
		m.pendingAgent = item.agent
		m.focusedID = 0
		m.threadReturn = 0
		m.comp.reset()
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
	case actionSearch:
		return m.openSearch("", "")
	case actionDocs:
		return m.openDocs()
	case actionFiles:
		return m.openFiles("")
	case actionDB:
		return m.openDB()
	case actionAssistant:
		if !m.assist.active {
			return m.toggleAssistant()
		}
		m.focus = focusComposer
		return m, m.comp.focus()
	}
	return m, m.focusCmd()
}

// ── Follow embeds ───────────────────────────────────────────────────────

// followSelectionEmbeds routes enter on a selected message: one [[embed]]
// follows immediately, several open a picker.
func (m Model) followSelectionEmbeds() (tea.Model, tea.Cmd) {
	target := m.findMessage(m.feedSel)
	if target == nil {
		return m, nil
	}
	refs := extractEmbeds(target.Body)
	switch len(refs) {
	case 0:
		return m, m.showToast("no references in this message")
	case 1:
		return m.followEmbed(refs[0])
	default:
		m.embedPicker.active = true
		m.embedPicker.refs = refs
		m.embedPicker.sel = 0
		return m, nil
	}
}

func (m Model) handleEmbedPickerKey(key string) (tea.Model, tea.Cmd) {
	p := &m.embedPicker
	switch key {
	case "esc", "q":
		p.active = false
	case "j", "down":
		if p.sel < len(p.refs)-1 {
			p.sel++
		}
	case "k", "up":
		if p.sel > 0 {
			p.sel--
		}
	case "enter":
		ref := p.refs[p.sel]
		p.active = false
		return m.followEmbed(ref)
	}
	return m, nil
}

func (m Model) renderEmbedPicker(width int) string {
	var rows []string
	rows = append(rows, stylePickerTitle.Render("⟨references⟩"), "")
	for i, ref := range m.embedPicker.refs {
		row := "⟨" + ref.kind + ":" + ref.ref + "⟩"
		if i == m.embedPicker.sel {
			rows = append(rows, stylePickerSel.Render("▸ "+truncate(row, width-4)))
		} else {
			rows = append(rows, stylePickerRow.Render("  "+truncate(row, width-4)))
		}
	}
	rows = append(rows, "", styleFeedTopic.Render("enter follow · esc close"))
	return lipgloss.NewStyle().Width(width).Height(m.height-1).Padding(1, 2).Render(strings.Join(rows, "\n"))
}

// followEmbed opens whatever a reference points at; types sal can't render
// yet fall back to a hint (the server's SLUG_TYPES/ID_TYPES routing).
func (m Model) followEmbed(ref embedRef) (tea.Model, tea.Cmd) {
	switch ref.kind {
	case "doc":
		return m.openDocBySlug(ref.ref)
	case "task":
		client, ctx := m.client, m.ctx
		slug := ref.ref
		return m, func() tea.Msg {
			task, err := client.Task(ctx, slug)
			if err != nil {
				return softErrMsg{err}
			}
			return taskDetailLoadedMsg{*task}
		}
	case "channel":
		for _, c := range m.store.Channels() {
			if c.Slug == ref.ref {
				return m.openChannelByID(c.ID)
			}
		}
		client, ctx := m.client, m.ctx
		slug := ref.ref
		return m, func() tea.Msg {
			channel, err := client.Channel(ctx, slug)
			if err != nil {
				return softErrMsg{err}
			}
			return channelResolvedMsg{*channel}
		}
	case "msg":
		id, err := strconv.ParseInt(ref.ref, 10, 64)
		if err != nil {
			return m, nil
		}
		client, ctx := m.client, m.ctx
		return m, func() tea.Msg {
			message, err := client.MessageByID(ctx, id)
			if err != nil {
				return softErrMsg{err}
			}
			return messageResolvedMsg{*message}
		}
	case "agent":
		return m.followAgentEmbed(ref.ref)
	case "upload":
		return m.openFiles(ref.ref)
	case "db":
		return m.openDBGrid(ref.ref)
	default:
		return m, m.showToast("⟨" + ref.kind + ":…⟩ opens on the web — sal follows doc/task/channel/msg/agent/upload/db")
	}
}

func (m Model) followAgentEmbed(slug string) (tea.Model, tea.Cmd) {
	for i := range m.agents {
		if m.agents[i].Slug != slug {
			continue
		}
		agent := m.agents[i]
		// An existing agent DM lives in the sidebar as an agent_dm channel.
		for _, it := range m.items {
			if it.channel != nil && it.channel.Kind == "agent_dm" &&
				it.channel.HostID != nil && *it.channel.HostID == agent.ID {
				return m.openChannelByID(it.channel.ID)
			}
		}
		return m.runPaletteItem(paletteItem{action: "agent", agent: &agent})
	}
	m.softErr = "agent not found: " + slug
	return m, nil
}

// ── Documents ───────────────────────────────────────────────────────────

func (m Model) openDocs() (tea.Model, tea.Cmd) {
	m.pal.close()
	m.search.close()
	m.notify.active = false
	m.threadPicker.active = false
	m.view = viewDocs
	m.comp.blur()
	m.docs.resize(m.width-sidebarWidth-1, m.height-1)
	if len(m.docs.list) == 0 {
		m.docs.loading = true
		return m, m.fetchDocuments()
	}
	return m, nil
}

// openDocBySlug jumps straight into the reader (follow-embed, search).
func (m Model) openDocBySlug(slug string) (tea.Model, tea.Cmd) {
	next, cmd := m.openDocs()
	model := next.(Model)
	model.docs.loading = true
	return model, tea.Batch(cmd, model.fetchDocument(slug, false))
}

func (m Model) fetchDocuments() tea.Cmd {
	client, ctx := m.client, m.ctx
	return func() tea.Msg {
		docs, err := client.Documents(ctx)
		if err != nil {
			return softErrMsg{err}
		}
		return docsLoadedMsg{docs}
	}
}

func (m Model) fetchDocument(slug string, edit bool) tea.Cmd {
	client, ctx := m.client, m.ctx
	return func() tea.Msg {
		doc, err := client.Document(ctx, slug)
		if err != nil {
			return softErrMsg{err}
		}
		return docLoadedMsg{doc: *doc, edit: edit}
	}
}

func (m Model) createDocument(title string) tea.Cmd {
	client, ctx := m.client, m.ctx
	return func() tea.Msg {
		doc, err := client.CreateDocument(ctx, title, "")
		if err != nil {
			return softErrMsg{err}
		}
		return docLoadedMsg{doc: *doc, edit: true} // straight into the editor
	}
}

func (m Model) handleDocsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	d := &m.docs
	key := msg.String()

	if d.conflict != nil {
		switch key {
		case "o": // overwrite: retry the save without the concurrency guard
			conflict := *d.conflict
			d.conflict = nil
			return m, m.saveDocumentBody(conflict.slug, conflict.body, time.Time{})
		case "esc", "q":
			path := d.conflict.path
			d.conflict = nil
			d.editSlug, d.editPath, d.editBody = "", "", ""
			return m, m.showToast("kept your copy at " + path)
		}
		return m, nil
	}

	if d.inputOpen {
		switch key {
		case "esc":
			d.inputOpen = false
			d.input.Blur()
			return m, nil
		case "enter":
			title := strings.TrimSpace(d.input.Value())
			if title == "" {
				return m, nil
			}
			d.inputOpen = false
			d.input.Blur()
			d.input.SetValue("")
			d.loading = true
			return m, m.createDocument(title)
		}
		var cmd tea.Cmd
		d.input, cmd = d.input.Update(msg)
		return m, cmd
	}

	if d.viewing != nil {
		switch key {
		case "esc", "q":
			d.viewing = nil
			return m, nil
		case "e":
			return m.startDocEdit(*d.viewing)
		case "y":
			if err := copyToClipboard("[[doc:" + d.viewing.Slug + "]]"); err == nil {
				return m, m.showToast("embed copied")
			}
			return m, nil
		}
		var cmd tea.Cmd
		d.vp, cmd = d.vp.Update(msg)
		return m, cmd
	}

	switch key {
	case "esc", "q":
		m.view = viewChat
		m.focus = focusComposer
		return m, m.comp.focus()
	case "j", "down":
		d.move(1)
	case "k", "up":
		d.move(-1)
	case "enter":
		if doc, ok := d.selected(); ok {
			d.loading = true
			return m, m.fetchDocument(doc.Slug, false)
		}
	case "e":
		if doc, ok := d.selected(); ok {
			d.loading = true
			return m, m.fetchDocument(doc.Slug, true)
		}
	case "n":
		d.inputOpen = true
		return m, d.input.Focus()
	case "y":
		if doc, ok := d.selected(); ok {
			if err := copyToClipboard("[[doc:" + doc.Slug + "]]"); err == nil {
				return m, m.showToast("embed copied")
			}
		}
	case "r":
		d.loading = true
		return m, m.fetchDocuments()
	}
	return m, nil
}

// startDocEdit writes the body to the crash-safe edit buffer and suspends
// the TUI into the user's editor; editorFinishedMsg resumes the flow.
func (m Model) startDocEdit(doc api.Document) (tea.Model, tea.Cmd) {
	path, err := config.EditBufferPath(doc.Slug)
	if err != nil {
		m.softErr = "editor: " + err.Error()
		return m, nil
	}
	if err := os.WriteFile(path, []byte(doc.Body), 0o600); err != nil {
		m.softErr = "editor: " + err.Error()
		return m, nil
	}
	m.docs.editSlug = doc.Slug
	m.docs.editBase = doc.UpdatedAt
	m.docs.editPath = path
	m.docs.editBody = doc.Body

	// Leave Stdin/Stdout nil — bubbletea wires the real TTY on release.
	return m, tea.ExecProcess(config.EditorCommand(path), func(err error) tea.Msg {
		return editorFinishedMsg{err}
	})
}

func (m Model) handleEditorFinished(msg editorFinishedMsg) (tea.Model, tea.Cmd) {
	d := &m.docs
	slug, path := d.editSlug, d.editPath
	if slug == "" {
		return m, nil
	}
	if msg.err != nil {
		m.softErr = "editor: " + msg.err.Error()
		return m, nil // buffer file stays for recovery
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		m.softErr = "editor: " + err.Error()
		return m, nil
	}
	body := string(raw)
	if body == d.editBody {
		_ = os.Remove(path)
		d.editSlug, d.editPath, d.editBody = "", "", ""
		return m, m.showToast("no changes")
	}
	return m, m.saveDocumentBody(slug, body, d.editBase)
}

func (m Model) saveDocumentBody(slug, body string, base time.Time) tea.Cmd {
	client, ctx := m.client, m.ctx
	path := m.docs.editPath
	return func() tea.Msg {
		doc, err := client.UpdateDocumentBody(ctx, slug, body, base)
		if err != nil {
			var apiErr *api.APIError
			if errors.As(err, &apiErr) && apiErr.Code == "stale_document" {
				return docConflictMsg{docConflict{slug: slug, path: path, body: body}}
			}
			return softErrMsg{err}
		}
		return docSavedMsg{*doc}
	}
}

// ── Files view ──────────────────────────────────────────────────────────

func (m Model) openFiles(pendingSelect string) (tea.Model, tea.Cmd) {
	m.pal.close()
	m.search.close()
	m.notify.active = false
	m.threadPicker.active = false
	m.view = viewFiles
	m.comp.blur()
	m.files.pendingSelect = pendingSelect
	m.files.loading = true
	return m, m.fetchUploads()
}

func (m Model) fetchUploads() tea.Cmd {
	client, ctx := m.client, m.ctx
	return func() tea.Msg {
		uploads, err := client.Uploads(ctx, api.UploadsOpts{})
		if err != nil {
			return softErrMsg{err}
		}
		return uploadsLoadedMsg{uploads}
	}
}

func (m Model) handleFilesKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	f := &m.files
	key := msg.String()

	if f.confirmRm != nil {
		target := *f.confirmRm
		f.confirmRm = nil
		if key == "y" {
			client, ctx := m.client, m.ctx
			return m, func() tea.Msg {
				if err := client.DeleteUpload(ctx, target.Slug); err != nil {
					return softErrMsg{err}
				}
				return uploadRemovedMsg{target.Slug}
			}
		}
		return m, nil
	}

	if f.inputOpen {
		switch key {
		case "esc":
			f.inputOpen = false
			f.input.Blur()
			return m, nil
		case "enter":
			path := expandHome(strings.TrimSpace(f.input.Value()))
			if path == "" {
				return m, nil
			}
			info, err := os.Stat(path)
			if err != nil {
				m.softErr = err.Error()
				return m, nil
			}
			if info.IsDir() {
				m.softErr = path + " is a directory"
				return m, nil
			}
			f.inputOpen = false
			f.input.Blur()
			f.input.SetValue("")
			f.loading = true
			client, ctx := m.client, m.ctx
			return m, func() tea.Msg {
				upload, err := client.UploadFile(ctx, path, "")
				if err != nil {
					return softErrMsg{err}
				}
				return uploadDoneMsg{*upload}
			}
		}
		var cmd tea.Cmd
		f.input, cmd = f.input.Update(msg)
		return m, cmd
	}

	switch key {
	case "esc", "q":
		m.view = viewChat
		m.focus = focusComposer
		return m, m.comp.focus()
	case "j", "down":
		f.move(1)
	case "k", "up":
		f.move(-1)
	case "d":
		if u, ok := f.selected(); ok {
			return m.downloadUpload(u)
		}
	case "u":
		f.inputOpen = true
		return m, f.input.Focus()
	case "x":
		if u, ok := f.selected(); ok {
			sel := u
			f.confirmRm = &sel
		}
	case "y":
		if u, ok := f.selected(); ok {
			if err := copyToClipboard("[[upload:" + u.Slug + "]]"); err == nil {
				return m, m.showToast("embed copied")
			}
		}
	case "r":
		f.loading = true
		return m, m.fetchUploads()
	}
	return m, nil
}

// downloadUpload saves to the cwd under the original filename; existing
// targets are refused (the CLI's -o/--force flow handles those).
func (m Model) downloadUpload(u api.Upload) (tea.Model, tea.Cmd) {
	target := u.OriginalFilename
	if target == "" {
		target = u.Slug
	}
	if _, err := os.Stat(target); err == nil {
		return m, m.showToast(target + " exists — use sal files get -o")
	}
	client, ctx := m.client, m.ctx
	return m, func() tea.Msg {
		body, _, _, err := client.DownloadUpload(ctx, u.Slug)
		if err != nil {
			return softErrMsg{err}
		}
		defer body.Close()
		written, err := config.SafeWriteFile(target, body)
		if err != nil {
			return softErrMsg{err}
		}
		return downloadDoneMsg{target: target, bytes: written}
	}
}

// ── Database view ───────────────────────────────────────────────────────

func (m Model) openDB() (tea.Model, tea.Cmd) {
	m.pal.close()
	m.search.close()
	m.notify.active = false
	m.threadPicker.active = false
	m.view = viewDB
	m.comp.blur()
	m.db.level = dbLevelList
	m.db.resize(m.width-sidebarWidth-1, m.height-1)
	if len(m.db.list) == 0 {
		m.db.loading = true
		return m, m.fetchDatabases()
	}
	return m, nil
}

// openDBGrid jumps straight into a table's grid (list enter, follow-embed).
func (m Model) openDBGrid(slug string) (tea.Model, tea.Cmd) {
	next, cmd := m.openDB()
	model := next.(Model)
	model.db.level = dbLevelGrid
	model.db.loading = true
	model.db.database = nil
	model.db.rows = nil
	model.db.pages = 0
	model.db.colOff = 0
	return model, tea.Batch(cmd, model.fetchDatabase(slug), model.fetchDBRows(slug, 1))
}

func (m Model) fetchDatabases() tea.Cmd {
	client, ctx := m.client, m.ctx
	return func() tea.Msg {
		databases, err := client.Databases(ctx)
		if err != nil {
			return softErrMsg{err}
		}
		return databasesLoadedMsg{databases}
	}
}

func (m Model) fetchDatabase(slug string) tea.Cmd {
	client, ctx := m.client, m.ctx
	return func() tea.Msg {
		database, err := client.Database(ctx, slug)
		if err != nil {
			return softErrMsg{err}
		}
		return dbOpenedMsg{*database}
	}
}

func (m Model) fetchDBRows(slug string, page int) tea.Cmd {
	client, ctx := m.client, m.ctx
	return func() tea.Msg {
		rows, err := client.DatabaseRows(ctx, slug, page, dbPageSize)
		if err != nil {
			return softErrMsg{err}
		}
		return dbRowsLoadedMsg{slug: slug, page: page, rows: rows}
	}
}

func (m Model) handleDBKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	d := &m.db
	key := msg.String()

	if d.level == dbLevelDetail {
		return m.handleDBDetailKey(msg)
	}

	if d.level == dbLevelGrid {
		switch key {
		case "esc", "q":
			d.level = dbLevelList
			if len(d.list) == 0 {
				d.loading = true
				return m, m.fetchDatabases()
			}
			return m, nil
		case "h", "left":
			d.colOff--
			d.buildGrid()
			return m, nil
		case "l", "right":
			d.colOff++
			d.buildGrid()
			return m, nil
		case "o":
			if d.more && d.database != nil {
				return m, m.fetchDBRows(d.database.Slug, d.pages+1)
			}
			return m, nil
		case "enter":
			if d.built && len(d.rows) > 0 {
				d.detailIdx = d.grid.Cursor()
				d.fieldSel = 0
				d.level = dbLevelDetail
			}
			return m, nil
		case "y":
			if d.database != nil {
				if err := copyToClipboard("[[db:" + d.database.Slug + "]]"); err == nil {
					return m, m.showToast("embed copied")
				}
			}
			return m, nil
		case "r":
			if d.database != nil {
				return m.openDBGrid(d.database.Slug)
			}
			return m, nil
		}
		if d.built {
			var cmd tea.Cmd
			d.grid, cmd = d.grid.Update(msg)
			return m, cmd
		}
		return m, nil
	}

	// Database list.
	switch key {
	case "esc", "q":
		m.view = viewChat
		m.focus = focusComposer
		return m, m.comp.focus()
	case "j", "down":
		d.move(1)
	case "k", "up":
		d.move(-1)
	case "enter":
		if db, ok := d.selected(); ok {
			return m.openDBGrid(db.Slug)
		}
	case "r":
		d.loading = true
		return m, m.fetchDatabases()
	}
	return m, nil
}

func (m Model) handleDBDetailKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	d := &m.db
	key := msg.String()
	columns := d.columns()

	if d.editOpen {
		switch key {
		case "esc":
			d.editOpen = false
			d.input.Blur()
			return m, nil
		case "enter":
			row := d.currentRow()
			if row == nil {
				d.editOpen = false
				return m, nil
			}
			// Whole-hash replace: copy, set the edited key as a raw string
			// (the server coerces per column type), PATCH.
			data := make(map[string]any, len(row.Data))
			for k, v := range row.Data {
				data[k] = v
			}
			value := strings.TrimSpace(d.input.Value())
			if value == "" {
				data[d.editKey] = nil
			} else {
				data[d.editKey] = value
			}
			d.editOpen = false
			d.input.Blur()
			client, ctx := m.client, m.ctx
			dbSlug, rowID := d.database.Slug, row.ID
			return m, func() tea.Msg {
				updated, err := client.UpdateRowData(ctx, dbSlug, rowID, data)
				if err != nil {
					return softErrMsg{err}
				}
				return rowUpdatedMsg{*updated}
			}
		}
		var cmd tea.Cmd
		d.input, cmd = d.input.Update(msg)
		return m, cmd
	}

	switch key {
	case "esc", "q":
		d.level = dbLevelGrid
	case "j", "down":
		if d.fieldSel < len(columns)-1 {
			d.fieldSel++
		}
	case "k", "up":
		if d.fieldSel > 0 {
			d.fieldSel--
		}
	case "enter":
		row := d.currentRow()
		if row == nil || d.fieldSel >= len(columns) {
			return m, nil
		}
		col := columns[d.fieldSel]
		if col.Type == "formula" {
			return m, m.showToast("computed column — edited by its formula")
		}
		d.editOpen = true
		d.editKey = col.Key
		d.input.SetValue(tablefmt.RenderCell(row.Data[col.Key]))
		d.input.CursorEnd()
		return m, d.input.Focus()
	}
	return m, nil
}

// ── Search ──────────────────────────────────────────────────────────────

const searchDebounce = 300 * time.Millisecond

func (m Model) openSearch(channelSlug, channelName string) (tea.Model, tea.Cmd) {
	m.pal.close()
	m.notify.active = false
	m.threadPicker.active = false
	m.comp.blur()
	return m, m.search.open(channelSlug, channelName)
}

func (m Model) handleSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.search.close()
		return m, m.focusCmd()
	case "up":
		m.search.move(-1)
		return m, nil
	case "down":
		m.search.move(1)
		return m, nil
	case "y":
		if row, ok := m.search.selected(); ok {
			return m, m.copyReference(row)
		}
		return m, nil
	case "enter":
		row, ok := m.search.selected()
		if !ok {
			return m, nil
		}
		return m.openSearchResult(row)
	}
	return m, m.updateSearchInput(msg)
}

// updateSearchInput forwards a message to the query input and re-arms the
// debounce timer when the text actually changed.
func (m *Model) updateSearchInput(msg tea.Msg) tea.Cmd {
	before := m.search.input.Value()
	var cmd tea.Cmd
	m.search.input, cmd = m.search.input.Update(msg)
	if m.search.input.Value() == before {
		return cmd
	}
	m.search.gen++
	gen := m.search.gen
	return tea.Batch(cmd, tea.Tick(searchDebounce, func(time.Time) tea.Msg {
		return searchDebounceMsg{gen}
	}))
}

func (m *Model) runSearch() tea.Cmd {
	query := strings.TrimSpace(m.search.input.Value())
	if len(query) < 2 {
		m.search.rows = nil
		m.search.ran = false
		return nil
	}
	m.search.loading = true
	client, ctx := m.client, m.ctx
	gen, channel := m.search.gen, m.search.channel
	return func() tea.Msg {
		results, err := client.Search(ctx, query, api.SearchOpts{Channel: channel})
		return searchResultsMsg{gen: gen, results: results, err: err}
	}
}

func (m Model) openSearchResult(row searchRow) (tea.Model, tea.Cmd) {
	switch {
	case row.message != nil:
		hit := row.message
		m.search.close()
		m.view = viewChat
		var next tea.Model = m
		var cmd tea.Cmd
		if _, inStore := m.store.Channel(hit.ChannelID); inStore || m.sidebarHas(hit.ChannelID) {
			next, cmd = m.openChannelByID(hit.ChannelID)
		} else {
			// A hit in a channel we never loaded (e.g. a thread): open it
			// from the search metadata; history fetch fills the feed.
			next, cmd = m.openThread(api.Channel{
				ID: hit.ChannelID, Slug: hit.Channel.Slug, Name: hit.Channel.Name, Kind: hit.Channel.Kind,
			})
		}
		if model, ok := next.(Model); ok {
			model.feedSel = hit.ID // highlights once the page containing it renders
			return model, cmd
		}
		return next, cmd
	case row.task != nil:
		m.search.close()
		m.view = viewTasks
		m.tasks.active = true
		m.tasks.mine = false
		m.tasks.loading = true
		return m, m.fetchTasks()
	case row.document != nil:
		return m, m.copyReference(row)
	case row.upload != nil:
		slug := row.upload.Slug
		m.search.close()
		return m.openFiles(slug)
	}
	return m, nil
}

func (m *Model) sidebarHas(channelID int64) bool {
	for _, it := range m.items {
		if it.channel != nil && it.channel.ID == channelID {
			return true
		}
	}
	return false
}

// copyReference copies a row's permalink/embed and toasts the outcome.
func (m *Model) copyReference(row searchRow) tea.Cmd {
	label, err := m.copyRowReference(row)
	if err != nil {
		m.softErr = "copy failed: " + err.Error()
		return nil
	}
	if label == "" {
		return nil
	}
	return m.showToast(label)
}

func (m *Model) showToast(text string) tea.Cmd {
	m.toast = text
	m.toastGen++
	gen := m.toastGen
	return tea.Tick(3*time.Second, func(time.Time) tea.Msg {
		return toastClearMsg{gen}
	})
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
	m.stashDraft() // before edit state clears — an in-flight edit is not a draft
	m.threadReturn = 0
	m.replyTo = nil
	m.clearEditState()
	m.feedSel = 0

	var cmds []tea.Cmd
	if it.isAgentStub() {
		m.pendingAgent = it.agent
		m.focusedID = 0
		m.comp.reset()
		m.refreshFeed(true)
	} else if it.channel.ID != m.focusedID {
		m.pendingAgent = nil
		m.snapshotUnread(*it.channel) // before markRead advances the cursor
		m.focusedID = it.channel.ID
		m.restoreDraft(it.channel.ID)
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
	m.stashDraft()
	if thread.Kind == "thread" && thread.ParentChannelID != nil {
		m.threadReturn = *thread.ParentChannelID
	} else {
		m.threadReturn = 0
	}
	m.store.Upsert(thread)
	m.snapshotUnread(thread)
	m.focusedID = thread.ID
	m.restoreDraft(thread.ID)
	m.pendingAgent = nil
	m.replyTo = nil
	m.clearEditState()
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
	m.stashDraft()
	m.threadReturn = 0
	m.feedSel = 0
	if c, ok := m.store.Channel(parentID); ok {
		m.focusedID = c.ID
		m.restoreDraft(c.ID)
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
		m.clearEditState()
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

	if m.editing != nil {
		m.sending = true
		return m, m.editMessage(*m.editing, body)
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
	case m.editing != nil:
		m.comp.setPlaceholder("editing message — enter saves · esc cancels")
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
			m.comp.setPlaceholder("message " + kindGlyph(c.Kind) + " " + c.Title() + " — enter sends · ctrl+j newline")
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
	content, blocks := m.renderer.Render(m.store.Messages(m.focusedID), m.feedSel, m.firstUnreadID())
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
	case m.search.active:
		pane = m.search.render(feedWidth, m.height-1)
	case m.notify.active:
		pane = m.notify.render(feedWidth, m.height-1)
	case m.view == viewTasks:
		pane = m.tasks.render(m.renderer, feedWidth, m.height-1)
	case m.view == viewDocs:
		pane = m.docs.render(feedWidth, m.height-1)
	case m.view == viewFiles:
		pane = m.files.render(feedWidth, m.height-1)
	case m.view == viewDB:
		pane = m.db.render(feedWidth, m.height-1)
	case m.threadPicker.active:
		pane = m.renderThreadPicker(feedWidth)
	case m.embedPicker.active:
		pane = m.renderEmbedPicker(feedWidth)
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
	title := styleFeedTitle.Render(kindGlyph(c.Kind) + " " + c.Title())
	if m.threadReturn != 0 {
		if parent, ok := m.store.Channel(m.threadReturn); ok {
			title = styleFeedTopic.Render(kindGlyph(parent.Kind)+" "+parent.Title()+" ▸ ") + styleFeedTitle.Render("↳ "+c.Name)
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
	if m.confirmDelete != nil {
		left += styleStatusRetry.Render(" delete message? y/n ")
	}
	if m.files.confirmRm != nil {
		left += styleStatusRetry.Render(" delete upload? y/n ")
	}
	if m.toast != "" {
		left += styleStatusOK.Render(" · " + truncate(m.toast, 40))
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
