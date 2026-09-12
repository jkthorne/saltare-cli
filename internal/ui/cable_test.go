package ui

import (
	"strings"
	"testing"

	"github.com/jkthorne/saltare-cli/internal/api"
	"github.com/jkthorne/saltare-cli/internal/cable"
)

// A revoked channel keeps its sidebar row and its history; it just stops
// receiving. Without a word from the client that reads as sal being broken,
// so the rejection has to reach the status bar.
func TestSubscriptionRejectionToastsTheChannelName(t *testing.T) {
	m := testModel(t)
	m.store.SetChannels([]api.Channel{{ID: 9, Slug: "secret", Name: "Secret", Kind: "channel"}})

	updated, _ := m.handleCable(cable.Event{Type: cable.EventSubscriptionRejected, ChannelID: 9})
	got := updated.(Model)

	if got.toast == "" {
		t.Fatal("a rejected subscription must say something")
	}
	if !strings.Contains(got.toast, "Secret") {
		t.Errorf("toast should name the channel, got %q", got.toast)
	}
	// The socket itself is healthy — this is not a disconnect.
	if got.conn == connRetrying {
		t.Error("one rejected channel must not mark the whole connection as retrying")
	}
}

func TestSubscriptionRejectionForAnUnknownChannelStillSpeaks(t *testing.T) {
	m := testModel(t)

	updated, _ := m.handleCable(cable.Event{Type: cable.EventSubscriptionRejected})
	if got := updated.(Model); got.toast == "" {
		t.Fatal("an unattributable rejection must still be reported")
	}
}
