// Package exec — client-side stage implementations for the PostProc region.
//
// When a pipeline contains a PostProc operator (mv-expand, parse), the exec
// layer splits at that boundary: pre-stages run in SQL, post-stages run
// client-side. These helpers implement the client-side semantics for the
// stages that commonly follow a PostProc operator (Aggregate count/sum,
// Limit, Project).
package exec

import (
	"fmt"

	"nzinfo/kql/internal/ir"
)

// aggregateRowsClient runs a client-side Aggregate over the result rows.
// Supports count() (with/without group-by), sum/avg/min/max (with group-by),
// mirroring the common summarize shapes that follow mv-expand/parse.
func aggregateRowsClient(res *Result, n *ir.Aggregate) (*Result, error) {
	// Determine group-by key column indices.
	keyIdx := make([]int, len(n.Keys))
	for i, k := range n.Keys {
		name := k.Name
		if col, ok := k.Expr.(*ir.Col); ok && name == "" {
			name = col.Name
		}
		keyIdx[i] = colIndex(res.Columns, name)
	}
	// Group rows by composite key.
	type group struct {
		keys []interface{}
		rows [][]interface{}
	}
	order := []string{}
	groups := map[string]*group{}
	for _, row := range res.Rows {
		key := make([]interface{}, len(keyIdx))
		kstr := ""
		for i, idx := range keyIdx {
			if idx >= 0 {
				key[i] = row[idx]
				kstr += fmt.Sprintf("%v\x00", row[idx])
			}
		}
		g, ok := groups[kstr]
		if !ok {
			g = &group{keys: key}
			groups[kstr] = g
			keyLabel := ""
			for _, k := range key {
				keyLabel += fmt.Sprintf("%v", k)
			}
			order = append(order, keyLabel)
		}
		g.rows = append(g.rows, row)
	}
	// Build output: key columns + aggregate columns.
	outCols := []string{}
	for _, k := range n.Keys {
		outCols = append(outCols, k.Name)
	}
	for _, a := range n.Aggregates {
		outCols = append(outCols, a.Name)
	}
	// Preserve group insertion order (KQL summarize preserves input order).
	var outRows [][]interface{}
	seen := map[string]bool{}
	for _, row := range res.Rows {
		kstr := ""
		for _, idx := range keyIdx {
			if idx >= 0 {
				kstr += fmt.Sprintf("%v\x00", row[idx])
			}
		}
		if seen[kstr] {
			continue
		}
		seen[kstr] = true
		g := groups[kstr]
		outRow := make([]interface{}, len(outCols))
		for i := range keyIdx {
			outRow[i] = g.keys[i]
		}
		for i, a := range n.Aggregates {
			outRow[len(keyIdx)+i] = computeAggregate(a, g.rows, res.Columns)
		}
		outRows = append(outRows, outRow)
	}
	return &Result{Columns: outCols, Rows: outRows}, nil
}

// computeAggregate evaluates one aggregate expression over a group's rows.
// Supports count() (all rows), count(col) (non-null), sum/avg/min/max(col).
func computeAggregate(ne *ir.NamedExpr, rows [][]interface{}, cols []string) interface{} {
	fc, ok := ne.Expr.(*ir.FuncCall)
	if !ok {
		return int64(len(rows))
	}
	switch fc.Name {
	case "count":
		if len(fc.Args) == 0 {
			return int64(len(rows))
		}
		// count(col) → non-null count
		cnt := int64(0)
		if c, ok := fc.Args[0].(*ir.Col); ok {
			idx := colIndex(cols, c.Name)
			for _, r := range rows {
				if idx >= 0 && idx < len(r) && r[idx] != nil {
					cnt++
				}
			}
		}
		return cnt
	case "sum":
		return numericAggregate(fc, rows, cols, "sum")
	case "avg":
		return numericAggregate(fc, rows, cols, "avg")
	case "min":
		return numericAggregate(fc, rows, cols, "min")
	case "max":
		return numericAggregate(fc, rows, cols, "max")
	}
	return nil
}

// numericAggregate computes sum/avg/min/max over a column referenced by the
// aggregate's first arg. Returns nil if the column isn't found or has no values.
func numericAggregate(fc *ir.FuncCall, rows [][]interface{}, cols []string, op string) interface{} {
	col, ok := fc.Args[0].(*ir.Col)
	if !ok {
		return nil
	}
	idx := colIndex(cols, col.Name)
	if idx < 0 {
		return nil
	}
	var sum, mn, mx float64
	count := 0
	for _, r := range rows {
		if idx >= len(r) || r[idx] == nil {
			continue
		}
		v := toFloat64(r[idx])
		if count == 0 {
			mn, mx = v, v
		} else {
			if v < mn { mn = v }
			if v > mx { mx = v }
		}
		sum += v
		count++
	}
	switch op {
	case "sum":
		return sum
	case "avg":
		if count == 0 { return nil }
		return sum / float64(count)
	case "min":
		return mn
	case "max":
		return mx
	}
	return nil
}

// limitRowsClient applies a client-side LIMIT N.
func limitRowsClient(res *Result, n *ir.Limit) (*Result, error) {
	if l, ok := n.Count.(*ir.Lit); ok {
		if iv, ok := l.Value.(int); ok && iv >= 0 && iv < len(res.Rows) {
			rows := make([][]interface{}, iv)
			copy(rows, res.Rows[:iv])
			return &Result{Columns: res.Columns, Rows: rows}, nil
		}
	}
	return res, nil
}

// projectRowsClient applies a client-side Project: select/rename columns.
func projectRowsClient(res *Result, n *ir.Project) (*Result, error) {
	// Handle Star (passthrough).
	for _, c := range n.Cols {
		if _, ok := c.Expr.(*ir.Star); ok {
			return res, nil
		}
	}
	outCols := make([]string, len(n.Cols))
	srcIdx := make([]int, len(n.Cols))
	for i, c := range n.Cols {
		outCols[i] = c.Name
		srcIdx[i] = -1
		if col, ok := c.Expr.(*ir.Col); ok {
			srcIdx[i] = colIndex(res.Columns, col.Name)
			if c.Name == "" {
				outCols[i] = col.Name
			}
		}
	}
	outRows := make([][]interface{}, len(res.Rows))
	for ri, row := range res.Rows {
		nr := make([]interface{}, len(outCols))
		for i, idx := range srcIdx {
			if idx >= 0 && idx < len(row) {
				nr[i] = row[idx]
			}
		}
		outRows[ri] = nr
	}
	return &Result{Columns: outCols, Rows: outRows}, nil
}

// toFloat64 converts a numeric-ish interface{} to float64.
func toFloat64(v interface{}) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case float32:
		return float64(x)
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case int32:
		return float64(x)
	}
	return 0
}

