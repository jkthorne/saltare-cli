// Package store holds the TUI's in-memory workspace state. It is not
// goroutine-safe by design: cable events enter the Bubble Tea update loop as
// messages, so every mutation happens on that single goroutine.
package store

import (
	"sort"

	"github.com/jkthorne/saltare/cli/internal/api"
	"github.com/jkthorne/saltare/cli/internal/cable"
)

type Store struct {
	channels []api.Channel
	byID     map[int64]int            // channel id → index into channels
	messages map[int64][]api.Message  // channel id → ascending by (created_at, id)
	seen     map[int64]map[int64]bool // channel id → message ids present
	unread   map[int64]int            // channel id → local unread count
}

func New() *Store {
	return &Store{
		byID:     map[int64]int{},
		messages: map[int64][]api.Message{},
		seen:     map[int64]map[int64]bool{},
		unread:   map[int64]int{},
	}
}

// contextualKinds never appear in the default channel listing — they enter the
// store only by being opened (a thread, a task discussion).
var contextualKinds = map[string]bool{"thread": true, "discussion": true}

// SetChannels replaces the channel list, seeding unread counts from the
// server's per-membership numbers. Contextual channels already held are kept:
// a refresh mid-session must not forget the thread the user is reading or the
// ones behind it.
func (s *Store) SetChannels(channels []api.Channel) {
	incoming := make(map[int64]bool, len(channels))
	for _, c := range channels {
		incoming[c.ID] = true
	}

	kept := make([]api.Channel, 0, len(channels))
	kept = append(kept, channels...)
	for _, c := range s.channels {
		if contextualKinds[c.Kind] && !incoming[c.ID] {
			kept = append(kept, c)
		}
	}

	s.channels = kept
	s.byID = map[int64]int{}
	for i, c := range kept {
		s.byID[c.ID] = i
		if incoming[c.ID] {
			s.unread[c.ID] = c.Unread()
		}
	}
}

func (s *Store) Channels() []api.Channel { return s.channels }

// Upsert adds or replaces one channel without touching the rest of the list —
// used for channels discovered after the initial load (an opened thread, a
// freshly bootstrapped agent DM).
func (s *Store) Upsert(c api.Channel) {
	if i, ok := s.byID[c.ID]; ok {
		s.channels[i] = c
		return
	}
	s.channels = append(s.channels, c)
	s.byID[c.ID] = len(s.channels) - 1
	if _, tracked := s.unread[c.ID]; !tracked {
		s.unread[c.ID] = c.Unread()
	}
}

func (s *Store) Channel(id int64) (api.Channel, bool) {
	i, ok := s.byID[id]
	if !ok {
		return api.Channel{}, false
	}
	return s.channels[i], true
}

func (s *Store) Unread(channelID int64) int { return s.unread[channelID] }

func (s *Store) ClearUnread(channelID int64) { s.unread[channelID] = 0 }

func (s *Store) TotalUnread() int {
	total := 0
	for _, n := range s.unread {
		total += n
	}
	return total
}

// MergeHistory folds a REST page (any order) into the channel's timeline,
// deduplicating against live events that may have raced ahead of it.
func (s *Store) MergeHistory(channelID int64, page []api.Message) {
	for _, m := range page {
		s.insert(channelID, m)
	}
}

// Apply folds one cable event in. Returns the affected channel id and whether
// anything actually changed (a duplicate created event returns false).
func (s *Store) Apply(ev cable.Event) (int64, bool) {
	switch ev.Type {
	case cable.EventMessageCreated:
		if ev.Message == nil {
			return 0, false
		}
		if !s.insert(ev.ChannelID, *ev.Message) {
			return ev.ChannelID, false
		}
		s.unread[ev.ChannelID]++
		return ev.ChannelID, true
	case cable.EventMessageUpdated:
		if ev.Message == nil {
			return 0, false
		}
		msgs := s.messages[ev.ChannelID]
		for i := range msgs {
			if msgs[i].ID == ev.Message.ID {
				msgs[i] = *ev.Message
				return ev.ChannelID, true
			}
		}
		return ev.ChannelID, false // not in the loaded window — nothing to update
	case cable.EventMessageDeleted:
		msgs := s.messages[ev.ChannelID]
		for i := range msgs {
			if msgs[i].ID == ev.MessageID {
				s.messages[ev.ChannelID] = append(msgs[:i], msgs[i+1:]...)
				delete(s.seen[ev.ChannelID], ev.MessageID)
				return ev.ChannelID, true
			}
		}
		return ev.ChannelID, false
	}
	return 0, false
}

func (s *Store) Messages(channelID int64) []api.Message { return s.messages[channelID] }

// insert adds m in timeline position; false if already present.
func (s *Store) insert(channelID int64, m api.Message) bool {
	if s.seen[channelID] == nil {
		s.seen[channelID] = map[int64]bool{}
	}
	if s.seen[channelID][m.ID] {
		return false
	}
	s.seen[channelID][m.ID] = true

	msgs := append(s.messages[channelID], m)
	sort.SliceStable(msgs, func(i, j int) bool {
		if msgs[i].CreatedAt.Equal(msgs[j].CreatedAt) {
			return msgs[i].ID < msgs[j].ID
		}
		return msgs[i].CreatedAt.Before(msgs[j].CreatedAt)
	})
	s.messages[channelID] = msgs
	return true
}
