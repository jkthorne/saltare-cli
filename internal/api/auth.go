package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// WorkspaceChoice is one entry of the 409 workspace_selection_required list.
type WorkspaceChoice struct {
	ID   int64  `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// WorkspaceSelectionError: the account belongs to several workspaces — retry
// Login with one of Choices' slugs.
type WorkspaceSelectionError struct {
	Choices []WorkspaceChoice
}

func (e *WorkspaceSelectionError) Error() string {
	return "choose a workspace and retry with its slug"
}

type LoginParams struct {
	ServerURL     string
	Email         string
	Password      string
	WorkspaceSlug string // optional; required after a WorkspaceSelectionError
	DeviceID      string
	DeviceName    string
}

// Login runs POST /api/v1/auth/token. It is a package function, not a Client
// method, because no tokens exist yet.
func Login(ctx context.Context, p LoginParams) (*DeviceSession, error) {
	base, err := url.Parse(strings.TrimRight(p.ServerURL, "/"))
	if err != nil || base.Host == "" {
		return nil, fmt.Errorf("invalid server url %q", p.ServerURL)
	}

	body := map[string]string{
		"email_address": p.Email,
		"password":      p.Password,
		"device_id":     p.DeviceID,
		"device_name":   p.DeviceName,
		"platform":      "cli",
	}
	if p.WorkspaceSlug != "" {
		body["workspace_slug"] = p.WorkspaceSlug
	}

	httpc := &http.Client{Timeout: 30 * time.Second}
	resp, err := postJSON(ctx, httpc, base, "/api/v1/auth/token", body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusConflict {
		var envelope struct {
			Error struct {
				Extra struct {
					Workspaces []WorkspaceChoice `json:"workspaces"`
				} `json:"extra"`
			} `json:"error"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
			return nil, err
		}
		return nil, &WorkspaceSelectionError{Choices: envelope.Error.Extra.Workspaces}
	}
	if resp.StatusCode >= 400 {
		return nil, decodeError(resp)
	}

	var session DeviceSession
	if err := json.NewDecoder(resp.Body).Decode(&session); err != nil {
		return nil, err
	}
	return &session, nil
}

// postRefresh rotates a device session; used by Client.refresh and shares its
// transport so tests can stub one server.
func postRefresh(ctx context.Context, httpc *http.Client, base *url.URL, refreshToken string) (*DeviceSession, error) {
	resp, err := postJSON(ctx, httpc, base, "/api/v1/auth/refresh", map[string]string{"refresh_token": refreshToken})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, decodeError(resp)
	}
	var session DeviceSession
	if err := json.NewDecoder(resp.Body).Decode(&session); err != nil {
		return nil, err
	}
	return &session, nil
}

// SignOut revokes the device session server-side (DELETE /api/v1/auth/token).
func (c *Client) SignOut(ctx context.Context) error {
	return c.do(ctx, http.MethodDelete, "/api/v1/auth/token", nil, nil, nil)
}

func (c *Client) Me(ctx context.Context) (*Me, error) {
	var me Me
	if err := c.get(ctx, "/api/v1/me", nil, &me); err != nil {
		return nil, err
	}
	return &me, nil
}

func postJSON(ctx context.Context, httpc *http.Client, base *url.URL, path string, body any) (*http.Response, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	u := *base
	u.Path = strings.TrimRight(u.Path, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	return httpc.Do(req)
}
