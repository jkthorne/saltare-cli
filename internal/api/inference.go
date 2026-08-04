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
// Anthropic wire format.

type InferenceMessage struct {
	Role    string `json:"role"` // "user" | "assistant"
	Content string `json:"content"`
}

type InferenceRequest struct {
	Model     string
	MaxTokens int
	System    string
	Messages  []InferenceMessage
}

type InferenceUsage struct {
	InputTokens  int
	OutputTokens int
}

// StreamInference runs one streaming completion. onDelta receives text
// fragments as they arrive; the full text and token usage return at the end.
func (c *Client) StreamInference(ctx context.Context, req InferenceRequest, onDelta func(string)) (string, *InferenceUsage, error) {
	text, usage, status, err := c.streamOnce(ctx, req, onDelta, c.AccessToken())
	if status != http.StatusUnauthorized {
		return text, usage, err
	}
	if err := c.refresh(ctx, c.AccessToken()); err != nil {
		return "", nil, err
	}
	text, usage, _, err = c.streamOnce(ctx, req, onDelta, c.AccessToken())
	return text, usage, err
}

func (c *Client) streamOnce(ctx context.Context, req InferenceRequest, onDelta func(string), token string) (string, *InferenceUsage, int, error) {
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 2048
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
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", nil, 0, err
	}

	u := *c.base
	u.Path = strings.TrimRight(u.Path, "/") + "/api/v1/inference/messages"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(raw))
	if err != nil {
		return "", nil, 0, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	// The shared client has a 30s total timeout — too short for a long
	// completion. Streams get a timeout-free client; ctx handles cancellation.
	resp, err := (&http.Client{}).Do(httpReq)
	if err != nil {
		return "", nil, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return "", nil, resp.StatusCode, nil
	}
	if resp.StatusCode != http.StatusOK {
		return "", nil, resp.StatusCode, decodeError(resp)
	}

	return parseSSE(resp.Body, onDelta)
}

// parseSSE consumes an Anthropic Messages SSE stream: text arrives in
// content_block_delta (text_delta) events, input tokens in message_start,
// cumulative output tokens in message_delta.
func parseSSE(body interface{ Read([]byte) (int, error) }, onDelta func(string)) (string, *InferenceUsage, int, error) {
	var full strings.Builder
	usage := &InferenceUsage{}

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
			Type  string `json:"type"`
			Delta struct {
				Type string `json:"type"`
				Text string `json:"text"`
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
		case "content_block_delta":
			if ev.Delta.Type == "text_delta" && ev.Delta.Text != "" {
				full.WriteString(ev.Delta.Text)
				if onDelta != nil {
					onDelta(ev.Delta.Text)
				}
			}
		case "message_start":
			usage.InputTokens = ev.Message.Usage.InputTokens
		case "message_delta":
			if ev.Usage.OutputTokens > 0 {
				usage.OutputTokens = ev.Usage.OutputTokens
			}
		case "error":
			return full.String(), usage, http.StatusOK, fmt.Errorf("inference error: %s", ev.Error.Message)
		case "message_stop":
			return full.String(), usage, http.StatusOK, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return full.String(), usage, http.StatusOK, err
	}
	return full.String(), usage, http.StatusOK, nil
}
