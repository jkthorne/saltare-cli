package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jkthorne/saltare-cli/internal/config"
)

func sseServer(t *testing.T, events []string, wantModel string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/inference/messages", func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, 8192)
		n, _ := r.Body.Read(body)
		if wantModel != "" && !strings.Contains(string(body[:n]), wantModel) {
			t.Errorf("model %q missing from request body", wantModel)
		}
		if !strings.Contains(string(body[:n]), `"stream":true`) {
			t.Error("stream:true missing from request body")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, ev := range events {
			w.Write([]byte(ev))
		}
	})
	return httptest.NewServer(mux)
}

func TestStreamInference(t *testing.T) {
	events := []string{
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":25,\"output_tokens\":1}}}\n\n",
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"salta\"}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"re\"}}\n\n",
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n",
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":7}}\n\n",
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
	}
	srv := sseServer(t, events, "claude-haiku-4-5")
	defer srv.Close()

	client, _ := New(srv.URL, config.Tokens{AccessToken: "t", RefreshToken: "r"})
	var deltas []string
	res, err := client.StreamInference(context.Background(), InferenceRequest{
		Model:    "claude-haiku-4-5",
		Messages: []InferenceMessage{{Role: "user", Content: "hi"}},
	}, func(d string) { deltas = append(deltas, d) })

	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "saltare" {
		t.Fatalf("text: %q", res.Text)
	}
	if len(deltas) != 2 || deltas[0] != "salta" {
		t.Fatalf("deltas: %v", deltas)
	}
	if res.Usage.InputTokens != 25 || res.Usage.OutputTokens != 7 {
		t.Fatalf("usage: %+v", res.Usage)
	}
	if res.StopReason != "end_turn" {
		t.Fatalf("stop reason: %q", res.StopReason)
	}
	if len(res.Blocks) != 1 || res.Blocks[0].Type != "text" || res.Blocks[0].Text != "saltare" {
		t.Fatalf("blocks: %+v", res.Blocks)
	}
}

func TestStreamInferenceToolUse(t *testing.T) {
	events := []string{
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":40,\"output_tokens\":1}}}\n\n",
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Looking that up.\"}}\n\n",
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n",
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_01\",\"name\":\"search_workspace\"}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"q\\\":\"}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"\\\"billing\\\"}\"}}\n\n",
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":1}\n\n",
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":31}}\n\n",
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
	}
	srv := sseServer(t, events, "")
	defer srv.Close()

	client, _ := New(srv.URL, config.Tokens{AccessToken: "t", RefreshToken: "r"})
	res, err := client.StreamInference(context.Background(), InferenceRequest{
		Model:    "claude-haiku-4-5",
		Messages: []InferenceMessage{{Role: "user", Content: "what did we decide about billing?"}},
		Tools:    []ToolDef{{Name: "search_workspace", Description: "Search", InputSchema: map[string]any{"type": "object"}}},
	}, nil)

	if err != nil {
		t.Fatal(err)
	}
	if res.StopReason != "tool_use" {
		t.Fatalf("stop reason: %q", res.StopReason)
	}
	if len(res.Blocks) != 2 {
		t.Fatalf("blocks: %+v", res.Blocks)
	}
	use := res.Blocks[1]
	if use.Type != "tool_use" || use.ID != "toolu_01" || use.Name != "search_workspace" {
		t.Fatalf("tool_use block: %+v", use)
	}
	if use.Input["q"] != "billing" {
		t.Fatalf("tool input: %+v", use.Input)
	}
	if res.Text != "Looking that up." {
		t.Fatalf("text: %q", res.Text)
	}
}

func TestStreamInferenceEmptyToolInput(t *testing.T) {
	events := []string{
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_02\",\"name\":\"list_channels\"}}\n\n",
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n",
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":9}}\n\n",
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
	}
	srv := sseServer(t, events, "")
	defer srv.Close()

	client, _ := New(srv.URL, config.Tokens{AccessToken: "t", RefreshToken: "r"})
	res, err := client.StreamInference(context.Background(), InferenceRequest{
		Model:    "claude-haiku-4-5",
		Messages: []InferenceMessage{{Role: "user", Content: "channels?"}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Blocks) != 1 || res.Blocks[0].Input == nil || len(res.Blocks[0].Input) != 0 {
		t.Fatalf("want empty non-nil input, got %+v", res.Blocks)
	}
}

func TestStreamInferenceProxyError(t *testing.T) {
	events := []string{
		"event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"proxy_error\",\"message\":\"Upstream timed out.\"}}\n\n",
	}
	srv := sseServer(t, events, "")
	defer srv.Close()

	client, _ := New(srv.URL, config.Tokens{AccessToken: "t", RefreshToken: "r"})
	_, err := client.StreamInference(context.Background(), InferenceRequest{
		Model:    "claude-haiku-4-5",
		Messages: []InferenceMessage{{Role: "user", Content: "hi"}},
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "Upstream timed out") {
		t.Fatalf("want proxy error, got %v", err)
	}
}

func TestStreamInferenceGateError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/inference/messages", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error":{"code":"model_plan_tier","message":"claude-opus-4-8 requires a higher plan."}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client, _ := New(srv.URL, config.Tokens{AccessToken: "t", RefreshToken: "r"})
	_, err := client.StreamInference(context.Background(), InferenceRequest{
		Model:    "claude-opus-4-8",
		Messages: []InferenceMessage{{Role: "user", Content: "hi"}},
	}, nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "model_plan_tier" {
		t.Fatalf("want model_plan_tier APIError, got %v", err)
	}
}

func TestContentBlockMarshalShapes(t *testing.T) {
	toolUse, _ := json.Marshal(ContentBlock{Type: "tool_use", ID: "toolu_03", Name: "list_channels"})
	if !strings.Contains(string(toolUse), `"input":{}`) {
		t.Fatalf("tool_use must carry input even when empty: %s", toolUse)
	}
	text, _ := json.Marshal(ContentBlock{Type: "text", Text: "hi"})
	if strings.Contains(string(text), "input") || strings.Contains(string(text), "tool_use_id") {
		t.Fatalf("text block leaked tool fields: %s", text)
	}
	result, _ := json.Marshal(ContentBlock{Type: "tool_result", ToolUseID: "toolu_03", Content: "[]", IsError: true})
	for _, want := range []string{`"tool_use_id":"toolu_03"`, `"is_error":true`, `"content":"[]"`} {
		if !strings.Contains(string(result), want) {
			t.Fatalf("tool_result missing %s: %s", want, result)
		}
	}
}

func TestSearchAndMessageMutations(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/search", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("q") != "billing" || r.URL.Query().Get("channel") != "general" {
			t.Errorf("unexpected query: %s", r.URL.RawQuery)
		}
		w.Write([]byte(`{"data":{"messages":[{"id":7,"channel_id":1,"body":"billing call notes","sender":{"type":"User","id":1,"name":"Alice"},"created_at":"2026-08-01T10:00:00Z","updated_at":"2026-08-01T10:00:00Z","channel":{"slug":"general","name":"General","kind":"public_channel"}}],"tasks":[],"documents":[{"id":3,"slug":"billing-plan","title":"Billing Plan","published":true,"created_at":"2026-08-01T10:00:00Z","updated_at":"2026-08-01T10:00:00Z"}]}}`))
	})
	mux.HandleFunc("PATCH /api/v1/messages/7", func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Message struct {
				Body string `json:"body"`
			} `json:"message"`
		}
		json.NewDecoder(r.Body).Decode(&payload)
		if payload.Message.Body != "edited" {
			t.Errorf("body: %q", payload.Message.Body)
		}
		w.Write([]byte(`{"data":{"id":7,"channel_id":1,"body":"edited","sender":{"type":"User","id":1,"name":"Alice"},"edited_at":"2026-08-04T10:00:00Z","created_at":"2026-08-01T10:00:00Z","updated_at":"2026-08-04T10:00:00Z"}}`))
	})
	mux.HandleFunc("DELETE /api/v1/messages/7", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client, _ := New(srv.URL, config.Tokens{AccessToken: "t", RefreshToken: "r"})
	ctx := context.Background()

	results, err := client.Search(ctx, "billing", SearchOpts{Channel: "general"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results.Messages) != 1 || results.Messages[0].Channel.Slug != "general" {
		t.Fatalf("messages: %+v", results.Messages)
	}
	if len(results.Documents) != 1 || results.Documents[0].Title != "Billing Plan" {
		t.Fatalf("documents: %+v", results.Documents)
	}

	edited, err := client.EditMessage(ctx, 7, "edited")
	if err != nil {
		t.Fatal(err)
	}
	if edited.Body != "edited" || edited.EditedAt == nil {
		t.Fatalf("edited: %+v", edited)
	}

	if err := client.DeleteMessage(ctx, 7); err != nil {
		t.Fatal(err)
	}
}
