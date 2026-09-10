// Package tablefmt renders database rows and byte sizes for both the CLI
// (sal db rows) and the TUI grid — one formatter, one set of rules.
package tablefmt

import (
	"fmt"
	"strings"

	"github.com/jkthorne/saltare-cli/internal/api"
)

// Records lays a table out: a header of column keys (id first, body last
// when any row carries one) then one record per row. Cells render scalars
// verbatim and arrays comma-joined; formula columns never appear in row
// data, so their cells stay empty.
func Records(columns []api.DBColumn, rows []api.DBRow) [][]string {
	hasBody := hasBodyColumn(rows)

	records := [][]string{headerRow(columns, rows, keyOf)}
	for _, row := range rows {
		record := []string{fmt.Sprintf("%d", row.ID)}
		for _, col := range columns {
			record = append(record, RenderCell(row.Data[col.Key]))
		}
		if hasBody {
			body := ""
			if row.Body != nil {
				body = *row.Body
			}
			record = append(record, body)
		}
		records = append(records, record)
	}
	return records
}

// Headings is Records' header row with each column's human name in place of
// its key. Only the TUI uses it — a person reading a grid wants "Deal Size",
// while the TSV and CSV `sal db rows` prints are parsed by scripts, so their
// header has to stay the stable key.
func Headings(columns []api.DBColumn, rows []api.DBRow) []string {
	return headerRow(columns, rows, api.DBColumn.Label)
}

func keyOf(col api.DBColumn) string { return col.Key }

// headerRow is the shared layout: "id", one heading per column, then "body"
// when any row carries one. Only the vocabulary differs between callers.
func headerRow(columns []api.DBColumn, rows []api.DBRow, heading func(api.DBColumn) string) []string {
	header := []string{"id"}
	for _, col := range columns {
		header = append(header, heading(col))
	}
	if hasBodyColumn(rows) {
		header = append(header, "body")
	}
	return header
}

func hasBodyColumn(rows []api.DBRow) bool {
	for _, row := range rows {
		if row.Body != nil && *row.Body != "" {
			return true
		}
	}
	return false
}

// RenderCell turns a raw JSON cell value into display text.
func RenderCell(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case []any:
		parts := make([]string, len(v))
		for i, item := range v {
			parts[i] = fmt.Sprintf("%v", item)
		}
		return strings.Join(parts, ",")
	case float64:
		// JSON numbers decode as float64; render integers without ".0".
		if v == float64(int64(v)) {
			return fmt.Sprintf("%d", int64(v))
		}
		return fmt.Sprintf("%v", v)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// Flatten keeps TSV rows one-line and tab-safe.
func Flatten(s string) string {
	if !strings.ContainsAny(s, "\t\n\r") {
		return s
	}
	return strings.Join(strings.Fields(s), " ")
}

// HumanSize renders a byte count the way a human wants to read it.
func HumanSize(bytes int64) string {
	switch {
	case bytes >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(bytes)/(1<<30))
	case bytes >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(bytes)/(1<<20))
	case bytes >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(bytes)/(1<<10))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}
