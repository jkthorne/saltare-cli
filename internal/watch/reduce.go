package watch

import (
	"sort"
	"strings"
	"time"

	"github.com/jkthorne/saltare-cli/internal/api"
)

// Inputs is everything the daemon knows at one instant. Reduce is pure over
// it: same Inputs, same State, no clock and no network. The daemon's only job
// is to keep this struct current.
type Inputs struct {
	Now       time.Time
	Server    string
	Workspace Workspace
	User      User

	// Session and Live are the two independent failure axes (see State).
	Session string
	Live    bool
	Error   string

	// Channels is the server's listing. Unread overrides the per-channel count
	// for channels the live socket has moved since that listing was fetched —
	// the map is the store's view, the listing is the server's.
	Channels []api.Channel
	Unread   map[int64]int

	Notifications []api.Notification // unread only, newest first
	Tasks         []api.Task         // the caller's tasks; Reduce drops the finished ones

	// Mail is nil for a reader that cannot see mail at all — no mail:read, or
	// no connected account — and Reduce keeps that distinction by leaving
	// State.Mail nil rather than making it an empty list.
	Mail []api.Mailbox
}

// mentionActions are the notification kinds that make a channel worth looking
// at right now, as opposed to worth knowing about. They set Channel.Mentioned
// and feed Totals.Mentions.
var mentionActions = map[string]bool{
	"mentioned":         true,
	"replied_in_thread": true,
	"assigned_task":     true,
}

// personalActions are the kinds a desktop toast is for by default: someone
// addressed you. Everything else — credit alerts, connector trouble — belongs
// in the workspace, not on top of whatever you were doing.
var personalActions = map[string]bool{
	"mentioned":         true,
	"replied_in_thread": true,
	"new_message":       true,
	"assigned_task":     true,
}

// IsPersonal reports whether an action is toast-worthy under the default
// notify policy.
func IsPersonal(action string) bool { return personalActions[action] }

// finishedStates are tasks that are no longer work. The tasks index is fetched
// unfiltered (one request, not three) and narrowed here.
var finishedStates = map[string]bool{"completed": true, "cancelled": true}

// Describe ports Notification#description from the server. The serializer
// sends `action` and lets clients phrase it, so a toast that invents its own
// wording would disagree with the web app about what just happened.
func Describe(n api.Notification) string {
	switch n.Action {
	case "mentioned":
		return "mentioned you"
	case "assigned_task":
		return "assigned you a task"
	case "task_state_changed":
		return "updated a task"
	case "task_due_soon":
		return "task due soon"
	case "new_message":
		return "sent a message"
	case "replied_in_thread":
		return "replied in a thread"
	case "connector_deactivated":
		return "connector deactivated"
	default:
		return strings.ReplaceAll(n.Action, "_", " ")
	}
}

// Reduce builds the published document.
func Reduce(in Inputs) State {
	s := State{
		Schema:        Schema,
		UpdatedAt:     in.Now,
		HeartbeatSec:  HeartbeatSec,
		Session:       in.Session,
		Live:          in.Live,
		Server:        in.Server,
		Workspace:     in.Workspace,
		User:          in.User,
		Error:         in.Error,
		Channels:      []Channel{},
		Notifications: []Notification{},
		Work:          Work{Overdue: []Task{}, Today: []Task{}},
	}
	if s.Session == "" {
		s.Session = SessionOK
	}

	mentionedChannels := map[int64]bool{}
	for _, n := range in.Notifications {
		s.Totals.Notifications++
		if mentionActions[n.Action] {
			s.Totals.Mentions++
		}
		if n.Message != nil && mentionActions[n.Action] {
			mentionedChannels[n.Message.ChannelID] = true
		}
		if len(s.Notifications) < MaxNotifications {
			s.Notifications = append(s.Notifications, ToNotification(n))
		}
	}

	for _, c := range in.Channels {
		unread := c.Unread()
		if live, ok := in.Unread[c.ID]; ok {
			unread = live
		}
		if unread <= 0 {
			continue
		}
		s.Totals.Unread += unread
		s.Channels = append(s.Channels, Channel{
			Slug:      c.Slug,
			Title:     c.Title(),
			Kind:      c.Kind,
			Unread:    unread,
			Mentioned: mentionedChannels[c.ID],
		})
	}
	// Busiest first, then by slug so two channels with equal unread do not
	// swap places between writes and repaint the popup for no reason.
	sort.SliceStable(s.Channels, func(i, j int) bool {
		if s.Channels[i].Unread != s.Channels[j].Unread {
			return s.Channels[i].Unread > s.Channels[j].Unread
		}
		return s.Channels[i].Slug < s.Channels[j].Slug
	})
	if len(s.Channels) > MaxChannels {
		s.Channels = s.Channels[:MaxChannels]
	}

	// ISO dates compare correctly as strings, so the buckets need no parsing —
	// and no timezone argument about what "today" means beyond the one the
	// caller already made by choosing Now.
	today := in.Now.Format("2006-01-02")
	for _, t := range in.Tasks {
		if finishedStates[t.State] || t.DueDate == nil || *t.DueDate == "" {
			continue
		}
		due := *t.DueDate
		entry := Task{Slug: t.Slug, Title: t.Title, DueDate: due, State: t.State}
		switch {
		case due < today:
			s.Totals.Overdue++
			if len(s.Work.Overdue) < MaxTasks {
				s.Work.Overdue = append(s.Work.Overdue, entry)
			}
		case due == today:
			s.Totals.DueToday++
			if len(s.Work.Today) < MaxTasks {
				s.Work.Today = append(s.Work.Today, entry)
			}
		}
	}
	sort.SliceStable(s.Work.Overdue, func(i, j int) bool {
		if s.Work.Overdue[i].DueDate != s.Work.Overdue[j].DueDate {
			return s.Work.Overdue[i].DueDate < s.Work.Overdue[j].DueDate
		}
		return s.Work.Overdue[i].Slug < s.Work.Overdue[j].Slug
	})
	sort.SliceStable(s.Work.Today, func(i, j int) bool {
		return s.Work.Today[i].Slug < s.Work.Today[j].Slug
	})

	for _, m := range in.Mail {
		inbox := m.Inbox()
		s.Totals.Mail += inbox.Unread
		if len(s.Mail) >= MaxMailboxes {
			continue
		}
		box := Mailbox{Slug: m.Slug, Name: m.Name(), Address: m.Address, Unread: inbox.Unread}
		if m.LastError != nil {
			box.Error = *m.LastError
		}
		s.Mail = append(s.Mail, box)
	}
	// Busiest first, then by address, for the same reason the channels sort
	// that way: a list that reorders between writes repaints for nothing.
	sort.SliceStable(s.Mail, func(i, j int) bool {
		if s.Mail[i].Unread != s.Mail[j].Unread {
			return s.Mail[i].Unread > s.Mail[j].Unread
		}
		return s.Mail[i].Address < s.Mail[j].Address
	})

	return s
}

// ToNotification is the wire form the document carries. Exported because the
// daemon announces a single arrival long before it publishes a whole document.
func ToNotification(n api.Notification) Notification {
	out := Notification{
		ID:          n.ID,
		Action:      n.Action,
		Description: Describe(n),
		CreatedAt:   n.CreatedAt,
	}
	if n.Actor != nil {
		out.Actor = n.Actor.Name
	}
	if n.Message != nil {
		out.ChannelSlug = n.Message.ChannelSlug
		out.Preview = n.Message.Preview
	}
	if n.Task != nil {
		out.TaskSlug = n.Task.Slug
		out.TaskTitle = n.Task.Title
	}
	return out
}

// Countable reports whether a live message should move an unread count.
// store.Apply increments for every created message, including your own and
// including system events — harmless in the TUI, where the open channel is
// cleared continuously, and wrong in a daemon that only ever counts up.
func Countable(m *api.Message, selfUserID int64) bool {
	if m == nil || m.IsSystemEvent() {
		return false
	}
	return !(m.Sender.Type == "User" && m.Sender.ID == selfUserID)
}
