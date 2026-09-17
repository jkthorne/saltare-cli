package watch

import (
	"testing"
	"time"

	"github.com/jkthorne/saltare-cli/internal/api"
)

func at(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func ptr[T any](v T) *T { return &v }

// Kinds are the server's enum (app/models/channel.rb). A fixture that invents
// one is a fixture that proves nothing — "channel" is not a kind, and using it
// here is how an agent DM reached a live bar rendered as "#DevOps Monitor".
func channel(id int64, slug string, unread int) api.Channel {
	return api.Channel{ID: id, Slug: slug, DisplayName: slug, Kind: "public_channel", UnreadCount: &unread}
}

func mention(id, channelID int64, slug, actor string) api.Notification {
	n := api.Notification{ID: id, Action: "mentioned", CreatedAt: at("2026-09-16T18:41:02Z")}
	n.Actor = &struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}{ID: 2, Name: actor}
	n.Message = &struct {
		ChannelID   int64  `json:"channel_id"`
		ChannelSlug string `json:"channel_slug"`
		Preview     string `json:"preview"`
	}{ChannelID: channelID, ChannelSlug: slug, Preview: "can you look at the deploy?"}
	return n
}

func baseInputs() Inputs {
	return Inputs{
		Now:       at("2026-09-16T18:42:11Z"),
		Server:    "https://saltare.ai",
		Workspace: Workspace{Slug: "acme", Name: "Acme"},
		User:      User{ID: 7, Name: "Jack Thorne"},
		Session:   SessionOK,
		Live:      true,
	}
}

func TestReduceSumsUnreadAndMarksMentionedChannels(t *testing.T) {
	in := baseInputs()
	in.Channels = []api.Channel{channel(1, "general", 3), channel(2, "random", 9), channel(3, "quiet", 0)}
	in.Notifications = []api.Notification{mention(991, 1, "general", "Alice Chen")}

	got := Reduce(in)

	if got.Totals.Unread != 12 {
		t.Errorf("total unread: want 12, got %d", got.Totals.Unread)
	}
	if got.Totals.Mentions != 1 {
		t.Errorf("mentions: want 1, got %d", got.Totals.Mentions)
	}
	if len(got.Channels) != 2 {
		t.Fatalf("want the two unread channels, got %d", len(got.Channels))
	}
	// Busiest first, and only the mentioned one is flagged.
	if got.Channels[0].Slug != "random" || got.Channels[0].Mentioned {
		t.Errorf("first channel: want random unflagged, got %+v", got.Channels[0])
	}
	if got.Channels[1].Slug != "general" || !got.Channels[1].Mentioned {
		t.Errorf("second channel: want general flagged, got %+v", got.Channels[1])
	}
}

func TestReduceLetsLiveCountsOverrideTheServerListing(t *testing.T) {
	in := baseInputs()
	in.Channels = []api.Channel{channel(1, "general", 3)}
	in.Unread = map[int64]int{1: 5} // two more arrived on the socket since the fetch

	if got := Reduce(in); got.Totals.Unread != 5 {
		t.Errorf("want the store's 5, got %d", got.Totals.Unread)
	}
}

func TestReduceBucketsWorkByDueDateAndDropsFinishedTasks(t *testing.T) {
	in := baseInputs()
	in.Tasks = []api.Task{
		{Slug: "fix-deploy", Title: "Fix the deploy", State: "open", DueDate: ptr("2026-09-14")},
		{Slug: "older", Title: "Older", State: "in_progress", DueDate: ptr("2026-09-02")},
		{Slug: "today", Title: "Today's thing", State: "open", DueDate: ptr("2026-09-16")},
		{Slug: "later", Title: "Later", State: "open", DueDate: ptr("2026-09-30")},
		{Slug: "done", Title: "Done", State: "completed", DueDate: ptr("2026-09-01")},
		{Slug: "undated", Title: "Undated", State: "open"},
	}

	got := Reduce(in)

	if got.Totals.Overdue != 2 || got.Totals.DueToday != 1 {
		t.Errorf("want 2 overdue / 1 today, got %d / %d", got.Totals.Overdue, got.Totals.DueToday)
	}
	// Oldest debt first — that is the one that has been ignored longest.
	if got.Work.Overdue[0].Slug != "older" {
		t.Errorf("want the oldest overdue first, got %q", got.Work.Overdue[0].Slug)
	}
}

func TestReduceCapsListsButNotTotals(t *testing.T) {
	in := baseInputs()
	for i := 0; i < MaxChannels+7; i++ {
		in.Channels = append(in.Channels, channel(int64(i+1), string(rune('a'+i))+"-chan", 2))
	}
	for i := 0; i < MaxNotifications+5; i++ {
		in.Notifications = append(in.Notifications, mention(int64(900+i), 1, "general", "Alice Chen"))
	}

	got := Reduce(in)

	if len(got.Channels) != MaxChannels {
		t.Errorf("channels: want the cap %d, got %d", MaxChannels, len(got.Channels))
	}
	if got.Totals.Unread != 2*(MaxChannels+7) {
		t.Errorf("unread total must ignore the cap, got %d", got.Totals.Unread)
	}
	if len(got.Notifications) != MaxNotifications {
		t.Errorf("notifications: want the cap %d, got %d", MaxNotifications, len(got.Notifications))
	}
	if got.Totals.Notifications != MaxNotifications+5 {
		t.Errorf("notification total must ignore the cap, got %d", got.Totals.Notifications)
	}
}

func TestReduceIsDeterministic(t *testing.T) {
	in := baseInputs()
	in.Channels = []api.Channel{channel(1, "zeta", 4), channel(2, "alpha", 4), channel(3, "mid", 4)}

	first, second := Reduce(in), Reduce(in)
	for i := range first.Channels {
		if first.Channels[i].Slug != second.Channels[i].Slug {
			t.Fatalf("equal-unread channels must not swap places between reduces")
		}
	}
	if first.Channels[0].Slug != "alpha" {
		t.Errorf("ties break on slug: want alpha first, got %q", first.Channels[0].Slug)
	}
}

func TestCountableIgnoresYourOwnMessagesAndSystemEvents(t *testing.T) {
	const me = 7
	own := &api.Message{ID: 1, Sender: api.Sender{Type: "User", ID: me}}
	theirs := &api.Message{ID: 2, Sender: api.Sender{Type: "User", ID: 9}}
	agent := &api.Message{ID: 3, Sender: api.Sender{Type: "Agent", ID: me}}
	system := &api.Message{ID: 4, SystemEvent: ptr("joined"), Sender: api.Sender{Type: "User", ID: 9}}

	for _, tc := range []struct {
		name string
		m    *api.Message
		want bool
	}{
		{"your own message", own, false},
		{"someone else's", theirs, true},
		{"an agent that shares your id", agent, true},
		{"a system event", system, false},
		{"nothing at all", nil, false},
	} {
		if got := Countable(tc.m, me); got != tc.want {
			t.Errorf("%s: want %v, got %v", tc.name, tc.want, got)
		}
	}
}

func TestDescribeMatchesTheServersWording(t *testing.T) {
	cases := map[string]string{
		"mentioned":         "mentioned you",
		"assigned_task":     "assigned you a task",
		"new_message":       "sent a message",
		"replied_in_thread": "replied in a thread",
		"credit_alert":      "credit alert", // no server phrasing without metadata; humanize
	}
	for action, want := range cases {
		if got := Describe(api.Notification{Action: action}); got != want {
			t.Errorf("%s: want %q, got %q", action, want, got)
		}
	}
}

func TestPersonalActionsAreTheDefaultToastSet(t *testing.T) {
	for _, action := range []string{"mentioned", "replied_in_thread", "new_message", "assigned_task"} {
		if !IsPersonal(action) {
			t.Errorf("%s should raise a toast by default", action)
		}
	}
	for _, action := range []string{"credit_alert", "connector_deactivated", "upgrade_suggestion", "task_state_changed"} {
		if IsPersonal(action) {
			t.Errorf("%s belongs in the workspace, not on top of what you were doing", action)
		}
	}
}
