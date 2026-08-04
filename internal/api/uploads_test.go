package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jkthorne/saltare/cli/internal/config"
)

// The 401-refresh retry must replay the multipart body — each attempt
// reopens the file, so the second request carries the full content again.
func TestUploadFileReplaysAfterRefresh(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.txt")
	if err := os.WriteFile(path, []byte("quarterly numbers"), 0o600); err != nil {
		t.Fatal(err)
	}

	var attempts int
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/uploads", func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":{"code":"unauthenticated","message":"expired"}}`))
			return
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("attempt %d: %v", attempts, err)
		}
		if got := r.FormValue("title"); got != "Q3 report" {
			t.Errorf("title: %q", got)
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Fatalf("file part: %v", err)
		}
		defer file.Close()
		content, _ := io.ReadAll(file)
		if string(content) != "quarterly numbers" {
			t.Errorf("replayed content: %q", content)
		}
		if header.Filename != "report.txt" {
			t.Errorf("filename: %q", header.Filename)
		}
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"data":{"id":5,"slug":"report-txt","title":"Q3 report","original_filename":"report.txt","content_type":"text/plain","file_size":17,"creator_id":1,"created_at":"2026-08-04T10:00:00Z","updated_at":"2026-08-04T10:00:00Z"}}`))
	})
	mux.HandleFunc("POST /api/v1/auth/refresh", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"token_type":"Bearer","access_token":"fresh","refresh_token":"r2","expires_at":"2027-01-01T00:00:00Z","refresh_expires_at":"2027-06-01T00:00:00Z","workspace":{"id":1,"slug":"test","name":"Test"},"user":{"id":1,"email":"t@example.com","name":"T"},"device":{"id":"d","name":"n","platform":"cli"}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client, _ := New(srv.URL, config.Tokens{AccessToken: "stale", RefreshToken: "r"})
	upload, err := client.UploadFile(context.Background(), path, "Q3 report")
	if err != nil {
		t.Fatal(err)
	}
	if upload.Slug != "report-txt" || attempts != 2 {
		t.Fatalf("upload=%+v attempts=%d", upload, attempts)
	}
}

// The download redirect must reach the storage host WITHOUT the bearer
// token — the URL is presigned, and forwarding Authorization would leak it.
func TestDownloadStripsAuthAcrossHosts(t *testing.T) {
	storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("Authorization must not follow the redirect to storage")
		}
		w.Header().Set("Content-Disposition", `attachment; filename="report.txt"`)
		w.Write([]byte("the payload"))
	}))
	defer storage.Close()

	// Go strips Authorization when the redirect changes *hostname* (ports
	// don't count — a dev same-host disk redirect keeps the header, which
	// is fine). Both httptest servers listen on 127.0.0.1, so address the
	// storage hop as "localhost" to make the hostnames genuinely differ,
	// like the prod Spaces domain does.
	crossHost := strings.Replace(storage.URL, "127.0.0.1", "localhost", 1)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/uploads/report-txt/download", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer t" {
			t.Errorf("api hop must carry the bearer, got %q", r.Header.Get("Authorization"))
		}
		http.Redirect(w, r, crossHost+"/blob?sig=abc", http.StatusFound)
	})
	api := httptest.NewServer(mux)
	defer api.Close()

	client, _ := New(api.URL, config.Tokens{AccessToken: "t", RefreshToken: "r"})
	body, filename, _, err := client.DownloadUpload(context.Background(), "report-txt")
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()

	content, _ := io.ReadAll(body)
	if string(content) != "the payload" {
		t.Fatalf("content: %q", content)
	}
	if filename != "report.txt" {
		t.Fatalf("filename: %q", filename)
	}
}

func TestDatabasesAndRows(t *testing.T) {
	var rowPages []string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/databases", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[{"id":1,"slug":"crm","name":"CRM","rows_count":2,"created_at":"2026-08-01T10:00:00Z","updated_at":"2026-08-01T10:00:00Z"}]}`))
	})
	mux.HandleFunc("GET /api/v1/databases/crm", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":{"id":1,"slug":"crm","name":"CRM","rows_count":2,"schema":{"columns":[{"key":"name","type":"text"},{"key":"deal_size","type":"number"},{"key":"tags","type":"multi_select","options":["hot","cold"]}]},"created_at":"2026-08-01T10:00:00Z","updated_at":"2026-08-01T10:00:00Z"}}`))
	})
	mux.HandleFunc("GET /api/v1/databases/crm/rows", func(w http.ResponseWriter, r *http.Request) {
		rowPages = append(rowPages, r.URL.Query().Get("page"))
		w.Write([]byte(`{"data":[{"id":10,"position":1,"data":{"name":"Acme","deal_size":50000,"tags":["hot"]},"created_at":"2026-08-01T10:00:00Z","updated_at":"2026-08-01T10:00:00Z"}]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client, _ := New(srv.URL, config.Tokens{AccessToken: "t", RefreshToken: "r"})
	ctx := context.Background()

	dbs, err := client.Databases(ctx)
	if err != nil || len(dbs) != 1 || dbs[0].Schema != nil {
		t.Fatalf("Databases: %v %+v (index must omit schema)", err, dbs)
	}

	db, err := client.Database(ctx, "crm")
	if err != nil || db.Schema == nil || len(db.Schema.Columns) != 3 {
		t.Fatalf("Database: %v %+v", err, db)
	}
	if db.Schema.Columns[1].Key != "deal_size" || db.Schema.Columns[2].Options[0] != "hot" {
		t.Fatalf("schema columns out of order: %+v", db.Schema.Columns)
	}

	rows, err := client.DatabaseRows(ctx, "crm", 2, 50)
	if err != nil || len(rows) != 1 {
		t.Fatalf("DatabaseRows: %v %+v", err, rows)
	}
	if rows[0].Data["deal_size"] != float64(50000) {
		t.Fatalf("typed cell: %+v", rows[0].Data)
	}
	if rowPages[0] != "2" {
		t.Fatalf("page param: %v", rowPages)
	}
}

func TestPlanLimitMessagesAreDeHTMLed(t *testing.T) {
	err := (&APIError{Status: 402, Code: "plan_limit", Message: `Upload limit reached. <a href="/w/x/settings/billing" class="link">Upgrade</a> for more.`}).Error()
	if strings.Contains(err, "<a") || strings.Contains(err, "</a>") {
		t.Fatalf("HTML must be stripped: %q", err)
	}
	if !strings.Contains(err, "Upgrade for more.") {
		t.Fatalf("text content must survive: %q", err)
	}
}
