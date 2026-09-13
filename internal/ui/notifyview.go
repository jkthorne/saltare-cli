package ui

import (
	"strings"

	"github.com/jkthorne/saltare-cli/internal/api"
)

// notifyView is the ctrl+n overlay: the caller's unread notifications.
type notifyView struct {
	active bool
	items  []api.Notification
	sel    int
}

func (n *notifyView) move(delta int) {
	if len(n.items) == 0 {
		return
	}
	next := n.sel + delta
	if next >= 0 && next < len(n.items) {
		n.sel = next
	}
}

func (n *notifyView) selected() (api.Notification, bool) {
	if len(n.items) == 0 || n.sel >= len(n.items) {
		return api.Notification{}, false
	}
	return n.items[n.sel], true
}

// notificationText renders a compact human line from the action enum — the
// serializer deliberately ships context, not prose.
func notificationText(n api.Notification) string {
	actor := "someone"
	if n.Actor != nil {
		actor = n.Actor.Name
	}
	switch n.Action {
	case "mentioned":
		return actor + " mentioned you"
	case "replied_in_thread":
		return actor + " replied in a thread"
	case "assigned_task":
		if n.Task != nil {
			return actor + " assigned you: " + n.Task.Title
		}
		return actor + " assigned you a task"
	case "task_state_changed":
		if n.Task != nil {
			return "task updated: " + n.Task.Title
		}
		return "a task changed state"
	case "task_due_soon":
		if n.Task != nil {
			return "due soon: " + n.Task.Title
		}
		return "a task is due soon"
	case "new_message":
		if n.Message != nil {
			return actor + ": " + n.Message.Preview
		}
		return actor + " sent a message"
	default:
		return strings.ReplaceAll(n.Action, "_", " ")
	}
}

func (n *notifyView) render(width, height int) string {
	body := n.lines(width).join()
	return styleTasksPane.Width(width).Height(height).MaxHeight(height).Render(body)
}

// lines lays the inbox out, tagging each line with its index into n.items.
func (n *notifyView) lines(width int) *rowBuilder {
	b := &rowBuilder{}
	b.chrome(stylePickerTitle.Render("◉ notifications"), "")
	if len(n.items) == 0 {
		b.chrome(styleFeedTopic.Render("all clear"))
	}
	for i, item := range n.items {
		line := notificationText(item)
		if item.Message != nil {
			line += styleFeedTopic.Render("  #" + item.Message.ChannelSlug)
		}
		line += "  " + styleTimestamp.Render(item.CreatedAt.Local().Format("Jan 2 15:04"))
		b.row(pickerLine(line, i == n.sel, width), i)
	}
	b.chrome("", styleFeedTopic.Render("enter jump · R mark all read · esc close"))
	return b
}
