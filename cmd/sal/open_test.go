package main

import (
	"testing"

	"github.com/jkthorne/saltare-cli/internal/weblink"
)

var openLinks = weblink.Links{Server: "https://saltare.test", Workspace: "acme"}

func TestResolveOpen(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"the workspace itself", nil, "https://saltare.test/w/acme"},
		{"a channel", []string{"channel", "general"}, "https://saltare.test/w/acme/channels/general"},
		{"a thread, by its slug alone", []string{"channel", "thread-ab12"}, "https://saltare.test/w/acme/t/thread-ab12"},
		{"a task", []string{"task", "fix-deploy"}, "https://saltare.test/w/acme/tasks/fix-deploy"},
		{"an agent", []string{"agent", "scout"}, "https://saltare.test/w/acme/agents/scout"},
		{"a message", []string{"message", "general", "42"}, "https://saltare.test/w/acme/channels/general#message_42"},
	} {
		got, err := resolveOpen(openLinks, tc.args)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s: want %q, got %q", tc.name, tc.want, got)
		}
	}
}

func TestResolveOpenRefusesWhatItCannotBuild(t *testing.T) {
	if _, err := resolveOpen(weblink.Links{}, []string{"channel", "general"}); err == nil {
		t.Error("no session, no URL")
	}
	if _, err := resolveOpen(openLinks, []string{"channel"}); err == nil {
		t.Error("a kind with no slug is a usage error, not an empty URL")
	}
	if _, err := resolveOpen(openLinks, []string{"nonsense", "x"}); err == nil {
		t.Error("an unknown kind should say so")
	}
	if _, err := resolveOpen(openLinks, []string{"message", "general"}); err == nil {
		t.Error("a message needs its id")
	}
	if _, err := resolveOpen(openLinks, []string{"message", "general", "abc"}); err == nil {
		t.Error("a message id must be a number")
	}
}
