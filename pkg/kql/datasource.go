// Package kql — datasource file loading (S1.S6).
//
// LoadFile reads a CSV or JSON file into the backend as a named table, enabling
// ad-hoc KQL queries against file data without a database. For DuckDB this
// uses native file reading (DuckDB reads CSV/Parquet directly); for SQLite the
// data is parsed in Go and inserted via batch INSERT.
package kql

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"nzinfo/kql/internal/backend"
)

// LoadFile loads a data file into the backend as a table with the given name.
// Supported formats: .csv, .json (array of objects), .jsonl (one object per line).
// Returns an error for unsupported extensions.
//
// For DuckDB: uses native `CREATE TABLE ... AS SELECT * FROM read_csv(...)` /
// `read_json(...)`, which is zero-copy and parallelized.
// For SQLite: parses in Go and batch-inserts.
func LoadFile(ctx context.Context, bk backend.Backend, path, tableName string) error {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".csv":
		return loadCSV(ctx, bk, path, tableName)
	case ".json":
		return loadJSON(ctx, bk, path, tableName)
	case ".jsonl", ".ndjson":
		return loadJSONL(ctx, bk, path, tableName)
	case ".parquet":
		return loadParquet(ctx, bk, path, tableName)
	default:
		return fmt.Errorf("unsupported file format %q (supported: .csv, .json, .jsonl, .parquet)", ext)
	}
}

// SupportedFormats returns the list of supported file extensions.
func SupportedFormats() []string {
	return []string{".csv", ".json", ".jsonl", ".parquet"}
}

func loadCSV(ctx context.Context, bk backend.Backend, path, table string) error {
	// DuckDB: native CSV reader (fastest path).
	if bk.Dialect() == backend.DialectDuckDB {
		_, err := bk.Exec(ctx, &backend.Query{
			SQL: fmt.Sprintf(`CREATE TABLE "%s" AS SELECT * FROM read_csv('%s', header=true, auto_detect=true)`,
				table, escapeSQL(path)),
		})
		return err
	}
	// Generic: parse in Go, batch insert.
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	r := csv.NewReader(f)
	headers, err := r.Read()
	if err != nil {
		return fmt.Errorf("read CSV header: %w", err)
	}
	// Create table.
	if err := createTableFromHeaders(ctx, bk, table, headers); err != nil {
		return err
	}
	// Batch insert.
	return batchInsert(ctx, bk, table, headers, func() ([]string, error) {
		return r.Read()
	})
}

func loadJSON(ctx context.Context, bk backend.Backend, path, table string) error {
	// DuckDB: native JSON reader.
	if bk.Dialect() == backend.DialectDuckDB {
		_, err := bk.Exec(ctx, &backend.Query{
			SQL: fmt.Sprintf(`CREATE TABLE "%s" AS SELECT * FROM read_json('%s', auto_detect=true)`,
				table, escapeSQL(path)),
		})
		return err
	}
	// Generic: parse JSON array of objects.
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	var rows []map[string]interface{}
	if err := json.Unmarshal(data, &rows); err != nil {
		return fmt.Errorf("parse JSON: %w", err)
	}
	if len(rows) == 0 {
		return fmt.Errorf("JSON file is empty")
	}
	// Collect column names from first row.
	headers := make([]string, 0, len(rows[0]))
	for k := range rows[0] {
		headers = append(headers, k)
	}
	if err := createTableFromHeaders(ctx, bk, table, headers); err != nil {
		return err
	}
	// Batch insert from JSON rows.
	return batchInsertRows(ctx, bk, table, headers, rows)
}

func loadJSONL(ctx context.Context, bk backend.Backend, path, table string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	// Read first object to get headers.
	var first map[string]interface{}
	if err := dec.Decode(&first); err != nil {
		return fmt.Errorf("decode first JSONL line: %w", err)
	}
	headers := make([]string, 0, len(first))
	for k := range first {
		headers = append(headers, k)
	}
	if err := createTableFromHeaders(ctx, bk, table, headers); err != nil {
		return err
	}
	// Insert first + remaining.
	rows := []map[string]interface{}{first}
	for {
		var obj map[string]interface{}
		if err := dec.Decode(&obj); err == io.EOF {
			break
		} else if err != nil {
			return fmt.Errorf("decode JSONL: %w", err)
		}
		rows = append(rows, obj)
	}
	return batchInsertRows(ctx, bk, table, headers, rows)
}

func loadParquet(ctx context.Context, bk backend.Backend, path, table string) error {
	// DuckDB: native Parquet reader.
	if bk.Dialect() == backend.DialectDuckDB {
		_, err := bk.Exec(ctx, &backend.Query{
			SQL: fmt.Sprintf(`CREATE TABLE "%s" AS SELECT * FROM read_parquet('%s')`,
				table, escapeSQL(path)),
		})
		return err
	}
	return fmt.Errorf("parquet loading requires DuckDB backend")
}

// --- helpers ---

func createTableFromHeaders(ctx context.Context, bk backend.Backend, table string, headers []string) error {
	cols := make([]string, len(headers))
	for i, h := range headers {
		cols[i] = fmt.Sprintf(`"%s" TEXT`, h) // default to TEXT for CSV/JSON
	}
	_, err := bk.Exec(ctx, &backend.Query{
		SQL: fmt.Sprintf(`CREATE TABLE "%s" (%s)`, table, strings.Join(cols, ", ")),
	})
	return err
}

func batchInsert(ctx context.Context, bk backend.Backend, table string, headers []string, nextRow func() ([]string, error)) error {
	placeholders := make([]string, len(headers))
	for i := range placeholders {
		placeholders[i] = "?"
	}
	phSQL := "(" + strings.Join(placeholders, ",") + ")"
	colSQL := "(" + strings.Join(quoteAll(headers), ",") + ")"
	batchSize := 500
	var batchArgs []interface{}
	var batchRows int
	for {
		row, err := nextRow()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read row: %w", err)
		}
		for _, v := range row {
			batchArgs = append(batchArgs, v)
		}
		batchRows++
		if batchRows >= batchSize {
			if err := flushBatch(ctx, bk, table, colSQL, phSQL, batchArgs, batchRows, len(headers)); err != nil {
				return err
			}
			batchArgs = batchArgs[:0]
			batchRows = 0
		}
	}
	if batchRows > 0 {
		return flushBatch(ctx, bk, table, colSQL, phSQL, batchArgs, batchRows, len(headers))
	}
	return nil
}

func batchInsertRows(ctx context.Context, bk backend.Backend, table string, headers []string, rows []map[string]interface{}) error {
	return batchInsert(ctx, bk, table, headers, func() ([]string, error) {
		if len(rows) == 0 {
			return nil, io.EOF
		}
		row := rows[0]
		rows = rows[1:]
		vals := make([]string, len(headers))
		for i, h := range headers {
			vals[i] = fmt.Sprintf("%v", row[h])
		}
		return vals, nil
	})
}

func flushBatch(ctx context.Context, bk backend.Backend, table, colSQL, phSQL string, args []interface{}, nRows, nCols int) error {
	phs := make([]string, nRows)
	for i := range phs {
		phs[i] = phSQL
	}
	_, err := bk.Exec(ctx, &backend.Query{
		SQL:  fmt.Sprintf(`INSERT INTO "%s" %s VALUES %s`, table, colSQL, strings.Join(phs, ",")),
		Args: args,
	})
	return err
}

func quoteAll(names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = `"` + n + `"`
	}
	return out
}

func escapeSQL(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}
