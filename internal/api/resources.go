package api

import (
	"context"
	"fmt"
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
	payload := map[string]any{
		"channel_slug": channelSlug,
		"message":      map[string]string{"body": body},
	}
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

type ReadReceipt struct {
	Slug        string    `json:"slug"`
	LastReadAt  time.Time `json:"last_read_at"`
	UnreadCount int       `json:"unread_count"`
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
