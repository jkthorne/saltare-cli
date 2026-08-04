package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type ChannelsOpts struct {
	Kind              string // filter to one kind; empty = server default (conversational kinds)
	ParentChannelSlug string // list a channel's threads instead
	Page              int
	PerPage           int
}

func (c *Client) Channels(ctx context.Context, opts ChannelsOpts) ([]Channel, error) {
	q := url.Values{}
	if opts.Kind != "" {
		q.Set("kind", opts.Kind)
	}
	if opts.ParentChannelSlug != "" {
		q.Set("parent_channel_slug", opts.ParentChannelSlug)
	}
	if opts.Page > 0 {
		q.Set("page", strconv.Itoa(opts.Page))
	}
	perPage := opts.PerPage
	if perPage == 0 {
		perPage = 100 // server max; an SMB workspace sidebar fits in one page
	}
	q.Set("per_page", strconv.Itoa(perPage))

	var out struct {
		Data []Channel `json:"data"`
	}
	if err := c.get(ctx, "/api/v1/channels", q, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// Messages returns one page, newest first (the server's order). Page 1 is the
// latest history; callers reverse for display.
func (c *Client) Messages(ctx context.Context, channelSlug string, page, perPage int) ([]Message, error) {
	if perPage == 0 {
		perPage = 50
	}
	if page == 0 {
		page = 1
	}
	q := url.Values{
		"channel_slug": {channelSlug},
		"page":         {strconv.Itoa(page)},
		"per_page":     {strconv.Itoa(perPage)},
	}
	var out struct {
		Data []Message `json:"data"`
	}
	if err := c.get(ctx, "/api/v1/messages", q, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// SendMessage posts to a channel (or thread — thread slugs are channel slugs;
// the server auto-joins the sender to threads).
func (c *Client) SendMessage(ctx context.Context, channelSlug, body string) (*Message, error) {
	return c.sendMessage(ctx, channelSlug, map[string]any{"body": body})
}

// SendReply posts a reply to a specific message: the server finds or creates
// that message's thread and posts there (the returned message's channel_id is
// the thread channel).
func (c *Client) SendReply(ctx context.Context, channelSlug, body string, replyToMessageID int64) (*Message, error) {
	return c.sendMessage(ctx, channelSlug, map[string]any{"body": body, "reply_to_message_id": replyToMessageID})
}

func (c *Client) sendMessage(ctx context.Context, channelSlug string, message map[string]any) (*Message, error) {
	payload := map[string]any{"channel_slug": channelSlug, "message": message}
	var out struct {
		Data Message `json:"data"`
	}
	if err := c.post(ctx, "/api/v1/messages", payload, &out); err != nil {
		return nil, err
	}
	return &out.Data, nil
}

type Mentionable struct {
	Type string `json:"type"` // "user" | "agent"
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"` // agents only
}

// Mentionables returns the workspace @-mention directory (members + active
// agents), fetched once and filtered locally.
func (c *Client) Mentionables(ctx context.Context) ([]Mentionable, error) {
	var out struct {
		Data []Mentionable `json:"data"`
	}
	if err := c.get(ctx, "/api/v1/mentionables", nil, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

type Agent struct {
	ID          int64   `json:"id"`
	Slug        string  `json:"slug"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
	Status      string  `json:"status"`
	AvatarColor string  `json:"avatar_color"`
}

func (c *Client) Agents(ctx context.Context) ([]Agent, error) {
	q := url.Values{"per_page": {"100"}}
	var out struct {
		Data []Agent `json:"data"`
	}
	if err := c.get(ctx, "/api/v1/agents", q, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// MessageAgent sends the first message to an agent, bootstrapping the 1:1
// agent-DM channel server-side. Returns the message and the DM channel id.
func (c *Client) MessageAgent(ctx context.Context, agentSlug, content string) (*Message, int64, error) {
	var out struct {
		Data      Message `json:"data"`
		ChannelID int64   `json:"channel_id"`
	}
	path := fmt.Sprintf("/api/v1/agents/%s/message", url.PathEscape(agentSlug))
	if err := c.post(ctx, path, map[string]string{"content": content}, &out); err != nil {
		return nil, 0, err
	}
	return &out.Data, out.ChannelID, nil
}

// Task mirrors Api::V1::TaskSerializer. Date-only fields stay strings —
// "2026-08-05" is not RFC3339 and must not go through time.Time.
type Task struct {
	ID          int64     `json:"id"`
	Slug        string    `json:"slug"`
	Title       string    `json:"title"`
	Description *string   `json:"description"`
	State       string    `json:"state"`
	Priority    *string   `json:"priority"`
	StartDate   *string   `json:"start_date"`
	DueDate     *string   `json:"due_date"`
	DueTime     *string   `json:"due_time"`
	ProjectID   int64     `json:"project_id"`
	Assignee    *Sender   `json:"assignee"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type TasksOpts struct {
	State     string
	Mine      bool
	ProjectID int64
}

func (c *Client) Tasks(ctx context.Context, opts TasksOpts) ([]Task, error) {
	q := url.Values{"per_page": {"100"}}
	if opts.State != "" {
		q.Set("state", opts.State)
	}
	if opts.Mine {
		q.Set("mine", "true")
	}
	if opts.ProjectID > 0 {
		q.Set("project_id", strconv.FormatInt(opts.ProjectID, 10))
	}
	var out struct {
		Data []Task `json:"data"`
	}
	if err := c.get(ctx, "/api/v1/tasks", q, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// CreateTask makes a task; assigneeUserID > 0 self-assigns it (a task created
// from a personal CLI belongs on the creator's "mine" list).
func (c *Client) CreateTask(ctx context.Context, projectID int64, title string, assigneeUserID int64) (*Task, error) {
	task := map[string]any{"project_id": projectID, "title": title}
	if assigneeUserID > 0 {
		task["assignee"] = map[string]any{"type": "User", "id": assigneeUserID}
	}
	payload := map[string]any{"task": task}
	var out struct {
		Data Task `json:"data"`
	}
	if err := c.post(ctx, "/api/v1/tasks", payload, &out); err != nil {
		return nil, err
	}
	return &out.Data, nil
}

func (c *Client) UpdateTaskState(ctx context.Context, slug, state string) (*Task, error) {
	payload := map[string]any{"task": map[string]string{"state": state}}
	var out struct {
		Data Task `json:"data"`
	}
	path := fmt.Sprintf("/api/v1/tasks/%s", url.PathEscape(slug))
	if err := c.do(ctx, http.MethodPatch, path, nil, payload, &out); err != nil {
		return nil, err
	}
	return &out.Data, nil
}

type Project struct {
	ID               int64  `json:"id"`
	Slug             string `json:"slug"`
	Name             string `json:"name"`
	ActiveTasksCount int    `json:"active_tasks_count"`
	Archived         bool   `json:"archived"`
}

func (c *Client) Projects(ctx context.Context) ([]Project, error) {
	q := url.Values{"per_page": {"100"}}
	var out struct {
		Data []Project `json:"data"`
	}
	if err := c.get(ctx, "/api/v1/projects", q, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

type Notification struct {
	ID        int64      `json:"id"`
	Action    string     `json:"action"`
	ReadAt    *time.Time `json:"read_at"`
	CreatedAt time.Time  `json:"created_at"`
	Actor     *struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	} `json:"actor"`
	Message *struct {
		ChannelID   int64  `json:"channel_id"`
		ChannelSlug string `json:"channel_slug"`
		Preview     string `json:"preview"`
	} `json:"message"`
	Task *struct {
		Slug  string `json:"slug"`
		Title string `json:"title"`
	} `json:"task"`
}

func (c *Client) Notifications(ctx context.Context, unreadOnly bool) ([]Notification, error) {
	q := url.Values{"per_page": {"50"}}
	if unreadOnly {
		q.Set("unread", "true")
	}
	var out struct {
		Data []Notification `json:"data"`
	}
	if err := c.get(ctx, "/api/v1/notifications", q, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

func (c *Client) ReadAllNotifications(ctx context.Context) (int, error) {
	var out struct {
		Data struct {
			MarkedRead int `json:"marked_read"`
		} `json:"data"`
	}
	if err := c.post(ctx, "/api/v1/notifications/read_all", nil, &out); err != nil {
		return 0, err
	}
	return out.Data.MarkedRead, nil
}

type ReadReceipt struct {
	Slug        string    `json:"slug"`
	LastReadAt  time.Time `json:"last_read_at"`
	UnreadCount int       `json:"unread_count"`
}

// EditMessage rewrites a message body (author or workspace admin; the
// server stamps edited_at).
func (c *Client) EditMessage(ctx context.Context, id int64, body string) (*Message, error) {
	payload := map[string]any{"message": map[string]string{"body": body}}
	var out struct {
		Data Message `json:"data"`
	}
	path := fmt.Sprintf("/api/v1/messages/%d", id)
	if err := c.do(ctx, http.MethodPatch, path, nil, payload, &out); err != nil {
		return nil, err
	}
	return &out.Data, nil
}

// DeleteMessage permanently removes a message (author or workspace admin).
func (c *Client) DeleteMessage(ctx context.Context, id int64) error {
	path := fmt.Sprintf("/api/v1/messages/%d", id)
	return c.do(ctx, http.MethodDelete, path, nil, nil, nil)
}

type SearchOpts struct {
	Type    string // "", "messages", "tasks", or "documents"; empty = all
	Channel string // channel slug to scope message results
	Limit   int    // per-type cap (server max 50)
}

type SearchChannelRef struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// SearchMessage is a message hit plus the channel context needed to jump.
type SearchMessage struct {
	Message
	Channel SearchChannelRef `json:"channel"`
}

type SearchResults struct {
	Messages  []SearchMessage `json:"messages"`
	Tasks     []Task          `json:"tasks"`
	Documents []Document      `json:"documents"`
}

// Search runs the workspace full-text search. Result types the key lacks a
// read scope for come back empty rather than erroring.
func (c *Client) Search(ctx context.Context, q string, opts SearchOpts) (*SearchResults, error) {
	query := url.Values{"q": {q}}
	if opts.Type != "" {
		query.Set("type", opts.Type)
	}
	if opts.Channel != "" {
		query.Set("channel", opts.Channel)
	}
	if opts.Limit > 0 {
		query.Set("limit", strconv.Itoa(opts.Limit))
	}
	var out struct {
		Data SearchResults `json:"data"`
	}
	if err := c.get(ctx, "/api/v1/search", query, &out); err != nil {
		return nil, err
	}
	return &out.Data, nil
}

// Document mirrors Api::V1::DocumentSerializer. The index omits body; show
// includes it.
type Document struct {
	ID        int64     `json:"id"`
	Slug      string    `json:"slug"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	Published bool      `json:"published"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (c *Client) Documents(ctx context.Context) ([]Document, error) {
	q := url.Values{"per_page": {"100"}}
	var out struct {
		Data []Document `json:"data"`
	}
	if err := c.get(ctx, "/api/v1/documents", q, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

func (c *Client) Document(ctx context.Context, slug string) (*Document, error) {
	var out struct {
		Data Document `json:"data"`
	}
	path := fmt.Sprintf("/api/v1/documents/%s", url.PathEscape(slug))
	if err := c.get(ctx, path, nil, &out); err != nil {
		return nil, err
	}
	return &out.Data, nil
}

// MarkRead advances the caller's read cursor; messageID 0 means "now".
func (c *Client) MarkRead(ctx context.Context, channelSlug string, messageID int64) (*ReadReceipt, error) {
	var body any
	if messageID > 0 {
		body = map[string]int64{"message_id": messageID}
	}
	var out struct {
		Data ReadReceipt `json:"data"`
	}
	path := fmt.Sprintf("/api/v1/channels/%s/read", url.PathEscape(channelSlug))
	if err := c.post(ctx, path, body, &out); err != nil {
		return nil, err
	}
	return &out.Data, nil
}
