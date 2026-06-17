package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"nzinfo/kql/pkg/kql"
)

// printResult writes a *kql.Result to w in the requested format.
// Supported: csv (default), json, table.
func printResult(w io.Writer, res *kql.Result, format string) error {
	switch strings.ToLower(format) {
	case "", "csv":
		return printCSV(w, res)
	case "json":
		return printJSON(w, res)
	case "table":
		return printTable(w, res)
	default:
		return fmt.Errorf("unknown output format %q (use csv|json|table)", format)
	}
}

// printCSV writes a header row followed by data rows, RFC 4180 quoting.
func printCSV(w io.Writer, res *kql.Result) error {
	cw := csv.NewWriter(w)
	header := make([]string, len(res.Columns()))
	for i, c := range res.Columns() {
		header[i] = c.Name
	}
	if err := cw.Write(header); err != nil {
		return err
	}
	for _, row := range res.Rows() {
		if err := cw.Write(cellsAsStrings(row)); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// printJSON writes a JSON array of objects, one per row, keyed by column name.
// Null cells become JSON null.
func printJSON(w io.Writer, res *kql.Result) error {
	names := make([]string, len(res.Columns()))
	for i, c := range res.Columns() {
		names[i] = c.Name
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	rows := make([]map[string]interface{}, 0, len(res.Rows()))
	for _, row := range res.Rows() {
		obj := make(map[string]interface{}, len(names))
		for i, name := range names {
			obj[name] = jsonValue(row[i])
		}
		rows = append(rows, obj)
	}
	return enc.Encode(rows)
}

// printTable writes an aligned ASCII table (column-aligned header + rows).
func printTable(w io.Writer, res *kql.Result) error {
	names := make([]string, len(res.Columns()))
	widths := make([]int, len(res.Columns()))
	for i, c := range res.Columns() {
		names[i] = c.Name
		widths[i] = len(c.Name)
	}
	strRows := make([][]string, len(res.Rows()))
	for r, row := range res.Rows() {
		cells := cellsAsStrings(row)
		strRows[r] = cells
		for i, c := range cells {
			if len(c) > widths[i] {
				widths[i] = len(c)
			}
		}
	}
	// header
	fmt.Fprintln(w, joinPadded(names, widths))
	fmt.Fprintln(w, joinDashes(widths))
	for _, r := range strRows {
		fmt.Fprintln(w, joinPadded(r, widths))
	}
	return nil
}

// cellsAsStrings converts a row's interface{} cells to display strings.
func cellsAsStrings(row []interface{}) []string {
	out := make([]string, len(row))
	for i, v := range row {
		out[i] = cellString(v)
	}
	return out
}

// cellString renders one cell value as a compact string. []byte → string;
// nil → "" (csv) ; time/other → fmt.
func cellString(v interface{}) string {
	switch x := v.(type) {
	case nil:
		return ""
	case []byte:
		return string(x)
	case string:
		return x
	default:
		return fmt.Sprintf("%v", x)
	}
}

// jsonValue converts a driver cell value into a JSON-encodable value. []byte
// becomes a string (base64 would be surprising for text columns); nil stays nil.
func jsonValue(v interface{}) interface{} {
	switch x := v.(type) {
	case []byte:
		return string(x)
	default:
		return v
	}
}

// joinPadded joins cells with a two-space separator, each left-padded to width.
func joinPadded(cells []string, widths []int) string {
	var b strings.Builder
	for i, c := range cells {
		if i > 0 {
			b.WriteString("  ")
		}
		b.WriteString(c)
		if pad := widths[i] - len(c); pad > 0 {
			b.WriteString(strings.Repeat(" ", pad))
		}
	}
	return b.String()
}

// joinDashes builds a separator line of dashes per column.
func joinDashes(widths []int) string {
	parts := make([]string, len(widths))
	for i, wd := range widths {
		parts[i] = strings.Repeat("-", wd)
	}
	return strings.Join(parts, "  ")
}
