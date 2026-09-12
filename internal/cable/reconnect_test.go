package cable

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func staticToken(tok string) func() string { return func() string { return tok } }

// collect drains events until it has n of them or ctx expires.
func collect(ctx context.Context, c *Client, n int) []Event {
	var out []Event
	for len(out) < n {
		select {
		case <-ctx.Done():
			return out
		case ev := <-c.Events():
			out = append(out, ev)
		}
	}
	return out
}

// Action Cable's request-forgery protection rejects a socket without a
// same-origin Origin header, and native sockets do not send one — so sal has
// to act like a browser or every connection dies at the handshake.
func TestDialSendsASameOriginOriginHeader(t *testing.T) {
	f := newFakeCable(t)
	f.handleWS = welcomeThenClose(f, 30*time.Millisecond)

	c := NewClient(f.URL, staticToken("sk_sal_test"), nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go drain(ctx, c)

	backoff := time.Second
	_ = c.connectOnce(ctx, &backoff)

	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.origins) == 0 {
		t.Fatal("no dial recorded")
	}
	if f.origins[0] != f.URL {
		t.Fatalf("Origin = %q, want the server's own origin %q", f.origins[0], f.URL)
	}
}

// Welcome is the only point at which subscriptions are valid, so every held
// channel must be (re)subscribed there — including on a reconnect, or a
// recovered socket delivers nothing for channels opened before the drop.
func TestWelcomeResubscribesEveryHeldChannel(t *testing.T) {
	f := newFakeCable(t)
	f.handleWS = welcomeThenClose(f, 80*time.Millisecond)

	c := NewClient(f.URL, staticToken("sk_sal_test"), []int64{7, 9})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go drain(ctx, c)

	backoff := time.Second
	_ = c.connectOnce(ctx, &backoff)
	first := append([]int64(nil), f.subscribed()...)
	sort.Slice(first, func(i, j int) bool { return first[i] < first[j] })
	if len(first) != 2 || first[0] != 7 || first[1] != 9 {
		t.Fatalf("first connect subscribed %v, want [7 9]", first)
	}

	// The socket died; the next one must re-establish the whole set.
	_ = c.connectOnce(ctx, &backoff)
	all := f.subscribed()
	if len(all) != 4 {
		t.Fatalf("reconnect subscribed %v, want all four (two per connection)", all)
	}
}

// Subscribe mid-session (opening a thread) must reach a live socket without
// waiting for a reconnect.
func TestSubscribeReachesALiveConnection(t *testing.T) {
	f := newFakeCable(t)
	f.handleWS = welcomeThenClose(f, 300*time.Millisecond)

	c := NewClient(f.URL, staticToken("sk_sal_test"), nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go drain(ctx, c)

	done := make(chan struct{})
	backoff := time.Second
	go func() { defer close(done); _ = c.connectOnce(ctx, &backoff) }()

	// Give the socket a moment to come up, then subscribe on it.
	time.Sleep(60 * time.Millisecond)
	c.Subscribe(42)

	<-done
	got := f.subscribed()
	if len(got) != 1 || got[0] != 42 {
		t.Fatalf("subscribed %v, want [42] delivered on the live socket", got)
	}
}

// A healthy connection has to clear the retry clock, or a session that
// reconnects occasionally over hours ends up permanently at the 30s ceiling.
func TestWelcomeResetsTheBackoff(t *testing.T) {
	f := newFakeCable(t)
	f.handleWS = welcomeThenClose(f, 30*time.Millisecond)

	c := NewClient(f.URL, staticToken("sk_sal_test"), nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go drain(ctx, c)

	backoff := maxBackoff
	_ = c.connectOnce(ctx, &backoff)
	if backoff != time.Second {
		t.Fatalf("backoff = %v after a healthy connection, want 1s", backoff)
	}
}

// A failed dial must not reset the clock — that is what makes the backoff
// exponential rather than a hot loop.
func TestAFailedDialLeavesTheBackoffAlone(t *testing.T) {
	c := NewClient("http://127.0.0.1:1", staticToken("sk_sal_test"), nil)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	backoff := 4 * time.Second
	if err := c.connectOnce(ctx, &backoff); err == nil {
		t.Fatal("want a dial error against a closed port")
	}
	if backoff != 4*time.Second {
		t.Fatalf("backoff = %v, want it untouched at 4s", backoff)
	}
}

func TestConnectOnceRejectsAnUnusableServerURL(t *testing.T) {
	ctx := context.Background()
	backoff := time.Second
	if err := NewClient("ftp://nope", staticToken("t"), nil).connectOnce(ctx, &backoff); err == nil {
		t.Fatal("want an error for an unsupported scheme")
	}
}

// Broadcasts arriving on a live socket surface as events, in order, after the
// connected event.
func TestBroadcastsSurfaceAsEventsAfterConnect(t *testing.T) {
	f := newFakeCable(t)
	f.handleWS = func(ctx context.Context, ws *websocket.Conn) {
		_ = ws.Write(ctx, websocket.MessageText, []byte(`{"type":"welcome"}`))
		_ = ws.Write(ctx, websocket.MessageText, []byte(`{"type":"ping","message":1}`))
		_ = ws.Write(ctx, websocket.MessageText, []byte(
			`{"identifier":"{\"channel\":\"MessagesChannel\",\"channel_id\":7}",`+
				`"message":{"event":"message_created","data":{"id":42,"channel_id":7,"body":"hi",`+
				`"sender":{"type":"User","id":1,"name":"Jack"}}}}`))
		time.Sleep(60 * time.Millisecond)
	}

	c := NewClient(f.URL, staticToken("sk_sal_test"), []int64{7})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	backoff := time.Second
	go func() { _ = c.connectOnce(ctx, &backoff) }()

	events := collect(ctx, c, 2)
	if len(events) != 2 {
		t.Fatalf("want connected + message, got %v", events)
	}
	if events[0].Type != EventConnected {
		t.Fatalf("first event = %v, want connected", events[0].Type)
	}
	// The ping must not produce an event of its own; it only feeds the watchdog.
	if events[1].Type != EventMessageCreated || events[1].ChannelID != 7 {
		t.Fatalf("second event = %+v, want the message_created broadcast", events[1])
	}
	if events[1].Message == nil || events[1].Message.ID != 42 {
		t.Fatalf("message did not decode: %+v", events[1].Message)
	}
}

// A server-sent disconnect means "stop using this socket" — it has to end the
// connection so Run reconnects, not be read as a normal frame.
func TestServerDisconnectEndsTheConnection(t *testing.T) {
	f := newFakeCable(t)
	f.handleWS = func(ctx context.Context, ws *websocket.Conn) {
		_ = ws.Write(ctx, websocket.MessageText, []byte(`{"type":"welcome"}`))
		_ = ws.Write(ctx, websocket.MessageText, []byte(`{"type":"disconnect","reason":"server_restart"}`))
		time.Sleep(200 * time.Millisecond)
	}

	c := NewClient(f.URL, staticToken("sk_sal_test"), nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go drain(ctx, c)

	backoff := time.Second
	err := c.connectOnce(ctx, &backoff)
	if err == nil {
		t.Fatal("a disconnect frame must end connectOnce with an error")
	}
}

// Run is the supervisor: it never returns on a dead socket, it reports the
// drop, and it comes back. Consumers gap-fill on each connected event, so
// both edges have to be visible.
func TestRunReportsDropsAndReconnects(t *testing.T) {
	f := newFakeCable(t)
	f.handleWS = welcomeThenClose(f, 20*time.Millisecond)

	c := NewClient(f.URL, staticToken("sk_sal_test"), []int64{7})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	go c.Run(ctx)

	// connected, disconnected, then connected again after the backoff.
	events := collect(ctx, c, 3)
	if len(events) != 3 {
		t.Fatalf("want connected/disconnected/connected, got %v", events)
	}
	if events[0].Type != EventConnected {
		t.Fatalf("event 0 = %v, want connected", events[0].Type)
	}
	if events[1].Type != EventDisconnected {
		t.Fatalf("event 1 = %v, want disconnected", events[1].Type)
	}
	if events[1].Err == nil {
		t.Error("a disconnect event should carry why")
	}
	if events[2].Type != EventConnected {
		t.Fatalf("event 2 = %v, want a reconnect", events[2].Type)
	}
}

func TestRunStopsOnContextCancel(t *testing.T) {
	f := newFakeCable(t)
	f.handleWS = welcomeThenClose(f, 20*time.Millisecond)

	c := NewClient(f.URL, staticToken("sk_sal_test"), nil)
	ctx, cancel := context.WithCancel(context.Background())
	go drain(context.Background(), c)

	done := make(chan struct{})
	go func() { defer close(done); c.Run(ctx) }()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after its context was cancelled")
	}
}
