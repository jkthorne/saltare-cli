package ui

import (
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jkthorne/saltare/cli/internal/api"
)

var testLinks = webLinks{server: "https://saltare.test", workspace: "acme"}

// osc8Pattern matches the whole hyperlink wrapper so tests can check both that
// the URL is there and that the visible label is unchanged without it.
var osc8Pattern = regexp.MustCompile("\x1b\\]8;;[^\x1b]*\x1b\\\\")

func stripOSC8(s string) string { return osc8Pattern.ReplaceAllString(s, "") }

// The whole reason hyperlinks are safe to add to already-laid-out text: they
// occupy no cells, so nothing downstream needs to know they are there.
func TestOSC8CostsNoWidth(t *testing.T) {
	label := "⟨task:fix-login⟩"
	linked := osc8("https://saltare.test/w/acme/tasks/fix-login", label)

	if got, want := lipgloss.Width(linked), lipgloss.Width(label); got != want {
		t.Fatalf("a linked label must measure as its text: got %d, want %d", got, want)
	}
	if got := stripOSC8(linked); got != label {
		t.Fatalf("stripping the link must leave the label: got %q", got)
	}
	if got := osc8("", label); got != label {
		t.Fatalf("no URL means no wrapper: got %q", got)
	}
}

func TestWebLinksBuildsWorkspaceURLs(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"channel", testLinks.channel("public_channel", "general"), "https://saltare.test/w/acme/channels/general"},
		{"thread", testLinks.channel("thread", "thread-ab12"), "https://saltare.test/w/acme/t/thread-ab12"},
		{"message", testLinks.message("public_channel", "general", 42), "https://saltare.test/w/acme/channels/general#message_42"},
		{"thread message", testLinks.message("thread", "thread-ab12", 7), "https://saltare.test/w/acme/t/thread-ab12#message_7"},
		{"task", testLinks.task("fix-login"), "https://saltare.test/w/acme/tasks/fix-login"},
		{"agent", testLinks.agent("scout"), "https://saltare.test/w/acme/agents/scout"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, c.got, c.want)
		}
	}
}

// A renderer built without workspace context (tests, a docs reader before login)
// must produce plain text rather than half-formed URLs.
func TestZeroWebLinksProduceNoURLs(t *testing.T) {
	var zero webLinks
	if got := zero.channel("public_channel", "general"); got != "" {
		t.Fatalf("no server means no URL, got %q", got)
	}
	if got := zero.message("public_channel", "general", 1); got != "" {
		t.Fatalf("no server means no permalink, got %q", got)
	}
	// A trailing slash on the server URL must not double up.
	trailing := webLinks{server: "https://saltare.test/", workspace: "acme"}
	if got := trailing.task("x"); got != "https://saltare.test/w/acme/tasks/x" {
		t.Fatalf("trailing slash mishandled: %q", got)
	}
}

func TestEmbedLinksOnlySlugAddressableTypes(t *testing.T) {
	linked := map[string]string{
		"task":    "https://saltare.test/w/acme/tasks/fix-login",
		"agent":   "https://saltare.test/w/acme/agents/fix-login",
		"channel": "https://saltare.test/w/acme/channels/fix-login",
	}
	for kind, want := range linked {
		if got := testLinks.embed(embedRef{kind: kind, ref: "fix-login"}); got != want {
			t.Errorf("%s: got %q, want %q", kind, got, want)
		}
	}
	// A thread slug routes to /t/ — the prefix is the only signal the reference
	// carries about which kind of channel it names.
	if got := testLinks.embed(embedRef{kind: "channel", ref: "thread-ab12"}); got != "https://saltare.test/w/acme/t/thread-ab12" {
		t.Errorf("thread reference: got %q", got)
	}
	// These live at data-tree paths, or need a channel sal doesn't have. A dead
	// link would be worse than a plain chip.
	for _, kind := range []string{"doc", "upload", "db", "msg", "unknown"} {
		if got := testLinks.embed(embedRef{kind: kind, ref: "whatever"}); got != "" {
			t.Errorf("%s must stay unlinked, got %q", kind, got)
		}
	}
}

func TestRenderEmbedChipsHyperlinksResolvableTypes(t *testing.T) {
	tokenized, refs := tokenizeEmbeds("see [[task:fix-login]] and ![[doc:api-spec]]")
	got := restoreEmbedChips(tokenized, refs, testLinks)

	if !strings.Contains(got, "https://saltare.test/w/acme/tasks/fix-login") {
		t.Fatalf("the task chip must carry its URL: %q", got)
	}
	if strings.Contains(got, "api-spec\x1b]8") || strings.Contains(stripOSC8(got), "\x1b]8") {
		t.Fatalf("the doc chip must stay unlinked: %q", got)
	}
	// Visible text is untouched either way.
	plain := stripOSC8(got)
	if !strings.Contains(plain, "⟨task:fix-login⟩") || !strings.Contains(plain, "⟨doc:api-spec⟩") {
		t.Fatalf("chips must render the same linked or not: %q", plain)
	}
}

// Chips have to survive markdown rendering. Substituting after glamour never
// worked: it colours per token and splits on punctuation, so "[[" comes back as
// "[" ESC "[" and the pattern silently stops matching. Every message body in the
// feed went through that path, so chips only ever appeared in the plain-text
// fallback. Tokenizing first is what fixes it — and the token has to survive
// wrapping, lists, headings and inline code too.
func TestEmbedChipsSurviveMarkdownRendering(t *testing.T) {
	r := newFeedRenderer(60, testLinks)
	bodies := map[string]string{
		"paragraph":   "look at [[task:fix-login]] please",
		"wrapped":     strings.Repeat("padding words to force a wrap ", 4) + "[[task:fix-login]] trailing text",
		"list":        "- first [[task:fix-login]]\n- second",
		"heading":     "# heading [[task:fix-login]]",
		"inline code": "run `[[task:fix-login]]`",
	}
	for name, body := range bodies {
		source, refs := tokenizeEmbeds(body)
		if len(refs) != 1 {
			t.Fatalf("%s: want 1 reference, got %d", name, len(refs))
		}
		out, err := r.markdown.Render(source)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got := restoreEmbedChips(out, refs, testLinks)
		if !strings.Contains(stripOSC8(got), "⟨task:fix-login⟩") {
			t.Errorf("%s: chip lost through markdown: %q", name, got)
		}
		if !strings.Contains(got, "https://saltare.test/w/acme/tasks/fix-login") {
			t.Errorf("%s: hyperlink lost through markdown: %q", name, got)
		}
	}
}

// A marker typed by a user has no reference behind it and must render literally
// rather than becoming someone else's chip.
func TestStrayEmbedTokenIsLeftAlone(t *testing.T) {
	source, refs := tokenizeEmbeds("literal " + embedToken(4) + " and [[task:real]]")
	got := restoreEmbedChips(source, refs, testLinks)
	if !strings.Contains(got, embedToken(4)) {
		t.Fatalf("an unbacked marker must survive verbatim: %q", got)
	}
	if !strings.Contains(stripOSC8(got), "⟨task:real⟩") {
		t.Fatalf("the real reference must still render: %q", got)
	}
}

// Repeats each get their own marker, so two references to the same thing both
// render instead of one swallowing the other.
func TestRepeatedEmbedsEachGetAChip(t *testing.T) {
	source, refs := tokenizeEmbeds("[[task:a]] then [[task:a]] again")
	if len(refs) != 2 {
		t.Fatalf("want 2 references, got %d", len(refs))
	}
	got := stripOSC8(restoreEmbedChips(source, refs, testLinks))
	if n := strings.Count(got, "⟨task:a⟩"); n != 2 {
		t.Fatalf("want 2 chips, got %d: %q", n, got)
	}
}

// The feed's timestamp is the click target for "open this message on the web" —
// the same URL `y` copies to the clipboard.
func TestFeedTimestampCarriesThePermalink(t *testing.T) {
	r := newFeedRenderer(80, testLinks)
	channel := api.Channel{ID: 1, Slug: "general", Kind: "public_channel"}
	msgs := []api.Message{
		{ID: 42, ChannelID: 1, Body: "hello", Sender: api.Sender{Type: "User", ID: 7, Name: "Me"}},
	}

	content, _ := r.Render(msgs, &channel, 0, 0)
	if !strings.Contains(content, "https://saltare.test/w/acme/channels/general#message_42") {
		t.Fatalf("the header timestamp must link to the message: %q", content)
	}

	// nil channel (no slug to build from) must not emit a broken link.
	bare, _ := r.Render(msgs, nil, 0, 0)
	if strings.Contains(bare, "\x1b]8;;") {
		t.Fatalf("without a channel there is no permalink to emit: %q", bare)
	}
}

// The links have to survive the whole render path, not just the renderer: the
// viewport pads and truncates every visible line, and lipgloss styles wrap the
// pane. Anything that measured a sequence as visible width would corrupt it here.
func TestHyperlinksSurviveTheFullFrame(t *testing.T) {
	m := testModel(t)
	m.links = testLinks
	m.renderer = newFeedRenderer(80, testLinks)

	channel := api.Channel{ID: 1, Slug: "general", Name: "General", Kind: "public_channel", Member: true}
	step, _ := m.Update(channelsLoadedMsg{channels: []api.Channel{channel}})
	step, _ = step.(Model).Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	model := step.(Model)
	model.store.MergeHistory(1, []api.Message{{
		ID: 42, ChannelID: 1, Body: "look at [[task:fix-login]]",
		Sender: api.Sender{Type: "User", ID: 7, Name: "Me"},
	}})
	model.focusedID = 1
	model.refreshFeed(true)

	view := model.View()
	for _, want := range []string{
		"https://saltare.test/w/acme/channels/general#message_42", // header timestamp
		"https://saltare.test/w/acme/tasks/fix-login",             // embed chip
		"https://saltare.test/w/acme/channels/general\x1b\\",      // feed title
	} {
		if !strings.Contains(view, want) {
			t.Errorf("the frame lost a hyperlink: %s", want)
		}
	}
	// And the visible text is unaffected — links add no columns.
	plain := stripOSC8(view)
	for _, line := range strings.Split(plain, "\n") {
		if lipgloss.Width(line) > model.width {
			t.Fatalf("a line overflowed the terminal width: %q", line)
		}
	}
}

// The copied permalink and the clicked one come from one builder, so they can't
// drift apart.
func TestCopiedPermalinkMatchesTheHyperlink(t *testing.T) {
	m := testModel(t)
	m.links = testLinks
	if got, want := m.messagePermalink("thread", "thread-ab12", 9), testLinks.message("thread", "thread-ab12", 9); got != want {
		t.Fatalf("permalink drift: copy=%q link=%q", got, want)
	}
}
