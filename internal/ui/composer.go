package ui

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/jkthorne/saltare/cli/internal/api"
)

const (
	composerMaxHeight = 4
	mentionMaxMatches = 6
)

// mentionTail matches an @-mention being typed at the end of the input.
// Names may contain spaces ("@Standup Bot"), so the query runs to end-of-line;
// once the tail stops prefix-matching anyone the popup closes.
var mentionTail = regexp.MustCompile(`(?:^|\s)@([^@\n]*)$`)

type composer struct {
	ta           textarea.Model
	mentionables []api.Mentionable

	mentionOpen    bool
	mentionMatches []api.Mentionable
	mentionSel     int
}

func newComposer() composer {
	ta := textarea.New()
	ta.Prompt = "┃ "
	ta.Placeholder = "message…"
	ta.CharLimit = 0
	ta.ShowLineNumbers = false
	ta.SetHeight(1)
	// Enter sends (handled by the app); newline moves to ctrl+j.
	ta.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("ctrl+j"))
	// Focus at construction: Init() runs on a value-receiver copy, so a
	// Focus() there mutates a discarded model and the composer boots blurred
	// — silently swallowing all typing (a blurred textarea ignores keys).
	ta.Focus()
	return composer{ta: ta}
}

func (c *composer) setWidth(w int)          { c.ta.SetWidth(w) }
func (c *composer) focus() tea.Cmd          { return c.ta.Focus() }
func (c *composer) blur()                   { c.ta.Blur(); c.closeMention() }
func (c *composer) value() string           { return c.ta.Value() }
func (c *composer) reset()                  { c.ta.Reset(); c.ta.SetHeight(1); c.closeMention() }
func (c *composer) view() string            { return c.ta.View() }
func (c *composer) height() int             { return c.ta.Height() }
func (c *composer) setPlaceholder(s string) { c.ta.Placeholder = s }

func (c *composer) update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	c.ta, cmd = c.ta.Update(msg)

	lines := c.ta.LineCount()
	if lines < 1 {
		lines = 1
	}
	if lines > composerMaxHeight {
		lines = composerMaxHeight
	}
	c.ta.SetHeight(lines)

	c.detectMention()
	return cmd
}

func (c *composer) detectMention() {
	m := mentionTail.FindStringSubmatch(c.ta.Value())
	if m == nil {
		c.closeMention()
		return
	}
	query := strings.ToLower(m[1])
	var matches []api.Mentionable
	for _, cand := range c.mentionables {
		if strings.HasPrefix(strings.ToLower(cand.Name), query) {
			matches = append(matches, cand)
			if len(matches) == mentionMaxMatches {
				break
			}
		}
	}
	if len(matches) == 0 {
		c.closeMention()
		return
	}
	c.mentionOpen = true
	c.mentionMatches = matches
	if c.mentionSel >= len(matches) {
		c.mentionSel = 0
	}
}

func (c *composer) closeMention() {
	c.mentionOpen = false
	c.mentionMatches = nil
	c.mentionSel = 0
}

func (c *composer) moveMention(delta int) {
	if !c.mentionOpen {
		return
	}
	c.mentionSel = (c.mentionSel + delta + len(c.mentionMatches)) % len(c.mentionMatches)
}

// completeMention replaces the trailing @query with the selected name.
// MentionExtractionJob matches "@" + exact name, case-insensitively.
func (c *composer) completeMention() {
	if !c.mentionOpen {
		return
	}
	chosen := c.mentionMatches[c.mentionSel]
	v := c.ta.Value()
	at := strings.LastIndex(v, "@") // safe: the match group excludes "@"
	if at < 0 {
		return
	}
	c.ta.SetValue(v[:at] + "@" + chosen.Name + " ")
	c.closeMention()
}

// mentionPopup renders the completion list shown above the composer.
func (c *composer) mentionPopup(width int) string {
	if !c.mentionOpen {
		return ""
	}
	var rows []string
	for i, m := range c.mentionMatches {
		glyph := "@"
		if m.Type == "agent" {
			glyph = "◇"
		}
		row := " " + glyph + " " + m.Name
		if i == c.mentionSel {
			row = styleMentionSel.Render(truncate(row, width-2))
		} else {
			row = styleMentionRow.Render(truncate(row, width-2))
		}
		rows = append(rows, row)
	}
	return strings.Join(rows, "\n")
}

func (c *composer) popupHeight() int {
	if !c.mentionOpen {
		return 0
	}
	return len(c.mentionMatches)
}
