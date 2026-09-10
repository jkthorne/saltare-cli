// Package api is a hand-rolled typed client for /api/v1. It owns the device
// session tokens: every call sends the access token, a 401 triggers one
// refresh-token rotation (single-flight) and one retry, and rotated tokens
// are handed to OnTokens for persistence.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jkthorne/saltare-cli/internal/config"
)

// ErrAuthExpired means both the access and refresh tokens are dead —
// the only fix is `sal login`.
var ErrAuthExpired = errors.New("session expired; run `sal login`")

// APIError is any non-2xx JSON error envelope: {error: {code, message}}.
type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string {
	if e.Message != "" {
		// Scope widenings only reach sessions minted after them — the fix
		// for a missing scope is always a fresh login.
		if e.Code == "missing_scope" {
			return fmt.Sprintf("%s (%s) — this session predates the capability; re-run `sal login`", e.Message, e.Code)
		}
		message := e.Message
		// plan_limit messages embed an upgrade <a> for the web UI.
		if e.Code == "plan_limit" {
			message = strings.Join(strings.Fields(htmlTagPattern.ReplaceAllString(message, " ")), " ")
		}
		return fmt.Sprintf("%s (%s)", message, e.Code)
	}
	return fmt.Sprintf("api error: HTTP %d", e.Status)
}

var htmlTagPattern = regexp.MustCompile(`<[^>]*>`)

type Client struct {
	base *url.URL
	http *http.Client

	mu       sync.Mutex
	tokens   config.Tokens
	OnTokens func(config.Tokens) // called after a successful refresh rotation
}

func New(serverURL string, tokens config.Tokens) (*Client, error) {
	base, err := url.Parse(strings.TrimRight(serverURL, "/"))
	if err != nil || base.Host == "" {
		return nil, fmt.Errorf("invalid server url %q", serverURL)
	}
	return &Client{
		base:   base,
		http:   &http.Client{Timeout: 30 * time.Second},
		tokens: tokens,
	}, nil
}

func (c *Client) BaseURL() *url.URL { return c.base }

func (c *Client) AccessToken() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.tokens.AccessToken
}

func (c *Client) get(ctx context.Context, path string, query url.Values, out any) error {
	return c.do(ctx, http.MethodGet, path, query, nil, out)
}

func (c *Client) post(ctx context.Context, path string, body, out any) error {
	return c.do(ctx, http.MethodPost, path, nil, body, out)
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	token := c.AccessToken()
	status, err := c.doOnce(ctx, method, path, query, body, out, token)
	if err != nil || status != http.StatusUnauthorized {
		return err
	}
	if err := c.refresh(ctx, token); err != nil {
		return err
	}
	_, err = c.doOnce(ctx, method, path, query, body, out, c.AccessToken())
	return err
}

// doOnce returns (status, nil) only mid-flow for 401 (so do can refresh);
// every other non-2xx becomes an *APIError.
func (c *Client) doOnce(ctx context.Context, method, path string, query url.Values, body, out any, token string) (int, error) {
	u := *c.base
	u.Path = strings.TrimRight(u.Path, "/") + path
	if query != nil {
		u.RawQuery = query.Encode()
	}

	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		reader = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(ctx, method, u.String(), reader)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return resp.StatusCode, nil
	}
	if resp.StatusCode >= 400 {
		return resp.StatusCode, decodeError(resp)
	}
	if out == nil {
		return resp.StatusCode, nil
	}
	return resp.StatusCode, json.NewDecoder(resp.Body).Decode(out)
}

// refresh rotates the device session. staleToken is the access token the
// caller saw the 401 with — if another goroutine already rotated past it,
// the refresh is skipped (single-flight).
func (c *Client) refresh(ctx context.Context, staleToken string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.tokens.AccessToken != staleToken {
		return nil // someone else refreshed while we waited on the lock
	}
	if c.tokens.RefreshToken == "" {
		return ErrAuthExpired
	}

	session, err := postRefresh(ctx, c.http, c.base, c.tokens.RefreshToken)
	if err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusUnauthorized {
			return ErrAuthExpired
		}
		return err
	}

	c.tokens = config.Tokens{
		AccessToken:      session.AccessToken,
		RefreshToken:     session.RefreshToken,
		AccessExpiresAt:  session.ExpiresAt,
		RefreshExpiresAt: session.RefreshExpiresAt,
	}
	if c.OnTokens != nil {
		c.OnTokens(c.tokens)
	}
	return nil
}

func decodeError(resp *http.Response) error {
	apiErr := &APIError{Status: resp.StatusCode}
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err == nil {
		apiErr.Code = envelope.Error.Code
		apiErr.Message = envelope.Error.Message
	}
	return apiErr
}
