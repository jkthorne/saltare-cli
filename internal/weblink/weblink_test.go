package weblink

import "testing"

var links = Links{Server: "https://saltare.test", Workspace: "acme"}

func TestChannelRoutesThreadsToTheirOwnPermalink(t *testing.T) {
	if got := links.Channel("", "general"); got != "https://saltare.test/w/acme/channels/general" {
		t.Errorf("got %q", got)
	}
	if got := links.Channel("thread", "thread-ab12"); got != "https://saltare.test/w/acme/t/thread-ab12" {
		t.Errorf("got %q", got)
	}
}

func TestChannelBySlugInfersTheKindFromThePrefix(t *testing.T) {
	// A notification payload carries a slug and no kind; the prefix is the
	// only signal there is.
	if got := links.ChannelBySlug("thread-ab12"); got != "https://saltare.test/w/acme/t/thread-ab12" {
		t.Errorf("got %q", got)
	}
	if got := links.ChannelBySlug("general"); got != "https://saltare.test/w/acme/channels/general" {
		t.Errorf("got %q", got)
	}
}

func TestMessageAnchorsInsideItsChannel(t *testing.T) {
	if got := links.Message("", "general", 42); got != "https://saltare.test/w/acme/channels/general#message_42" {
		t.Errorf("got %q", got)
	}
}

func TestTaskAndAgent(t *testing.T) {
	if got := links.Task("fix-deploy"); got != "https://saltare.test/w/acme/tasks/fix-deploy" {
		t.Errorf("got %q", got)
	}
	if got := links.Agent("scout"); got != "https://saltare.test/w/acme/agents/scout" {
		t.Errorf("got %q", got)
	}
}

func TestTrailingSlashOnTheServerDoesNotDoubleUp(t *testing.T) {
	trailing := Links{Server: "https://saltare.test/", Workspace: "acme"}
	if got := trailing.Task("x"); got != "https://saltare.test/w/acme/tasks/x" {
		t.Errorf("got %q", got)
	}
}

func TestTheZeroValueYieldsNoURLs(t *testing.T) {
	// A caller with no session should degrade to plain text, never to a link
	// that goes nowhere.
	var none Links
	for name, got := range map[string]string{
		"base":    none.Base(),
		"channel": none.Channel("", "general"),
		"task":    none.Task("x"),
		"agent":   none.Agent("x"),
		"message": none.Message("", "general", 1),
	} {
		if got != "" {
			t.Errorf("%s: want empty, got %q", name, got)
		}
	}
	if links.Task("") != "" {
		t.Error("an empty slug has no URL either")
	}
}
