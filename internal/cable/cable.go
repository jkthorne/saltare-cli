// Package cable is a minimal Action Cable client for Saltare's JSON
// MessagesChannel. Protocol: connect to /cable?access_token=..., wait for
// {"type":"welcome"}, send one subscribe command per chat channel, then read
// broadcast frames. The server pings every ~3s, so a read deadline doubles as
// a liveness watchdog; any failure tears the socket down and reconnects with
// exponential backoff.
package cable

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/jkthorne/saltare-cli/internal/api"
)

type EventType string

const (
	EventConnected      EventType = "connected" // welcome received (also fires on each reconnect)
	EventDisconnected   EventType = "disconnected"
	EventMessageCreated EventType = "message_created"
	EventMessageUpdated EventType = "message_updated"
	EventMessageDeleted EventType = "message_destroyed"
)

type Event struct {
	Type      EventType
	ChannelID int64        // chat channel the event belongs to (message events only)
	Message   *api.Message // created/updated
	MessageID int64        // destroyed
	Err       error        // disconnected
}

const (
	readTimeout = 15 * time.Second // server pings every ~3s; 15s of silence = dead socket
	maxBackoff  = 30 * time.Second
)

// Client owns one cable connection with a dynamic subscription set: Subscribe
// adds channels at runtime (e.g. opening a thread), and every reconnect
// resubscribes the full set.
type Client struct {
	server string
	token  string
	events chan Event

	mu     sync.Mutex
	ids    map[int64]bool
	notify chan int64 // ids to subscribe on the live connection
}

func NewClient(serverURL, accessToken string, channelIDs []int64) *Client {
	c := &Client{
		server: serverURL,
		token:  accessToken,
		events: make(chan Event, 64),
		ids:    map[int64]bool{},
		notify: make(chan int64, 256),
	}
	for _, id := range channelIDs {
		c.ids[id] = true
	}
	return c
}

func (c *Client) Events() <-chan Event { return c.events }

// Subscribe adds a channel to the set. Idempotent and async: on a live
// connection the subscribe frame goes out immediately; while disconnected the
// id waits for the next welcome.
func (c *Client) Subscribe(channelID int64) {
	c.mu.Lock()
	already := c.ids[channelID]
	c.ids[channelID] = true
	c.mu.Unlock()
	if already {
		return
	}
	select {
	case c.notify <- channelID:
	default: // full buffer: the reconnect resubscribe path covers it
	}
}

func (c *Client) snapshotIDs() []int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]int64, 0, len(c.ids))
	for id := range c.ids {
		out = append(out, id)
	}
	return out
}

// Run connects and pumps events until ctx is cancelled. It never returns
// early: connection failures surface as EventDisconnected and it retries.
// After every EventConnected (including reconnects) the consumer should
// gap-fill via REST — broadcasts during the outage are lost.
func (c *Client) Run(ctx context.Context) {
	backoff := time.Second
	for {
		err := c.connectOnce(ctx, &backoff)
		if ctx.Err() != nil {
			return
		}
		emit(ctx, c.events, Event{Type: EventDisconnected, Err: err})
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return
		}
		if backoff < maxBackoff {
			backoff *= 2
		}
	}
}

func (c *Client) connectOnce(ctx context.Context, backoff *time.Duration) error {
	wsURL, err := websocketURL(c.server, c.token)
	if err != nil {
		return err
	}

	// Action Cable's request-forgery protection requires a same-origin Origin
	// header; native sockets don't send one by default, so act like a browser.
	origin, err := httpOrigin(c.server)
	if err != nil {
		return err
	}
	opts := &websocket.DialOptions{HTTPHeader: http.Header{"Origin": []string{origin}}}

	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	ws, _, err := websocket.Dial(dialCtx, wsURL, opts)
	cancel()
	if err != nil {
		return err
	}
	conn := &wsConn{ws: ws}
	defer ws.Close(websocket.StatusNormalClosure, "")
	// A feed of chat history can exceed the 32KiB default read limit.
	ws.SetReadLimit(1 << 20)

	// Forward runtime Subscribe calls onto this connection; dies with it.
	connCtx, connCancel := context.WithCancel(ctx)
	defer connCancel()
	go func() {
		for {
			select {
			case <-connCtx.Done():
				return
			case id := <-c.notify:
				_ = conn.subscribe(connCtx, id)
			}
		}
	}()

	for {
		readCtx, cancel := context.WithTimeout(ctx, readTimeout)
		_, data, err := ws.Read(readCtx)
		cancel()
		if err != nil {
			return err
		}

		var frame struct {
			Type       string          `json:"type"`
			Identifier string          `json:"identifier"`
			Message    json.RawMessage `json:"message"`
		}
		if err := json.Unmarshal(data, &frame); err != nil {
			continue // tolerate unknown frames
		}

		switch frame.Type {
		case "welcome":
			for _, id := range c.snapshotIDs() {
				if err := conn.subscribe(ctx, id); err != nil {
					return err
				}
			}
			*backoff = time.Second // healthy connection resets the retry clock
			emit(ctx, c.events, Event{Type: EventConnected})
		case "ping", "confirm_subscription", "reject_subscription":
			// pings feed the read deadline; rejections mean visibility was
			// revoked mid-session — the REST layer will surface that.
		case "disconnect":
			return fmt.Errorf("server sent disconnect")
		default:
			if ev, ok := parseBroadcast(frame.Identifier, frame.Message); ok {
				emit(ctx, c.events, ev)
			}
		}
	}
}

// wsConn serializes writes — coder/websocket allows one concurrent writer,
// and both the read loop (welcome) and the notify forwarder send subscribes.
type wsConn struct {
	ws *websocket.Conn
	mu sync.Mutex
}

func (c *wsConn) subscribe(ctx context.Context, channelID int64) error {
	identifier, _ := json.Marshal(map[string]any{"channel": "MessagesChannel", "channel_id": channelID})
	cmd, _ := json.Marshal(map[string]string{"command": "subscribe", "identifier": string(identifier)})
	writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ws.Write(writeCtx, websocket.MessageText, cmd)
}

// parseBroadcast decodes a broadcast frame: identifier is a JSON-encoded
// string naming the subscription; message is {event, data}.
func parseBroadcast(identifier string, message json.RawMessage) (Event, bool) {
	if identifier == "" || len(message) == 0 {
		return Event{}, false
	}
	var ident struct {
		Channel   string `json:"channel"`
		ChannelID int64  `json:"channel_id"`
	}
	if err := json.Unmarshal([]byte(identifier), &ident); err != nil || ident.Channel != "MessagesChannel" {
		return Event{}, false
	}

	var payload struct {
		Event string          `json:"event"`
		Data  json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(message, &payload); err != nil {
		return Event{}, false
	}

	switch EventType(payload.Event) {
	case EventMessageCreated, EventMessageUpdated:
		var msg api.Message
		if err := json.Unmarshal(payload.Data, &msg); err != nil {
			return Event{}, false
		}
		return Event{Type: EventType(payload.Event), ChannelID: ident.ChannelID, Message: &msg}, true
	case EventMessageDeleted:
		var data struct {
			ID int64 `json:"id"`
		}
		if err := json.Unmarshal(payload.Data, &data); err != nil {
			return Event{}, false
		}
		return Event{Type: EventMessageDeleted, ChannelID: ident.ChannelID, MessageID: data.ID}, true
	}
	return Event{}, false
}

func websocketURL(serverURL, accessToken string) (string, error) {
	u, err := url.Parse(strings.TrimRight(serverURL, "/"))
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("invalid server url %q", serverURL)
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	default:
		return "", fmt.Errorf("unsupported scheme %q", u.Scheme)
	}
	u.Path += "/cable"
	q := u.Query()
	q.Set("access_token", accessToken)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// httpOrigin returns scheme://host of the server URL, the value a browser
// would send as the Origin header.
func httpOrigin(serverURL string) (string, error) {
	u, err := url.Parse(strings.TrimRight(serverURL, "/"))
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("invalid server url %q", serverURL)
	}
	return u.Scheme + "://" + u.Host, nil
}

func emit(ctx context.Context, events chan<- Event, ev Event) {
	select {
	case events <- ev:
	case <-ctx.Done():
	}
}
