package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jkthorne/saltare/cli/internal/api"
)

func testComposer() composer {
	c := newComposer()
	c.mentionables = []api.Mentionable{
		{Type: "user", ID: 1, Name: "Alice Chen"},
		{Type: "user", ID: 2, Name: "Bob Park"},
		{Type: "agent", ID: 3, Name: "Standup Bot", Slug: "standup-bot"},
		{Type: "agent", ID: 4, Name: "Standup", Slug: "standup"},
	}
	return c
}

func (c *composer) typeValue(v string) {
	c.ta.SetValue(v)
	c.detectMention()
}

func TestMentionDetection(t *testing.T) {
	c := testComposer()

	c.typeValue("hello @ali")
	if !c.mentionOpen {
		t.Fatal("popup should open on @ali")
	}
	if len(c.mentionMatches) != 1 || c.mentionMatches[0].Name != "Alice Chen" {
		t.Fatalf("unexpected matches: %+v", c.mentionMatches)
	}

	// Names with spaces keep matching while typed.
	c.typeValue("ping @standup b")
	if !c.mentionOpen || len(c.mentionMatches) != 1 || c.mentionMatches[0].Name != "Standup Bot" {
		t.Fatalf("space-name prefix should match Standup Bot: %+v", c.mentionMatches)
	}

	// Prefix "standup" matches both agents.
	c.typeValue("ping @standup")
	if len(c.mentionMatches) != 2 {
		t.Fatalf("want 2 standup matches, got %+v", c.mentionMatches)
	}

	// A bare @ lists everyone (capped).
	c.typeValue("@")
	if !c.mentionOpen || len(c.mentionMatches) != 4 {
		t.Fatalf("bare @ should list all 4, got %d", len(c.mentionMatches))
	}

	// No trailing mention → closed.
	c.typeValue("no mention here")
	if c.mentionOpen {
		t.Fatal("popup should close without a trailing @query")
	}

	// Email-like text must not trigger (@ not preceded by space/start).
	c.typeValue("mail me jack@example")
	if c.mentionOpen {
		t.Fatal("mid-word @ must not trigger the popup")
	}
}

func TestMentionCompletion(t *testing.T) {
	c := testComposer()
	c.typeValue("hey @ali")
	c.completeMention()

	if got := c.value(); got != "hey @Alice Chen " {
		t.Fatalf("unexpected completion: %q", got)
	}
	if c.mentionOpen {
		t.Fatal("popup should close after completion")
	}
}

func TestMentionSelectionWraps(t *testing.T) {
	c := testComposer()
	c.typeValue("@standup")
	c.moveMention(1)
	if c.mentionSel != 1 {
		t.Fatalf("want sel 1, got %d", c.mentionSel)
	}
	c.moveMention(1)
	if c.mentionSel != 0 {
		t.Fatalf("selection should wrap to 0, got %d", c.mentionSel)
	}
}

func TestBuildSidebarMergesAgents(t *testing.T) {
	agentType := "Agent"
	hostID := int64(3)
	channels := []api.Channel{
		{ID: 1, Name: "General", Kind: "public_channel"},
		{ID: 2, Name: "Standup Bot", Kind: "agent_dm", HostType: &agentType, HostID: &hostID},
	}
	agents := []api.Agent{
		{ID: 3, Slug: "standup-bot", Name: "Standup Bot", Status: "active"},
		{ID: 4, Slug: "researcher", Name: "Researcher", Status: "active"},
		{ID: 5, Slug: "archived", Name: "Old Agent", Status: "archived"},
	}

	items := buildSidebar(channels, agents)

	var stubs, agentChannels int
	for _, it := range items {
		if it.isAgentStub() {
			stubs++
			if it.agent.Slug != "researcher" {
				t.Fatalf("only channel-less active agents become stubs, got %s", it.agent.Slug)
			}
		} else if it.channel.Kind == "agent_dm" {
			agentChannels++
		}
	}
	if stubs != 1 {
		t.Fatalf("want 1 agent stub (researcher), got %d", stubs)
	}
	if agentChannels != 1 {
		t.Fatalf("want 1 agent_dm channel row, got %d", agentChannels)
	}
	if len(items) != 3 {
		t.Fatalf("want 3 rows total, got %d", len(items))
	}
}

func TestRenderEmbedChips(t *testing.T) {
	in := "see [[task:fix-login]] and ![[doc:api-spec]] plus [[not an embed]]"
	got := renderEmbedChips(in)
	if !strings.Contains(got, "⟨task:fix-login⟩") {
		t.Fatalf("inline chip missing: %q", got)
	}
	if !strings.Contains(got, "⟨doc:api-spec⟩") {
		t.Fatalf("block chip missing: %q", got)
	}
	if strings.Contains(got, "[[task:fix-login]]") || strings.Contains(got, "![[doc:api-spec]]") {
		t.Fatalf("raw embed syntax survived: %q", got)
	}
	if !strings.Contains(got, "[[not an embed]]") {
		t.Fatalf("non-embed brackets must stay untouched: %q", got)
	}
}

// TestComposerAcceptsTypingAtBoot drives a real key event through a freshly
// constructed composer. A blurred textarea silently ignores keys, and Init()
// can't focus it (value receiver — the mutation is discarded), so focus must
// be set at construction.
func TestComposerAcceptsTypingAtBoot(t *testing.T) {
	c := newComposer()
	c.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hi")})
	if c.value() != "hi" {
		t.Fatalf("fresh composer swallowed typing; got %q", c.value())
	}
}
