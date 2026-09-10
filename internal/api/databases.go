package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Database mirrors Api::V1::DatabaseSerializer. The response key for the
// column layout is `schema` (only present on show, not index) even though
// the write param is `schema_definition` — server asymmetry.
type Database struct {
	ID        int64     `json:"id"`
	Slug      string    `json:"slug"`
	Name      string    `json:"name"`
	RowsCount int       `json:"rows_count"`
	Schema    *DBSchema `json:"schema"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// DBSchema is the schema_definition jsonb; extra keys are ignored.
type DBSchema struct {
	Columns []DBColumn `json:"columns"`
}

type DBColumn struct {
	Key  string `json:"key"`
	Type string `json:"type"`
	// Name is the column's human label. The schema jsonb calls it `name`;
	// this struct read `label` until the golden test caught it, which is why
	// the grid has always headed columns with their keys.
	Name    string   `json:"name"`
	Options []string `json:"options"`
}

// DBRow mirrors Api::V1::RowSerializer. Data is the raw cell hash keyed by
// column key; formula columns are computed on read and never appear.
type DBRow struct {
	ID        int64          `json:"id"`
	Position  int            `json:"position"`
	Data      map[string]any `json:"data"`
	Body      *string        `json:"body"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

func (c *Client) Databases(ctx context.Context) ([]Database, error) {
	q := url.Values{"per_page": {"100"}}
	var out struct {
		Data []Database `json:"data"`
	}
	if err := c.get(ctx, "/api/v1/databases", q, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

func (c *Client) Database(ctx context.Context, slug string) (*Database, error) {
	var out struct {
		Data Database `json:"data"`
	}
	path := "/api/v1/databases/" + url.PathEscape(slug)
	if err := c.get(ctx, path, nil, &out); err != nil {
		return nil, err
	}
	return &out.Data, nil
}

// UpdateRowData rewrites a row's cell hash. The server replaces `data`
// wholesale (not a merge) and coerces string values per column type, so
// callers read-modify-write the full map and may send plain strings.
func (c *Client) UpdateRowData(ctx context.Context, dbSlug string, rowID int64, data map[string]any) (*DBRow, error) {
	payload := map[string]any{"row": map[string]any{"data": data}}
	var out struct {
		Data DBRow `json:"data"`
	}
	path := fmt.Sprintf("/api/v1/databases/%s/rows/%d", url.PathEscape(dbSlug), rowID)
	if err := c.do(ctx, http.MethodPatch, path, nil, payload, &out); err != nil {
		return nil, err
	}
	return &out.Data, nil
}

// DatabaseRows returns one page in the table's position order.
func (c *Client) DatabaseRows(ctx context.Context, slug string, page, perPage int) ([]DBRow, error) {
	if page == 0 {
		page = 1
	}
	if perPage == 0 {
		perPage = 100
	}
	q := url.Values{
		"page":     {strconv.Itoa(page)},
		"per_page": {strconv.Itoa(perPage)},
	}
	var out struct {
		Data []DBRow `json:"data"`
	}
	path := "/api/v1/databases/" + url.PathEscape(slug) + "/rows"
	if err := c.get(ctx, path, q, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}
