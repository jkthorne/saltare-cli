package tablefmt

import (
	"testing"

	"github.com/jkthorne/saltare-cli/internal/api"
)

func strPtr(s string) *string { return &s }

func TestRecordsOrdersAndRenders(t *testing.T) {
	columns := []api.DBColumn{
		{Key: "name", Type: "text"},
		{Key: "deal_size", Type: "number"},
		{Key: "tags", Type: "multi_select"},
		{Key: "score", Type: "formula"},
	}
	rows := []api.DBRow{
		{ID: 10, Data: map[string]any{"name": "Acme", "deal_size": float64(50000), "tags": []any{"hot", "q3"}}},
		{ID: 11, Data: map[string]any{"name": "Globex", "deal_size": 2.5}, Body: strPtr("call notes")},
	}

	records := Records(columns, rows)

	wantHeader := []string{"id", "name", "deal_size", "tags", "score", "body"}
	for i, key := range wantHeader {
		if records[0][i] != key {
			t.Fatalf("header: %v", records[0])
		}
	}
	first := records[1]
	if first[0] != "10" || first[1] != "Acme" || first[2] != "50000" || first[3] != "hot,q3" {
		t.Fatalf("row 1: %v", first)
	}
	if first[4] != "" {
		t.Fatalf("formula cells must stay empty, got %q", first[4])
	}
	second := records[2]
	if second[2] != "2.5" || second[5] != "call notes" {
		t.Fatalf("row 2: %v", second)
	}
}

func TestRecordsOmitsBodyColumnWhenUnused(t *testing.T) {
	records := Records([]api.DBColumn{{Key: "name", Type: "text"}}, []api.DBRow{
		{ID: 1, Data: map[string]any{"name": "solo"}},
	})
	if len(records[0]) != 2 {
		t.Fatalf("no body → no body column: %v", records[0])
	}
}

func TestFlatten(t *testing.T) {
	if got := Flatten("multi\nline\tcell"); got != "multi line cell" {
		t.Fatalf("got %q", got)
	}
	if got := Flatten("untouched"); got != "untouched" {
		t.Fatalf("got %q", got)
	}
}

func TestHumanSize(t *testing.T) {
	cases := map[int64]string{
		512:         "512 B",
		2048:        "2.0 KB",
		5 << 20:     "5.0 MB",
		3 << 30:     "3.0 GB",
		1536 * 1024: "1.5 MB",
	}
	for in, want := range cases {
		if got := HumanSize(in); got != want {
			t.Fatalf("HumanSize(%d) = %q, want %q", in, got, want)
		}
	}
}
