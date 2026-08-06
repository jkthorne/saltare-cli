package store

import (
	"testing"
	"time"

	"github.com/jkthorne/saltare/cli/internal/api"
	"github.com/jkthorne/saltare/cli/internal/cable"
)

func msg(id int64, channelID int64, at time.Time) api.Message {
	return api.Message{ID: id, ChannelID: channelID, Body: "m", CreatedAt: at, UpdatedAt: at}
}

func TestMergeHistoryOrdersAndDedups(t *testing.T) {
	s := New()
	base := time.Now()

	// REST page arrives newest-first; a live event already inserted id 3.
	s.Apply(cable.Event{Type: cable.EventMessageCreated, ChannelID: 1, Message: ptr(msg(3, 1, base.Add(2*time.Second)))})
	s.MergeHistory(1, []api.Message{
		msg(3, 1, base.Add(2*time.Second)),
		msg(2, 1, base.Add(time.Second)),
		msg(1, 1, base),
	})

	got := s.Messages(1)
	if len(got) != 3 {
		t.Fatalf("want 3 messages, got %d", len(got))
	}
	for i, want := range []int64{1, 2, 3} {
		if got[i].ID != want {
			t.Errorf("position %d: want id %d, got %d", i, want, got[i].ID)
		}
	}
}

func TestApplyCreatedIncrementsUnreadOnceAndDedups(t *testing.T) {
	s := New()
	m := msg(7, 4, time.Now())

	if _, changed := s.Apply(cable.Event{Type: cable.EventMessageCreated, ChannelID: 4, Message: &m}); !changed {
		t.Fatal("first apply should change state")
	}
	if _, changed := s.Apply(cable.Event{Type: cable.EventMessageCreated, ChannelID: 4, Message: &m}); changed {
		t.Fatal("duplicate apply should be a no-op")
	}
	if s.Unread(4) != 1 {
		t.Fatalf("want unread 1, got %d", s.Unread(4))
	}
}

func TestApplyUpdatedReplacesBody(t *testing.T) {
	s := New()
	at := time.Now()
	s.MergeHistory(1, []api.Message{msg(5, 1, at)})

	updated := msg(5, 1, at)
	updated.Body = "edited"
	if _, changed := s.Apply(cable.Event{Type: cable.EventMessageUpdated, ChannelID: 1, Message: &updated}); !changed {
		t.Fatal("update of loaded message should change state")
	}
	if s.Messages(1)[0].Body != "edited" {
		t.Fatalf("body not replaced: %q", s.Messages(1)[0].Body)
	}
}

func TestApplyDestroyedRemoves(t *testing.T) {
	s := New()
	at := time.Now()
	s.MergeHistory(1, []api.Message{msg(5, 1, at), msg(6, 1, at.Add(time.Second))})

	if _, changed := s.Apply(cable.Event{Type: cable.EventMessageDeleted, ChannelID: 1, MessageID: 5}); !changed {
		t.Fatal("destroy of loaded message should change state")
	}
	got := s.Messages(1)
	if len(got) != 1 || got[0].ID != 6 {
		t.Fatalf("want only id 6 left, got %+v", got)
	}
	// A re-created id 5 (impossible in practice, but seen must not block it).
	s.MergeHistory(1, []api.Message{msg(5, 1, at)})
	if len(s.Messages(1)) != 2 {
		t.Fatal("seen-set should forget destroyed ids")
	}
}

func TestSetChannelsSeedsUnread(t *testing.T) {
	s := New()
	three := 3
	s.SetChannels([]api.Channel{{ID: 9, UnreadCount: &three, Member: true}})
	if s.Unread(9) != 3 {
		t.Fatalf("want seeded unread 3, got %d", s.Unread(9))
	}
	s.ClearUnread(9)
	if s.Unread(9) != 0 {
		t.Fatal("clear failed")
	}
}

func TestSetChannelsKeepsOpenedThreads(t *testing.T) {
	s := New()
	s.SetChannels([]api.Channel{
		{ID: 1, Slug: "general", Kind: "public_channel", Member: true},
		{ID: 2, Slug: "old-dm", Kind: "dm", Member: true},
	})
	s.Upsert(api.Channel{ID: 7, Slug: "thread-abc", Kind: "thread", Member: true})
	s.unread[7] = 2 // a live reply landed while the thread was open

	// A refresh returns only the listable channels — and drops the DM.
	s.SetChannels([]api.Channel{{ID: 1, Slug: "general", Kind: "public_channel", Member: true}})

	if _, ok := s.Channel(7); !ok {
		t.Fatal("a channel refresh must not forget an opened thread")
	}
	if s.Unread(7) != 2 {
		t.Fatalf("a preserved thread keeps its unread count; got %d", s.Unread(7))
	}
	if _, ok := s.Channel(2); ok {
		t.Fatal("a listable channel the server stopped returning must drop out")
	}
	if got := len(s.Channels()); got != 2 {
		t.Fatalf("want general + the thread, got %d channels", got)
	}
}

func ptr[T any](v T) *T { return &v }
