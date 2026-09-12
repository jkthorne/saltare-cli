package cable

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// fakeCable is an Action Cable server good enough to drive the client: it
// accepts the socket, records what each dial presented, and plays whatever
// frames the test queues.
type fakeCable struct {
	*httptest.Server

	mu       sync.Mutex
	tokens   []string // access_token query param, one per dial
	origins  []string
	subs     []int64 // channel_ids the client subscribed to
	handleWS func(ctx context.Context, ws *websocket.Conn)
}

func newFakeCable(t *testing.T, handle func(ctx context.Context, ws *websocket.Conn)) *fakeCable {
	t.Helper()
	f := &fakeCable{handleWS: handle}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.tokens = append(f.tokens, r.URL.Query().Get("access_token"))
		f.origins = append(f.origins, r.Header.Get("Origin"))
		f.mu.Unlock()

		ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer ws.Close(websocket.StatusNormalClosure, "")
		f.handleWS(r.Context(), ws)
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeCable) dialedTokens() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.tokens...)
}

func (f *fakeCable) subscribed() []int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int64(nil), f.subs...)
}

func (f *fakeCable) recordSubscribe(raw []byte) {
	var cmd struct {
		Command    string `json:"command"`
		Identifier string `json:"identifier"`
	}
	if err := json.Unmarshal(raw, &cmd); err != nil || cmd.Command != "subscribe" {
		return
	}
	var ident struct {
		ChannelID int64 `json:"channel_id"`
	}
	if err := json.Unmarshal([]byte(cmd.Identifier), &ident); err != nil {
		return
	}
	f.mu.Lock()
	f.subs = append(f.subs, ident.ChannelID)
	f.mu.Unlock()
}

// welcomeThenClose greets the client, drains its subscribes for a moment, and
// hangs up — enough for one full connect cycle.
func welcomeThenClose(f *fakeCable, wait time.Duration) func(context.Context, *websocket.Conn) {
	return func(ctx context.Context, ws *websocket.Conn) {
		_ = ws.Write(ctx, websocket.MessageText, []byte(`{"type":"welcome"}`))
		deadline, cancel := context.WithTimeout(ctx, wait)
		defer cancel()
		for {
			_, data, err := ws.Read(deadline)
			if err != nil {
				return
			}
			f.recordSubscribe(data)
		}
	}
}

// The access token rides the dial URL and only lives 30 days, so a client that
// captured it at construction reconnects with a rotated-out key forever. Each
// dial must ask for the token again.
func TestConnectOnceReReadsTheTokenEveryDial(t *testing.T) {
	f := newFakeCable(t, func(ctx context.Context, ws *websocket.Conn) {})
	f.handleWS = welcomeThenClose(f, 50*time.Millisecond)

	tokens := []string{"sk_sal_first", "sk_sal_rotated"}
	var n int
	c := NewClient(f.URL, func() string {
		tok := tokens[min(n, len(tokens)-1)]
		n++
		return tok
	}, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	backoff := time.Second
	go drain(ctx, c)
	_ = c.connectOnce(ctx, &backoff)
	_ = c.connectOnce(ctx, &backoff)

	got := f.dialedTokens()
	if len(got) != 2 {
		t.Fatalf("want 2 dials, got %d: %v", len(got), got)
	}
	if got[0] != "sk_sal_first" || got[1] != "sk_sal_rotated" {
		t.Fatalf("second dial did not pick up the rotated token: %v", got)
	}
}

func drain(ctx context.Context, c *Client) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.Events():
		}
	}
}
