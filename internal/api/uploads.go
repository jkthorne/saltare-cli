package api

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Upload mirrors Api::V1::UploadSerializer. Category is computed async
// (CategorizeUploadJob) — it's nil right after create; re-fetch to see it.
type Upload struct {
	ID               int64     `json:"id"`
	Slug             string    `json:"slug"`
	Title            string    `json:"title"`
	OriginalFilename string    `json:"original_filename"`
	ContentType      string    `json:"content_type"`
	Category         *string   `json:"category"`
	FileSize         int64     `json:"file_size"`
	CreatorID        int64     `json:"creator_id"`
	DownloadURL      *string   `json:"download_url"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type UploadsOpts struct {
	Category string // exact category filter
	Query    string // full-text over title/filename/extracted text
}

func (c *Client) Uploads(ctx context.Context, opts UploadsOpts) ([]Upload, error) {
	q := url.Values{"per_page": {"100"}}
	if opts.Category != "" {
		q.Set("category", opts.Category)
	}
	if opts.Query != "" {
		q.Set("q", opts.Query)
	}
	var out struct {
		Data []Upload `json:"data"`
	}
	if err := c.get(ctx, "/api/v1/uploads", q, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

func (c *Client) UploadRecord(ctx context.Context, slug string) (*Upload, error) {
	var out struct {
		Data Upload `json:"data"`
	}
	path := "/api/v1/uploads/" + url.PathEscape(slug)
	if err := c.get(ctx, path, nil, &out); err != nil {
		return nil, err
	}
	return &out.Data, nil
}

func (c *Client) DeleteUpload(ctx context.Context, slug string) error {
	return c.do(ctx, http.MethodDelete, "/api/v1/uploads/"+url.PathEscape(slug), nil, nil, nil)
}

// UploadFile streams a multipart POST /api/v1/uploads (top-level `file`
// part + optional `title`). Each attempt reopens the file — a consumed
// multipart body can't replay through the 401-refresh retry.
func (c *Client) UploadFile(ctx context.Context, path, title string) (*Upload, error) {
	upload, status, err := c.uploadOnce(ctx, path, title, c.AccessToken())
	if status != http.StatusUnauthorized {
		return upload, err
	}
	if err := c.refresh(ctx, c.AccessToken()); err != nil {
		return nil, err
	}
	upload, _, err = c.uploadOnce(ctx, path, title, c.AccessToken())
	return upload, err
}

func (c *Client) uploadOnce(ctx context.Context, path, title, token string) (*Upload, int, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}

	// Pipe the multipart body straight from disk — a large file must not
	// be buffered in RAM. The transport always closes the request body, so
	// an early server response unblocks the writer via ErrClosedPipe.
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		defer file.Close()
		var werr error
		if title != "" {
			werr = mw.WriteField("title", title)
		}
		if werr == nil {
			var part io.Writer
			part, werr = mw.CreateFormFile("file", filepath.Base(path))
			if werr == nil {
				_, werr = io.Copy(part, file)
			}
		}
		if werr == nil {
			werr = mw.Close()
		}
		pw.CloseWithError(werr)
	}()

	u := *c.base
	u.Path = strings.TrimRight(u.Path, "/") + "/api/v1/uploads"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), pr)
	if err != nil {
		pr.Close()
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Accept", "application/json")

	// Large transfers outlive the shared client's 30s timeout; ctx cancels.
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, resp.StatusCode, nil
	}
	if resp.StatusCode >= 400 {
		return nil, resp.StatusCode, decodeError(resp)
	}
	var out struct {
		Data Upload `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, resp.StatusCode, err
	}
	return &out.Data, resp.StatusCode, nil
}

// DownloadUpload opens the authenticated download and follows the redirect
// to the presigned storage URL. Go's default redirect policy strips
// Authorization on the cross-host hop — exactly right (the storage URL is
// presigned; forwarding the bearer token would leak it) — so no custom
// CheckRedirect. The caller closes the returned body.
func (c *Client) DownloadUpload(ctx context.Context, slug string) (io.ReadCloser, string, int64, error) {
	body, filename, size, status, err := c.downloadOnce(ctx, slug, c.AccessToken())
	if status != http.StatusUnauthorized {
		return body, filename, size, err
	}
	if err := c.refresh(ctx, c.AccessToken()); err != nil {
		return nil, "", 0, err
	}
	body, filename, size, _, err = c.downloadOnce(ctx, slug, c.AccessToken())
	return body, filename, size, err
}

func (c *Client) downloadOnce(ctx context.Context, slug, token string) (io.ReadCloser, string, int64, int, error) {
	u := *c.base
	u.Path = strings.TrimRight(u.Path, "/") + "/api/v1/uploads/" + url.PathEscape(slug) + "/download"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, "", 0, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	// Timeout-free for the same reason as uploads; ctx cancels.
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return nil, "", 0, 0, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		return nil, "", 0, resp.StatusCode, nil
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		return nil, "", 0, resp.StatusCode, decodeError(resp)
	}

	filename := ""
	if _, params, err := mime.ParseMediaType(resp.Header.Get("Content-Disposition")); err == nil {
		filename = params["filename"]
	}
	return resp.Body, filename, resp.ContentLength, resp.StatusCode, nil
}
