package cable

import (
	"encoding/json"
	"testing"
)

// identifiers arrive as JSON-encoded strings inside the frame, exactly as
// Action Cable sends them.
func ident(channelID int64) string {
	raw, _ := json.Marshal(map[string]any{"channel": "MessagesChannel", "channel_id": channelID})
	return string(raw)
}

func TestParseBroadcastCreated(t *testing.T) {
	message := json.RawMessage(`{"event":"message_created","data":{"id":42,"channel_id":7,"body":"hi","sender":{"type":"User","id":1,"name":"Jack"},"created_at":"2026-08-03T12:00:00.000Z","updated_at":"2026-08-03T12:00:00.000Z"}}`)

	ev, ok := parseBroadcast(ident(7), message)
	if !ok {
		t.Fatal("expected a parsed event")
	}
	if ev.Type != EventMessageCreated || ev.ChannelID != 7 {
		t.Fatalf("unexpected event: %+v", ev)
	}
	if ev.Message == nil || ev.Message.ID != 42 || ev.Message.Sender.Name != "Jack" {
		t.Fatalf("unexpected message: %+v", ev.Message)
	}
}

func TestParseBroadcastDestroyed(t *testing.T) {
	message := json.RawMessage(`{"event":"message_destroyed","data":{"id":42,"channel_id":7}}`)

	ev, ok := parseBroadcast(ident(7), message)
	if !ok {
		t.Fatal("expected a parsed event")
	}
	if ev.Type != EventMessageDeleted || ev.MessageID != 42 {
		t.Fatalf("unexpected event: %+v", ev)
	}
}

func TestParseBroadcastIgnoresOtherChannelsAndJunk(t *testing.T) {
	if _, ok := parseBroadcast(`{"channel":"TypingChannel","channel_id":7}`, json.RawMessage(`{"event":"message_created"}`)); ok {
		t.Fatal("non-MessagesChannel identifiers must be ignored")
	}
	if _, ok := parseBroadcast(ident(7), json.RawMessage(`{"event":"unknown_event","data":{}}`)); ok {
		t.Fatal("unknown events must be ignored")
	}
	if _, ok := parseBroadcast("", nil); ok {
		t.Fatal("empty frames must be ignored")
	}
}

func TestWebsocketURL(t *testing.T) {
	got, err := websocketURL("https://saltare.example", "sk_sal_abc")
	if err != nil {
		t.Fatal(err)
	}
	want := "wss://saltare.example/cable?access_token=sk_sal_abc"
	if got != want {
		t.Fatalf("want %q, got %q", want, got)
	}
	if _, err := websocketURL("ftp://nope", "t"); err == nil {
		t.Fatal("non-http schemes must error")
	}
}
