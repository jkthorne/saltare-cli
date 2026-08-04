package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// The inference proxy (POST /api/v1/inference/messages) is a transparent
// passthrough to the Anthropic Messages API: the server holds the provider
// key and meters workspace credits; requests and SSE responses stay in the
// Anthropic wire format — including tool_use/tool_result content blocks.

// ContentBlock is one element of a message's content array. Exactly one
// shape is populated, keyed by Type.
type ContentBlock struct {
	Type string // "text" | "tool_use" | "tool_result"

	Text string // text

	ID    string         // tool_use
	Name  string         // tool_use
	Input map[string]any // tool_use

	ToolUseID string // tool_result
	Content   string // tool_result payload (text)
	IsError   bool   // tool_result
}

// MarshalJSON emits the exact Anthropic wire shape per block type — a flat
// struct with omitempty can't, because tool_use requires "input" even when
// empty while other types must not carry it.
func (b ContentBlock) MarshalJSON() ([]byte, error) {
	switch b.Type {
	case "tool_use":
		input := b.Input
		if input == nil {
			input = map[string]any{}
		}
		return json.Marshal(map[string]any{"type": "tool_use", "id": b.ID, "name": b.Name, "input": input})
	case "tool_result":
		out := map[string]any{"type": "tool_result", "tool_use_id": b.ToolUseID, "content": b.Content}
		if b.IsError {
			out["is_error"] = true
		}
		return json.Marshal(out)
	default:
		return json.Marshal(map[string]any{"type": "text", "text": b.Text})
	}
}

// InferenceMessage's Content is either a plain string or []ContentBlock —
// both marshal to valid Anthropic content.
type InferenceMessage struct {
	Role    string `json:"role"` // "user" | "assistant"
	Content any    `json:"content"`
}

func TextMessage(role, text string) InferenceMessage {
	return InferenceMessage{Role: role, Content: text}
}

func BlocksMessage(role string, blocks []ContentBlock) InferenceMessage {
	return InferenceMessage{Role: role, Content: blocks}
}

// TextContent flattens the message to display text (tool blocks excluded).
func (m InferenceMessage) TextContent() string {
	switch content := m.Content.(type) {
	case string:
		return content
	case []ContentBlock:
		var b strings.Builder
		for _, block := range content {
			if block.Type == "text" {
				b.WriteString(block.Text)
			}
		}
		return b.String()
	default:
		return ""
	}
}

// ToolUses returns the message's tool_use blocks, if any.
func (m InferenceMessage) ToolUses() []ContentBlock {
	blocks, ok := m.Content.([]ContentBlock)
	if !ok {
		return nil
	}
	var uses []ContentBlock
	for _, block := range blocks {
		if block.Type == "tool_use" {
			uses = append(uses, block)
		}
	}
	return uses
}

// ToolDef mirrors the Anthropic tools array entry.
type ToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

type InferenceRequest struct {
	Model     string
	MaxTokens int
	System    string
	Messages  []InferenceMessage
	Tools     []ToolDef
}

type InferenceUsage struct {
	InputTokens  int
	OutputTokens int
}

// InferenceResult is one complete assistant turn: ordered content blocks
// (text and tool_use), the flattened text, and why the model stopped —
// StopReason "tool_use" means the caller owes tool_result blocks back.
type InferenceResult struct {
	Blocks     []ContentBlock
	Text       string
	StopReason string
	Usage      InferenceUsage
}

// StreamInference runs one streaming completion. onDelta receives text
// fragments as they arrive; the assembled result returns at the end.
func (c *Client) StreamInference(ctx context.Context, req InferenceRequest, onDelta func(string)) (*InferenceResult, error) {
	res, status, err := c.streamOnce(ctx, req, onDelta, c.AccessToken())
	if status != http.StatusUnauthorized {
		return res, err
	}
	if err := c.refresh(ctx, c.AccessToken()); err != nil {
		return nil, err
	}
	res, _, err = c.streamOnce(ctx, req, onDelta, c.AccessToken())
	return res, err
}

func (c *Client) streamOnce(ctx context.Context, req InferenceRequest, onDelta func(string), token string) (*InferenceResult, int, error) {
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 4096
	}
	payload := map[string]any{
		"model":      req.Model,
		"max_tokens": maxTokens,
		"messages":   req.Messages,
		"stream":     true,
	}
	if req.System != "" {
		payload["system"] = req.System
	}
	if len(req.Tools) > 0 {
		payload["tools"] = req.Tools
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, 0, err
	}

	u := *c.base
	u.Path = strings.TrimRight(u.Path, "/") + "/api/v1/inference/messages"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(raw))
	if err != nil {
		return nil, 0, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	// The shared client has a 30s total timeout — too short for a long
	// completion. Streams get a timeout-free client; ctx handles cancellation.
	resp, err := (&http.Client{}).Do(httpReq)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, resp.StatusCode, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, decodeError(resp)
	}

	return parseSSE(resp.Body, onDelta)
}

// parseSSE consumes an Anthropic Messages SSE stream into ordered content
// blocks: content_block_start opens a block (tool_use carries id+name),
// text_delta/input_json_delta grow it, content_block_stop finalizes tool
// arguments, message_delta carries stop_reason and output tokens.
func parseSSE(body interface{ Read([]byte) (int, error) }, onDelta func(string)) (*InferenceResult, int, error) {
	res := &InferenceResult{}
	var text strings.Builder
	slotAt := map[int]int{}               // SSE content index → res.Blocks slot
	jsonBuf := map[int]*strings.Builder{} // tool_use argument accumulation

	finish := func(err error) (*InferenceResult, int, error) {
		res.Text = text.String()
		return res, http.StatusOK, err
	}

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}

		var ev struct {
			Type         string `json:"type"`
			Index        int    `json:"index"`
			ContentBlock struct {
				Type string `json:"type"`
				ID   string `json:"id"`
				Name string `json:"name"`
				Text string `json:"text"`
			} `json:"content_block"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				PartialJSON string `json:"partial_json"`
				StopReason  string `json:"stop_reason"`
			} `json:"delta"`
			Message struct {
				Usage struct {
					InputTokens  int `json:"input_tokens"`
					OutputTokens int `json:"output_tokens"`
				} `json:"usage"`
			} `json:"message"`
			Usage struct {
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
			Error struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			continue // tolerate unknown frames
		}

		switch ev.Type {
		case "content_block_start":
			slotAt[ev.Index] = len(res.Blocks)
			res.Blocks = append(res.Blocks, ContentBlock{
				Type: ev.ContentBlock.Type,
				ID:   ev.ContentBlock.ID,
				Name: ev.ContentBlock.Name,
				Text: ev.ContentBlock.Text,
			})
			if ev.ContentBlock.Type == "tool_use" {
				jsonBuf[ev.Index] = &strings.Builder{}
			}
		case "content_block_delta":
			switch ev.Delta.Type {
			case "text_delta":
				if ev.Delta.Text == "" {
					continue
				}
				text.WriteString(ev.Delta.Text)
				if slot, ok := slotAt[ev.Index]; ok {
					res.Blocks[slot].Text += ev.Delta.Text
				}
				if onDelta != nil {
					onDelta(ev.Delta.Text)
				}
			case "input_json_delta":
				if buf := jsonBuf[ev.Index]; buf != nil {
					buf.WriteString(ev.Delta.PartialJSON)
				}
			}
		case "content_block_stop":
			slot, ok := slotAt[ev.Index]
			if !ok || res.Blocks[slot].Type != "tool_use" {
				continue
			}
			raw := "{}"
			if buf := jsonBuf[ev.Index]; buf != nil && buf.Len() > 0 {
				raw = buf.String()
			}
			input := map[string]any{}
			_ = json.Unmarshal([]byte(raw), &input) // malformed args stay {}
			res.Blocks[slot].Input = input
		case "message_start":
			res.Usage.InputTokens = ev.Message.Usage.InputTokens
		case "message_delta":
			if ev.Usage.OutputTokens > 0 {
				res.Usage.OutputTokens = ev.Usage.OutputTokens
			}
			if ev.Delta.StopReason != "" {
				res.StopReason = ev.Delta.StopReason
			}
		case "error":
			return finish(fmt.Errorf("inference error: %s", ev.Error.Message))
		case "message_stop":
			return finish(nil)
		}
	}
	if err := scanner.Err(); err != nil {
		return finish(err)
	}
	return finish(nil)
}
