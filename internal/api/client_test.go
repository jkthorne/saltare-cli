package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

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
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{
				"code": "workspace_selection_required",
				"extra": map[string]any{"workspaces": []map[string]any{
					{"id": 1, "slug": "acme", "name": "Acme"},
					{"id": 2, "slug": "beta", "name": "Beta"},
				}},
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
