package main

import (
	"strings"
	"testing"

	"github.com/jkthorne/saltare-cli/internal/watch"
	"github.com/jkthorne/saltare-cli/internal/weblink"
)

type recorder struct {
	calls   [][]string
	present map[string]bool
}

func testNotifier(all bool, helpers ...string) (*notifier, *recorder) {
	rec := &recorder{present: map[string]bool{}}
	for _, h := range helpers {
		rec.present[h] = true
	}
	n := &notifier{
		links: weblink.Links{Server: "https://saltare.test", Workspace: "acme"},
		all:   all,
		have:  func(name string) bool { return rec.present[name] },
		run: func(name string, args ...string) error {
			rec.calls = append(rec.calls, append([]string{name}, args...))
			return nil
		},
	}
	return n, rec
}

func mentionNote() watch.Notification {
	return watch.Notification{
		ID: 991, Action: "mentioned", Actor: "Alice Chen", Description: "mentioned you",
		ChannelSlug: "general", Preview: "can you look at the deploy?",
	}
}

func TestNotifyOnlyPersonalActionsByDefault(t *testing.T) {
	n, rec := testNotifier(false, "omarchy-notification-send")

	n.notify(mentionNote())
	if len(rec.calls) != 1 {
		t.Fatalf("a mention should toast, got %d calls", len(rec.calls))
	}

	n.notify(watch.Notification{ID: 1, Action: "credit_alert", Description: "AI credits at 80%"})
	if len(rec.calls) != 1 {
		t.Error("a credit alert belongs in the workspace, not on top of what you were doing")
	}
}

func TestNotifyAllLetsWorkspaceAlertsThrough(t *testing.T) {
	n, rec := testNotifier(true, "omarchy-notification-send")
	n.notify(watch.Notification{ID: 1, Action: "credit_alert", Description: "AI credits at 80%"})
	if len(rec.calls) != 1 {
		t.Fatalf("--notify-all means all, got %d calls", len(rec.calls))
	}
}

func TestToastIsClickableThroughOmarchy(t *testing.T) {
	n, _ := testNotifier(false, "omarchy-notification-send")
	name, args := n.command(mentionNote())

	if name != "omarchy-notification-send" {
		t.Fatalf("got %q", name)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--exec sal open channel general") {
		t.Errorf("the toast must land you in the channel, got %q", joined)
	}
	if !strings.Contains(joined, "Alice Chen · #general") {
		t.Errorf("headline: got %q", joined)
	}
	if !strings.Contains(joined, "can you look at the deploy?") {
		t.Errorf("body: got %q", joined)
	}
}

func TestFallsBackToNotifySendWithoutTheClickAction(t *testing.T) {
	n, _ := testNotifier(false, "notify-send")
	name, args := n.command(mentionNote())

	if name != "notify-send" {
		t.Fatalf("got %q", name)
	}
	if strings.Contains(strings.Join(args, " "), "--exec") {
		t.Error("notify-send has no --exec; the toast informs but cannot navigate")
	}
}

func TestNoNotifierOnTheMachineIsNotAnError(t *testing.T) {
	n, rec := testNotifier(false)
	n.notify(mentionNote())
	if len(rec.calls) != 0 {
		t.Error("nothing to call, so nothing was called")
	}
}

func TestBurstInOneChannelCollapsesOntoOneToast(t *testing.T) {
	first := mentionNote()
	second := mentionNote()
	second.ID = 992
	second.Preview = "and the migration"

	if replaceID(first) != replaceID(second) {
		t.Error("two messages in one channel must replace each other, not stack")
	}

	other := mentionNote()
	other.ChannelSlug = "engineering"
	if replaceID(first) == replaceID(other) {
		t.Error("two channels are two toasts")
	}
	if replaceID(watch.Notification{Action: "credit_alert"}) != 0 {
		t.Error("nothing to key on means no replacement id")
	}
}

func TestPhraseSaysWhatHappenedToATask(t *testing.T) {
	_, body := phrase(watch.Notification{
		Action: "assigned_task", Actor: "Alice Chen",
		Description: "assigned you a task", TaskSlug: "fix-deploy", TaskTitle: "Fix the deploy",
	})
	if body != "assigned you a task: Fix the deploy" {
		t.Errorf("got %q", body)
	}

	// A mention's preview speaks for itself.
	if _, body := phrase(mentionNote()); body != "can you look at the deploy?" {
		t.Errorf("got %q", body)
	}
}

func TestTaskNotificationsClickThroughToTheTask(t *testing.T) {
	n, _ := testNotifier(false, "omarchy-notification-send")
	_, args := n.command(watch.Notification{
		Action: "assigned_task", Actor: "Alice Chen",
		Description: "assigned you a task", TaskSlug: "fix-deploy", TaskTitle: "Fix the deploy",
	})
	if !strings.Contains(strings.Join(args, " "), "--exec sal open task fix-deploy") {
		t.Errorf("got %q", strings.Join(args, " "))
	}
}
