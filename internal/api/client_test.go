package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jkthorne/saltare/cli/internal/config"
)

// TestRefreshOn401RetriesOnce: a stale access token gets one refresh rotation
// and one retry; rotated tokens reach OnTokens.
func TestRefreshOn401RetriesOnce(t *testing.T) {
	var refreshes int
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/me", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fresh-token" {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": "unauthenticated", "message": "nope"}})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"workspace": map[string]any{"slug": "acme"}, "user": map[string]any{"email": "a@b.c"}})
	})
	mux.HandleFunc("POST /api/v1/auth/refresh", func(w http.ResponseWriter, r *http.Request) {
		refreshes++
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		if body["refresh_token"] != "rt-old" {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": "invalid_refresh_token", "message": "bad"}})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"access_token": "fresh-token", "refresh_token": "rt-new"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client, err := New(srv.URL, config.Tokens{AccessToken: "stale-token", RefreshToken: "rt-old"})
	if err != nil {
		t.Fatal(err)
	}
	var persisted config.Tokens
	client.OnTokens = func(tk config.Tokens) { persisted = tk }

	me, err := client.Me(context.Background())
	if err != nil {
		t.Fatalf("Me after refresh: %v", err)
	}
	if me.Workspace.Slug != "acme" {
		t.Fatalf("unexpected payload: %+v", me)
	}
	if refreshes != 1 {
		t.Fatalf("want exactly 1 refresh, got %d", refreshes)
	}
	if persisted.AccessToken != "fresh-token" || persisted.RefreshToken != "rt-new" {
		t.Fatalf("rotated tokens not persisted: %+v", persisted)
	}
}

// TestDeadRefreshTokenSurfacesErrAuthExpired: when the refresh also 401s, the
// caller gets the run-sal-login sentinel, not a raw HTTP error.
func TestDeadRefreshTokenSurfacesErrAuthExpired(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/me", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": "unauthenticated", "message": "nope"}})
	})
	mux.HandleFunc("POST /api/v1/auth/refresh", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": "invalid_refresh_token", "message": "bad"}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client, _ := New(srv.URL, config.Tokens{AccessToken: "stale", RefreshToken: "rt-dead"})
	_, err := client.Me(context.Background())
	if !errors.Is(err, ErrAuthExpired) {
		t.Fatalf("want ErrAuthExpired, got %v", err)
	}
}

// TestLoginWorkspaceSelection: 409 becomes a WorkspaceSelectionError carrying
// the server's choices.
func TestLoginWorkspaceSelection(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/auth/token", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		if body["platform"] != "cli" {
			t.Errorf("want platform cli, got %q", body["platform"])
		}
		if body["workspace_slug"] == "" {
			// Mirrors render_api_error's real shape: extra keys are merged
			// into the error object (error.workspaces, not error.extra.*).
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{
				"code":    "workspace_selection_required",
				"message": "Choose a workspace to sign into, then retry with workspace_slug.",
				"workspaces": []map[string]any{
					{"id": 1, "slug": "acme", "name": "Acme"},
					{"id": 2, "slug": "beta", "name": "Beta"},
				},
			}})
			return
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "at",
			"refresh_token": "rt",
			"workspace":     map[string]any{"slug": body["workspace_slug"]},
			"user":          map[string]any{"email": body["email_address"]},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	params := LoginParams{ServerURL: srv.URL, Email: "a@b.c", Password: "pw", DeviceID: "d1", DeviceName: "test"}
	_, err := Login(context.Background(), params)
	var choice *WorkspaceSelectionError
	if !errors.As(err, &choice) {
		t.Fatalf("want WorkspaceSelectionError, got %v", err)
	}
	if len(choice.Choices) != 2 || choice.Choices[1].Slug != "beta" {
		t.Fatalf("unexpected choices: %+v", choice.Choices)
	}

	params.WorkspaceSlug = "beta"
	sess, err := Login(context.Background(), params)
	if err != nil {
		t.Fatalf("retry with slug: %v", err)
	}
	if sess.Workspace.Slug != "beta" || sess.AccessToken != "at" {
		t.Fatalf("unexpected session: %+v", sess)
	}
}

// TestLoginRejectsEmptyWorkspaceList: a 409 with no choices is an explicit
// error, never an empty picker.
func TestLoginRejectsEmptyWorkspaceList(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/auth/token", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": "workspace_selection_required"}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	_, err := Login(context.Background(), LoginParams{ServerURL: srv.URL, Email: "a@b.c", Password: "pw"})
	var choice *WorkspaceSelectionError
	if errors.As(err, &choice) {
		t.Fatal("empty choice list must not become a WorkspaceSelectionError")
	}
	if err == nil || !strings.Contains(err.Error(), "no choices") {
		t.Fatalf("want explicit no-choices error, got %v", err)
	}
}

// TestPhase2Resources: SendMessage, MessageAgent, and Mentionables round-trip
// their payloads correctly.
func TestPhase2Resources(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ChannelSlug string `json:"channel_slug"`
			Message     struct {
				Body string `json:"body"`
			} `json:"message"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if body.ChannelSlug != "general" || body.Message.Body != "hi" {
			t.Errorf("unexpected send payload: %+v", body)
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"id": 9, "channel_id": 1, "body": "hi"}})
	})
	mux.HandleFunc("POST /api/v1/agents/researcher/message", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]any{
			"data":       map[string]any{"id": 12, "channel_id": 77, "body": "hello agent"},
			"channel_id": 77,
		})
	})
	mux.HandleFunc("GET /api/v1/mentionables", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{
			{"type": "user", "id": 1, "name": "Alice"},
			{"type": "agent", "id": 2, "name": "Researcher", "slug": "researcher"},
		}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client, _ := New(srv.URL, config.Tokens{AccessToken: "t", RefreshToken: "r"})
	ctx := context.Background()

	msg, err := client.SendMessage(ctx, "general", "hi")
	if err != nil || msg.ID != 9 {
		t.Fatalf("SendMessage: %v %+v", err, msg)
	}

	agentMsg, channelID, err := client.MessageAgent(ctx, "researcher", "hello agent")
	if err != nil || channelID != 77 || agentMsg.ID != 12 {
		t.Fatalf("MessageAgent: %v channel=%d %+v", err, channelID, agentMsg)
	}

	mentionables, err := client.Mentionables(ctx)
	if err != nil || len(mentionables) != 2 || mentionables[1].Slug != "researcher" {
		t.Fatalf("Mentionables: %v %+v", err, mentionables)
	}
}

func TestPhase6ResourceMethods(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/tasks/fix-login-abc", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":{"id":5,"slug":"fix-login-abc","title":"Fix login","state":"open","project_id":1,"subtasks_count":2,"discussion_channel_slug":"task-fix-login-abc","creator_id":7,"created_at":"2026-08-01T10:00:00Z","updated_at":"2026-08-01T10:00:00Z"}}`))
	})
	mux.HandleFunc("POST /api/v1/tasks/fix-login-abc/discussion", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":{"id":40,"slug":"task-fix-login-abc","name":"task-fix-login-abc","kind":"discussion","member":true,"created_at":"2026-08-01T10:00:00Z","updated_at":"2026-08-01T10:00:00Z"}}`))
	})
	mux.HandleFunc("GET /api/v1/channels/task-fix-login-abc", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":{"id":40,"slug":"task-fix-login-abc","name":"task-fix-login-abc","kind":"discussion","created_at":"2026-08-01T10:00:00Z","updated_at":"2026-08-01T10:00:00Z"}}`))
	})
	mux.HandleFunc("GET /api/v1/messages/91", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":{"id":91,"channel_id":40,"body":"event","sender":{"type":"User","id":7,"name":"Me"},"system_event":"state_changed","metadata":{"from":"open","to":"in_progress"},"created_at":"2026-08-01T10:00:00Z","updated_at":"2026-08-01T10:00:00Z"}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client, _ := New(srv.URL, config.Tokens{AccessToken: "t", RefreshToken: "r"})
	ctx := context.Background()

	task, err := client.Task(ctx, "fix-login-abc")
	if err != nil || task.SubtasksCount != 2 || *task.DiscussionChannelSlug != "task-fix-login-abc" {
		t.Fatalf("Task: %v %+v", err, task)
	}
	channel, err := client.TaskDiscussion(ctx, "fix-login-abc")
	if err != nil || channel.Kind != "discussion" || !channel.Member {
		t.Fatalf("TaskDiscussion: %v %+v", err, channel)
	}
	bySlug, err := client.Channel(ctx, "task-fix-login-abc")
	if err != nil || bySlug.ID != 40 {
		t.Fatalf("Channel: %v %+v", err, bySlug)
	}
	msg, err := client.MessageByID(ctx, 91)
	if err != nil || msg.Metadata["to"] != "in_progress" {
		t.Fatalf("MessageByID: %v %+v", err, msg)
	}
}

func TestDocumentWriteMethods(t *testing.T) {
	var updateCalls int
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/documents", func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Document map[string]string `json:"document"`
		}
		json.NewDecoder(r.Body).Decode(&payload)
		if payload.Document["title"] != "Notes" {
			t.Errorf("title: %+v", payload.Document)
		}
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"data":{"id":3,"slug":"notes","title":"Notes","body":"hi","published":false,"created_at":"2026-08-01T10:00:00Z","updated_at":"2026-08-01T10:00:00Z"}}`))
	})
	mux.HandleFunc("PATCH /api/v1/documents/notes", func(w http.ResponseWriter, r *http.Request) {
		updateCalls++
		var payload struct {
			Document      map[string]string `json:"document"`
			BaseUpdatedAt string            `json:"base_updated_at"`
		}
		json.NewDecoder(r.Body).Decode(&payload)
		if updateCalls == 1 {
			if payload.BaseUpdatedAt == "" {
				t.Error("first update must carry base_updated_at")
			}
			w.WriteHeader(http.StatusConflict)
			w.Write([]byte(`{"error":{"code":"stale_document","message":"Document changed since you fetched it."}}`))
			return
		}
		if payload.BaseUpdatedAt != "" {
			t.Error("forced update must omit base_updated_at")
		}
		w.Write([]byte(`{"data":{"id":3,"slug":"notes","title":"Notes","body":"forced","published":false,"created_at":"2026-08-01T10:00:00Z","updated_at":"2026-08-02T10:00:00Z"}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client, _ := New(srv.URL, config.Tokens{AccessToken: "t", RefreshToken: "r"})
	ctx := context.Background()

	doc, err := client.CreateDocument(ctx, "Notes", "hi")
	if err != nil || doc.Slug != "notes" {
		t.Fatalf("CreateDocument: %v %+v", err, doc)
	}

	_, err = client.UpdateDocumentBody(ctx, "notes", "late edit", doc.UpdatedAt)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "stale_document" {
		t.Fatalf("want stale_document conflict, got %v", err)
	}

	forced, err := client.UpdateDocumentBody(ctx, "notes", "forced", time.Time{})
	if err != nil || forced.Body != "forced" {
		t.Fatalf("forced update: %v %+v", err, forced)
	}
}

func TestMissingScopeHintsRelogin(t *testing.T) {
	err := (&APIError{Status: 403, Code: "missing_scope", Message: "Missing required scope"}).Error()
	if !strings.Contains(err, "re-run `sal login`") {
		t.Fatalf("missing_scope must hint a re-login: %q", err)
	}
}
