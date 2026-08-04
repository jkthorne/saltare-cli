package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jkthorne/saltare/cli/internal/config"
)

func sseServer(t *testing.T, events []string, wantModel string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/inference/messages", func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, 4096)
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
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"salta\"}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"re\"}}\n\n",
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":7}}\n\n",
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
	}
	srv := sseServer(t, events, "claude-haiku-4-5")
	defer srv.Close()

	client, _ := New(srv.URL, config.Tokens{AccessToken: "t", RefreshToken: "r"})
	var deltas []string
	full, usage, err := client.StreamInference(context.Background(), InferenceRequest{
		Model:    "claude-haiku-4-5",
		Messages: []InferenceMessage{{Role: "user", Content: "hi"}},
	}, func(d string) { deltas = append(deltas, d) })

	if err != nil {
		t.Fatal(err)
	}
	if full != "saltare" {
		t.Fatalf("full text: %q", full)
	}
	if len(deltas) != 2 || deltas[0] != "salta" {
		t.Fatalf("deltas: %v", deltas)
	}
	if usage.InputTokens != 25 || usage.OutputTokens != 7 {
		t.Fatalf("usage: %+v", usage)
	}
}

func TestStreamInferenceProxyError(t *testing.T) {
	events := []string{
		"event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"proxy_error\",\"message\":\"Upstream timed out.\"}}\n\n",
	}
	srv := sseServer(t, events, "")
	defer srv.Close()

	client, _ := New(srv.URL, config.Tokens{AccessToken: "t", RefreshToken: "r"})
	_, _, err := client.StreamInference(context.Background(), InferenceRequest{
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
	_, _, err := client.StreamInference(context.Background(), InferenceRequest{
		Model:    "claude-opus-4-8",
		Messages: []InferenceMessage{{Role: "user", Content: "hi"}},
	}, nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "model_plan_tier" {
		t.Fatalf("want model_plan_tier APIError, got %v", err)
	}
}
