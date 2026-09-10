package ui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jkthorne/saltare-cli/internal/api"
	"github.com/jkthorne/saltare-cli/internal/assist"
)

// assistDefaultModel is served by the inference proxy on every plan tier and
// aliased in the server's cost calculator.
const assistDefaultModel = "claude-haiku-4-5"
const assistMaxTurns = 20

// assistEvent crosses from the streaming goroutine into the tea loop.
type assistEvent struct {
	delta string
	tool  string // pre-formatted trace line: a tool is about to run
	done  bool
	full  string
	turns []api.InferenceMessage // full history after the loop (done only)
	usage *api.InferenceUsage
	err   error
}

// assistant is the local Claude session: the conversation lives client-side,
// only inference rides through the server (which meters credits).
type assistant struct {
	active    bool
	turns     []api.InferenceMessage
	current   string // partial text while streaming
	streaming bool
	events    chan assistEvent
	usageLine string
}

func (a *assistant) pushUser(q string) {
	a.turns = assist.TrimTurns(append(a.turns, api.TextMessage("user", q)), assistMaxTurns)
}

func (a *assistant) pushAssistant(text string) {
	a.turns = assist.TrimTurns(append(a.turns, api.TextMessage("assistant", text)), assistMaxTurns)
}

// render builds the transcript pane content. markdown renders completed
// assistant turns; the in-flight turn stays raw with a cursor block.
func (a *assistant) render(r *feedRenderer, width int) string {
	var b strings.Builder
	if len(a.turns) == 0 && !a.streaming {
		b.WriteString(styleFeedTopic.Render("ask anything — the conversation stays on this device; inference is metered by the workspace"))
		b.WriteString("\n")
	}
	for _, turn := range a.turns {
		text := turn.TextContent()
		if turn.Role == "user" {
			if text == "" {
				continue // tool_result turns render via the tool trace, not as prose
			}
			b.WriteString("\n" + styleSenderUser.Render("you") + "\n")
			b.WriteString(indentPlain(text, width-4) + "\n")
			continue
		}
		b.WriteString("\n" + styleSenderAgent.Render("◆ claude") + "\n")
		for _, use := range turn.ToolUses() {
			b.WriteString(styleFeedTopic.Render("◇ "+toolCallLine(use)) + "\n")
		}
		if text == "" {
			continue
		}
		if r.markdown != nil {
			if out, err := r.markdown.Render(text); err == nil {
				b.WriteString(strings.Trim(out, "\n") + "\n")
				continue
			}
		}
		b.WriteString(indentPlain(text, width-4) + "\n")
	}
	if a.streaming {
		b.WriteString("\n" + styleSenderAgent.Render("◆ claude") + "\n")
		b.WriteString(indentPlain(a.current, width-4) + styleSelGutter.Render("▊") + "\n")
	}
	if a.usageLine != "" && !a.streaming {
		b.WriteString("\n" + styleFeedTopic.Render(a.usageLine) + "\n")
	}
	return b.String()
}

// toolCallLine compacts a tool_use block to one dim trace line.
func toolCallLine(use api.ContentBlock) string {
	args := make([]string, 0, len(use.Input))
	for k, v := range use.Input {
		args = append(args, fmt.Sprintf("%s: %v", k, v))
	}
	sort.Strings(args)
	line := use.Name + "(" + strings.Join(args, ", ") + ")"
	if len(line) > 80 {
		line = line[:77] + "..."
	}
	return line
}

func indentPlain(s string, width int) string {
	if width < 10 {
		width = 10
	}
	return wrapPlain(s, width)
}

// wrapPlain is a dumb word wrapper for raw (non-markdown) turns.
func wrapPlain(s string, width int) string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		for len(line) > width {
			cut := strings.LastIndex(line[:width], " ")
			if cut <= 0 {
				cut = width
			}
			out = append(out, line[:cut])
			line = strings.TrimLeft(line[cut:], " ")
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}
