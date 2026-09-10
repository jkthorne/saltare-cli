package assist

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jkthorne/saltare-cli/internal/api"
	"github.com/jkthorne/saltare-cli/internal/config"
)

func testClient(t *testing.T, srv *httptest.Server) *api.Client {
	t.Helper()
	client, err := api.New(srv.URL, config.Tokens{AccessToken: "t", RefreshToken: "r"})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestTrimTurnsKeepsToolPairsTogether(t *testing.T) {
	turns := []api.InferenceMessage{
		api.TextMessage("user", "a"),
		api.BlocksMessage("assistant", []api.ContentBlock{{Type: "tool_use", ID: "1", Name: "x"}}),
		api.BlocksMessage("user", []api.ContentBlock{{Type: "tool_result", ToolUseID: "1", Content: "[]"}}),
		api.TextMessage("assistant", "answer"),
		api.TextMessage("user", "b"),
		api.TextMessage("assistant", "c"),
	}

	trimmed := TrimTurns(turns, 4)
	// A naive cut of 2 would open on the tool_result turn; the window must
	// advance to the next plain user turn instead.
	if len(trimmed) != 2 || trimmed[0].TextContent() != "b" {
		t.Fatalf("trimmed: %+v", trimmed)
	}

	if got := TrimTurns(turns, 10); len(got) != len(turns) {
		t.Fatalf("under-limit history must be untouched, got %d turns", len(got))
	}
}

func TestExecutorSearchAndCreateTask(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/search", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("q") != "billing" {
			t.Errorf("q: %s", r.URL.RawQuery)
		}
		w.Write([]byte(`{"data":{"messages":[{"id":9,"channel_id":1,"body":"billing sync notes","sender":{"type":"User","id":1,"name":"Alice"},"created_at":"2026-08-01T10:00:00Z","updated_at":"2026-08-01T10:00:00Z","channel":{"slug":"general","name":"General","kind":"public_channel"}}],"tasks":[],"documents":[]}}`))
	})
	mux.HandleFunc("GET /api/v1/projects", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[{"id":4,"slug":"backlog","name":"Backlog","active_tasks_count":2,"archived":false},{"id":5,"slug":"sprint","name":"Current Sprint","active_tasks_count":1,"archived":false}]}`))
	})
	mux.HandleFunc("POST /api/v1/tasks", func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Task map[string]any `json:"task"`
		}
		json.NewDecoder(r.Body).Decode(&payload)
		if payload.Task["project_id"].(float64) != 5 {
			t.Errorf("project_id: %v", payload.Task["project_id"])
		}
		assignee, _ := payload.Task["assignee"].(map[string]any)
		if assignee["id"].(float64) != 42 {
			t.Errorf("assignee: %v", payload.Task["assignee"])
		}
		w.Write([]byte(`{"data":{"id":10,"slug":"ship-it-abc123","title":"Ship it","state":"open","project_id":5,"created_at":"2026-08-04T10:00:00Z","updated_at":"2026-08-04T10:00:00Z"}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	exec := &Executor{Client: testClient(t, srv), UserID: 42}
	ctx := context.Background()

	result := exec.Execute(ctx, api.ContentBlock{Type: "tool_use", ID: "t1", Name: "search_workspace", Input: map[string]any{"q": "billing"}})
	if result.IsError || !strings.Contains(result.Content, "billing sync notes") {
		t.Fatalf("search result: %+v", result)
	}
	if result.ToolUseID != "t1" {
		t.Fatalf("tool_use_id: %q", result.ToolUseID)
	}

	// Project resolves by case-insensitive name.
	result = exec.Execute(ctx, api.ContentBlock{Type: "tool_use", ID: "t2", Name: "create_task", Input: map[string]any{"title": "Ship it", "project": "current sprint"}})
	if result.IsError || !strings.Contains(result.Content, "ship-it-abc123") {
		t.Fatalf("create result: %+v", result)
	}

	// Ambiguous project comes back as a recoverable tool error.
	result = exec.Execute(ctx, api.ContentBlock{Type: "tool_use", ID: "t3", Name: "create_task", Input: map[string]any{"title": "No project"}})
	if !result.IsError || !strings.Contains(result.Content, "backlog, sprint") {
		t.Fatalf("ambiguous project: %+v", result)
	}

	result = exec.Execute(ctx, api.ContentBlock{Type: "tool_use", ID: "t4", Name: "no_such_tool"})
	if !result.IsError {
		t.Fatalf("unknown tool must be an error result: %+v", result)
	}
}

const toolUseSSE = "event: content_block_start\n" +
	"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_x\",\"name\":\"list_channels\"}}\n\n" +
	"data: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
	"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":5}}\n\n" +
	"data: {\"type\":\"message_stop\"}\n\n"

const endTurnSSE = "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
	"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"done\"}}\n\n" +
	"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":2}}\n\n" +
	"data: {\"type\":\"message_stop\"}\n\n"

func TestRunLoopCapsIterationsThenForcesText(t *testing.T) {
	var inferenceCalls, toolCalls int
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/inference/messages", func(w http.ResponseWriter, r *http.Request) {
		inferenceCalls++
		raw := make([]byte, 256*1024)
		n, _ := r.Body.Read(raw)
		w.Header().Set("Content-Type", "text/event-stream")
		if strings.Contains(string(raw[:n]), `"tools"`) {
			w.Write([]byte(toolUseSSE))
		} else {
			w.Write([]byte(endTurnSSE))
		}
	})
	mux.HandleFunc("GET /api/v1/channels", func(w http.ResponseWriter, r *http.Request) {
		toolCalls++
		w.Write([]byte(`{"data":[]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := testClient(t, srv)
	exec := &Executor{Client: client, UserID: 1}

	var traces []string
	turns, res, err := RunLoop(context.Background(), client, exec,
		[]api.InferenceMessage{api.TextMessage("user", "loop forever")},
		LoopOpts{
			Model:  "claude-haiku-4-5",
			Tools:  Tools(),
			OnTool: func(name string, _ map[string]any) { traces = append(traces, name) },
		})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "done" || res.StopReason != "end_turn" {
		t.Fatalf("final result: %+v", res)
	}
	// 8 tool rounds with tools + 1 forced no-tools call.
	if inferenceCalls != maxToolIterations+1 {
		t.Fatalf("inference calls: %d", inferenceCalls)
	}
	if toolCalls != maxToolIterations || len(traces) != maxToolIterations {
		t.Fatalf("tool executions: %d, traces: %d", toolCalls, len(traces))
	}
	// History: question + 8×(assistant tool_use + user tool_result) + final answer.
	if len(turns) != 1+2*maxToolIterations+1 {
		t.Fatalf("turns: %d", len(turns))
	}
	if res.Usage.OutputTokens != 5*maxToolIterations+2 {
		t.Fatalf("total usage must sum every hop: %+v", res.Usage)
	}
}
