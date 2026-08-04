package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/jkthorne/saltare/cli/internal/api"
)

func runDB(args []string) error {
	if len(args) > 0 && args[0] == "rows" {
		return dbRows(args[1:])
	}

	fs := flag.NewFlagSet("db", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "print raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	_, client, err := session()
	if err != nil {
		return err
	}
	ctx, cancel := interruptContext()
	defer cancel()

	databases, err := client.Databases(ctx)
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(databases)
	}
	for _, db := range databases {
		fmt.Printf("%-28s %6d rows  %s\n", db.Slug, db.RowsCount, db.Name)
	}
	return nil
}

func dbRows(args []string) error {
	fs := flag.NewFlagSet("db rows", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "print raw JSON rows")
	asCSV := fs.Bool("csv", false, "print CSV instead of TSV")
	limit := fs.Int("limit", 0, "stop after N rows (default: every row)")
	slug, err := slugAndFlags(fs, args, "sal db rows SLUG [--csv|--json] [--limit N]")
	if err != nil {
		return err
	}

	_, client, err := session()
	if err != nil {
		return err
	}
	ctx, cancel := interruptContext()
	defer cancel()

	database, err := client.Database(ctx, slug)
	if err != nil {
		return err
	}
	if database.Schema == nil || len(database.Schema.Columns) == 0 {
		return fmt.Errorf("%s has no columns yet — add some on the web first", slug)
	}

	// Page everything: scripting wants the whole table. A short page means
	// we've hit the end.
	const perPage = 100
	var rows []api.DBRow
	for page := 1; ; page++ {
		batch, err := client.DatabaseRows(ctx, slug, page, perPage)
		if err != nil {
			return err
		}
		rows = append(rows, batch...)
		if *limit > 0 && len(rows) >= *limit {
			rows = rows[:*limit]
			break
		}
		if len(batch) < perPage {
			break
		}
	}

	if *asJSON {
		return printJSON(rows)
	}

	records := formatRows(database.Schema.Columns, rows)
	if *asCSV {
		w := csv.NewWriter(os.Stdout)
		if err := w.WriteAll(records); err != nil {
			return err
		}
		w.Flush()
		return w.Error()
	}
	for _, record := range records {
		for i, cell := range record {
			record[i] = flattenWhitespace(cell)
		}
		fmt.Println(strings.Join(record, "\t"))
	}
	return nil
}

// formatRows lays a table out as records: a header of column keys (id
// first, body last when any row carries one) then one record per row.
// Cells render scalars verbatim and arrays comma-joined; formula columns
// never appear in row data, so their cells stay empty.
func formatRows(columns []api.DBColumn, rows []api.DBRow) [][]string {
	hasBody := false
	for _, row := range rows {
		if row.Body != nil && *row.Body != "" {
			hasBody = true
			break
		}
	}

	header := []string{"id"}
	for _, col := range columns {
		header = append(header, col.Key)
	}
	if hasBody {
		header = append(header, "body")
	}

	records := [][]string{header}
	for _, row := range rows {
		record := []string{fmt.Sprintf("%d", row.ID)}
		for _, col := range columns {
			record = append(record, renderCell(row.Data[col.Key]))
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

func renderCell(value any) string {
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

// flattenWhitespace keeps TSV rows one-line and tab-safe.
func flattenWhitespace(s string) string {
	if !strings.ContainsAny(s, "\t\n\r") {
		return s
	}
	return strings.Join(strings.Fields(s), " ")
}
