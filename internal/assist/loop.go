package assist

import (
	"context"

	"github.com/jkthorne/saltare-cli/internal/api"
)

// maxToolIterations bounds one user turn: after this many tool rounds the
// final call runs without tools, forcing a text answer.
const maxToolIterations = 8

type LoopOpts struct {
	Model     string
	System    string
	MaxTokens int
	Tools     []api.ToolDef                           // nil = plain chat
	OnText    func(delta string)                      // streaming text
	OnTool    func(name string, input map[string]any) // fires before each execution
}

// RunLoop drives the tool-use conversation: stream a completion, execute
// any tool_use blocks, feed tool_results back, repeat until the model
// stops asking (or the iteration cap forces a plain answer). Returns the
// history with all assistant/tool turns appended, plus the total usage
// across every hop — that's what the workspace was billed for.
func RunLoop(ctx context.Context, client *api.Client, exec *Executor, history []api.InferenceMessage, opts LoopOpts) ([]api.InferenceMessage, *api.InferenceResult, error) {
	turns := append([]api.InferenceMessage(nil), history...)
	total := api.InferenceUsage{}

	for iteration := 0; ; iteration++ {
		req := api.InferenceRequest{
			Model:     opts.Model,
			System:    opts.System,
			MaxTokens: opts.MaxTokens,
			Messages:  turns,
			Tools:     opts.Tools,
		}
		if iteration >= maxToolIterations {
			req.Tools = nil // force a final text answer
		}

		res, err := client.StreamInference(ctx, req, opts.OnText)
		if res != nil {
			total.InputTokens += res.Usage.InputTokens
			total.OutputTokens += res.Usage.OutputTokens
		}
		if err != nil {
			if res != nil {
				res.Usage = total
			}
			return turns, res, err
		}

		if len(res.Blocks) > 0 {
			turns = append(turns, api.BlocksMessage("assistant", res.Blocks))
		}

		uses := toolUses(res.Blocks)
		if res.StopReason != "tool_use" || len(uses) == 0 {
			res.Usage = total
			return turns, res, nil
		}

		results := make([]api.ContentBlock, 0, len(uses))
		for _, use := range uses {
			if opts.OnTool != nil {
				opts.OnTool(use.Name, use.Input)
			}
			if ctx.Err() != nil {
				res.Usage = total
				return turns, res, ctx.Err()
			}
			results = append(results, exec.Execute(ctx, use))
		}
		turns = append(turns, api.BlocksMessage("user", results))
	}
}

func toolUses(blocks []api.ContentBlock) []api.ContentBlock {
	var uses []api.ContentBlock
	for _, b := range blocks {
		if b.Type == "tool_use" {
			uses = append(uses, b)
		}
	}
	return uses
}

// TrimTurns caps conversation history without splitting a tool_use turn
// from its tool_result: after cutting from the front, the window advances
// to the next plain-text user turn, so it always opens at a conversation
// boundary Anthropic accepts.
func TrimTurns(turns []api.InferenceMessage, max int) []api.InferenceMessage {
	if len(turns) <= max {
		return turns
	}
	cut := len(turns) - max
	for cut < len(turns) && !isPlainUserTurn(turns[cut]) {
		cut++
	}
	return turns[cut:]
}

func isPlainUserTurn(turn api.InferenceMessage) bool {
	if turn.Role != "user" {
		return false
	}
	_, isText := turn.Content.(string)
	return isText
}
