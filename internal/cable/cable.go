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
	"time"

	"github.com/coder/websocket"

	"github.com/jkthorne/saltare/cli/internal/api"
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

// Run connects and pumps events until ctx is cancelled. It never returns
// early: connection failures surface as EventDisconnected and it retries.
// After every EventConnected (including reconnects) the consumer should
// gap-fill via REST — broadcasts during the outage are lost.
func Run(ctx context.Context, serverURL, accessToken string, channelIDs []int64, events chan<- Event) {
	backoff := time.Second
	for {
		err := connectOnce(ctx, serverURL, accessToken, channelIDs, events, &backoff)
		if ctx.Err() != nil {
			return
		}
		emit(ctx, events, Event{Type: EventDisconnected, Err: err})
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

func connectOnce(ctx context.Context, serverURL, accessToken string, channelIDs []int64, events chan<- Event, backoff *time.Duration) error {
	wsURL, err := websocketURL(serverURL, accessToken)
	if err != nil {
		return err
	}

	// Action Cable's request-forgery protection requires a same-origin Origin
	// header; native sockets don't send one by default, so act like a browser.
	origin, err := httpOrigin(serverURL)
	if err != nil {
		return err
	}
	opts := &websocket.DialOptions{HTTPHeader: http.Header{"Origin": []string{origin}}}

	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	conn, _, err := websocket.Dial(dialCtx, wsURL, opts)
	cancel()
	if err != nil {
		return err
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	// A feed of chat history can exceed the 32KiB default read limit.
	conn.SetReadLimit(1 << 20)

	for {
		readCtx, cancel := context.WithTimeout(ctx, readTimeout)
		_, data, err := conn.Read(readCtx)
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
			for _, id := range channelIDs {
				if err := subscribe(ctx, conn, id); err != nil {
					return err
				}
			}
			*backoff = time.Second // healthy connection resets the retry clock
			emit(ctx, events, Event{Type: EventConnected})
		case "ping", "confirm_subscription", "reject_subscription":
			// pings feed the read deadline; rejections mean membership was
			// revoked mid-session — the REST layer will surface that.
		case "disconnect":
			return fmt.Errorf("server sent disconnect")
		default:
			if ev, ok := parseBroadcast(frame.Identifier, frame.Message); ok {
				emit(ctx, events, ev)
			}
		}
	}
}

func subscribe(ctx context.Context, conn *websocket.Conn, channelID int64) error {
	identifier, _ := json.Marshal(map[string]any{"channel": "MessagesChannel", "channel_id": channelID})
	cmd, _ := json.Marshal(map[string]string{"command": "subscribe", "identifier": string(identifier)})
	writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return conn.Write(writeCtx, websocket.MessageText, cmd)
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
