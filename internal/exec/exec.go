// Package exec provides the execution layer that orchestrates SQL execution
// with client-side post-processing for operators the backend can't express
// (mv-expand, parse, make-series — marked NeedsPostProc).
//
// Strategy: split the IR pipeline at PostProc boundaries. The stages BEFORE
// the first PostProc operator are emitted as SQL and executed by the backend.
// The PostProc operator and subsequent stages run client-side in Go on the
// returned rows.
//
// Current scope: mv-expand (explode an array/dynamic column into multiple rows).
// parse/make-series/series-* are reserved for future expansion.
package exec

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"nzinfo/kql/internal/backend"
	"nzinfo/kql/internal/ir"
)

// Result is the execution result (mirrors backend.Result but lives in exec so
// callers don't import backend).
type Result struct {
	Columns []string
	Rows    [][]interface{}
}

// ExecPipeline runs an IR pipeline, splitting at PostProc boundaries. The
// pre-PostProc stages are emitted+executed by the backend (bk.Emit + bk.Exec);
// PostProc stages run client-side on the returned rows.
//
// If no PostProc stages exist, this is equivalent to bk.Emit + bk.Exec.
func ExecPipeline(ctx context.Context, bk backend.Backend, pipe *ir.Pipeline) (*Result, error) {
	if pipe == nil {
		return nil, fmt.Errorf("nil pipeline")
	}
	// Step 1: Arrow execution path (if backend supports it and the build has
	// -tags duckdb_arrow). When arrowExecHook is nil (no tag), this is a no-op.
	if arrowExecHook != nil {
		res, used, err := arrowExecHook(ctx, bk, pipe)
		if err != nil {
			return nil, err
		}
		if used {
			return res, nil
		}
		// Not used → fall through to row path.
	}
	// O4: check for an IndexLookup-hinted join first (two-phase strategy).
	// This runs BEFORE the normal path; if it applies, it returns early. If it
	// can't apply (no keys extractable, too many keys), it falls back.
	if findIndexLookupJoin(pipe.Stages) >= 0 {
		res, applied, err := execIndexLookup(ctx, bk, pipe)
		if err != nil {
			return nil, err
		}
		if applied {
			return res, nil
		}
		// Not applied → fall through to normal path.
	}
	splitIdx := findPostProcBoundary(pipe.Stages)
	if splitIdx < 0 {
		// No PostProc: emit the whole pipeline and execute directly.
		q, err := bk.Emit(pipe)
		if err != nil {
			return nil, fmt.Errorf("emit: %w", err)
		}
		res, err := bk.Exec(ctx, q)
		if err != nil {
			return nil, fmt.Errorf("exec: %w", err)
		}
		return backendResultToExec(res), nil
	}

	// Split: pre-stages → SQL, post-stages → client-side.
	prePipe := &ir.Pipeline{
		Source:   pipe.Source,
		Stages:   pipe.Stages[:splitIdx],
		Position: pipe.Position,
	}
	q, err := bk.Emit(prePipe)
	if err != nil {
		return nil, fmt.Errorf("emit (pre-postproc): %w", err)
	}
	bkRes, err := bk.Exec(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("exec (pre-postproc): %w", err)
	}

	// Run PostProc stages client-side.
	result := backendResultToExec(bkRes)
	for _, st := range pipe.Stages[splitIdx:] {
		var err error
		result, err = applyPostProc(result, st)
		if err != nil {
			return nil, fmt.Errorf("postproc %T: %w", st, err)
		}
	}
	return result, nil
}

// findPostProcBoundary returns the index of the first PostProc stage, or -1
// if none. mv-expand is the first implemented PostProc operator.
func findPostProcBoundary(stages []ir.Stage) int {
	for i, st := range stages {
		if isPostProc(st) {
			return i
		}
	}
	return -1
}

// isPostProc reports whether a stage requires client-side processing.
func isPostProc(st ir.Stage) bool {
	switch st.(type) {
	case *ir.MvExpand, *ir.Parse:
		return true // dedicated PostProc IR stages (mv-expand, parse/parse-where)
	}
	return false
}

// applyPostProc runs one stage client-side on the current result set.
func applyPostProc(res *Result, st ir.Stage) (*Result, error) {
	switch n := st.(type) {
	case *ir.MvExpand:
		// mv-expand: explode the SOURCE array column. Source is typically a bare
		// *ir.Col naming the array; ColName is the optional output rename.
		srcCol := n.ColName
		if c, ok := n.Source.(*ir.Col); ok && c.Name != "" {
			srcCol = c.Name
		}
		outCol := n.ColName
		if outCol == "" {
			outCol = srcCol
		}
		return mvExpandRows(res, srcCol, outCol)
	case *ir.Parse:
		targetCol := ""
		if c, ok := n.Target.(*ir.Col); ok {
			targetCol = c.Name
		}
		return parseRows(res, &ParseSpec{
			TargetCol: targetCol,
			Pattern:   n.Pattern,
			IsWhere:   n.IsWhere,
		})
	case *ir.Aggregate:
		return aggregateRowsClient(res, n)
	case *ir.Limit:
		return limitRowsClient(res, n)
	case *ir.Sort:
		// Client-side sort is a no-op here (order preserved from input); a full
		// client sort would need a comparator. Keep row order as-is.
		return res, nil
	case *ir.Project:
		// Client-side projection: select/rename columns by name.
		return projectRowsClient(res, n)
	}
	return res, nil
}

// backendResultToExec converts a backend.Result to an exec.Result.
func backendResultToExec(r *backend.Result) *Result {
	cols := make([]string, len(r.Columns))
	for i, c := range r.Columns {
		cols[i] = c.Name
	}
	return &Result{Columns: cols, Rows: r.Rows}
}

// mvExpandRows is the client-side mv-expand implementation: for each row,
// explode the srcCol array column into multiple rows (one per element), renaming
// the exploded column to outCol. Non-array values produce a single row with the
// original value. Used when the backend lacks UNNEST (sqlite) or complex types.
func mvExpandRows(res *Result, srcCol, outCol string) (*Result, error) {
	colIdx := -1
	for i, c := range res.Columns {
		if c == srcCol {
			colIdx = i
			break
		}
	}
	if colIdx < 0 {
		return res, nil // column not found; passthrough
	}
	// Rename the exploded column in the output schema when outCol != srcCol.
	if outCol != srcCol {
		outCols := append([]string{}, res.Columns...)
		outCols[colIdx] = outCol
		res.Columns = outCols
	}

	var outRows [][]interface{}
	for _, row := range res.Rows {
		val := row[colIdx]
		elems := explodeValue(val)
		if len(elems) == 0 {
			// null/empty array → one row with null
			newRow := make([]interface{}, len(row))
			copy(newRow, row)
			newRow[colIdx] = nil
			outRows = append(outRows, newRow)
			continue
		}
		for _, el := range elems {
			newRow := make([]interface{}, len(row))
			copy(newRow, row)
			newRow[colIdx] = el
			outRows = append(outRows, newRow)
		}
	}
	res.Rows = outRows
	return res, nil
}

// explodeValue extracts elements from an array-like value (JSON string,
// []interface{}, or a scalar wrapped in a single-element slice).
func explodeValue(v interface{}) []interface{} {
	if v == nil {
		return nil
	}
	switch x := v.(type) {
	case []interface{}:
		return x
	case string:
		// Try JSON parse (dynamic columns come as JSON text from SQL backends).
		var arr []interface{}
		if json.Unmarshal([]byte(x), &arr) == nil {
			return arr
		}
		return []interface{}{x}
	case []byte:
		var arr []interface{}
		if json.Unmarshal(x, &arr) == nil {
			return arr
		}
		return []interface{}{string(x)}
	default:
		return []interface{}{v}
	}
}


// parseRows implements the client-side `parse [Kind] Target with Pattern`
// PostProc operator. The KQL pattern syntax uses `*ColName` placeholders:
// literal text between/around them is matched verbatim, and each `*ColName`
// captures the substring up to the next literal (or end).
//
// Example: parse EventData with '<Tag>' Tag '<' extracts Tag between the
// literal '<Tag>' and '<'.
//
// For IsWhere (parse-where), non-matching rows are dropped; plain parse keeps
// all rows (captures stay null on mismatch).
//
// Captured columns are appended to the result schema.
func parseRows(res *Result, n *ParseSpec) (*Result, error) {
	targetIdx := -1
	for i, c := range res.Columns {
		if c == n.TargetCol {
			targetIdx = i
			break
		}
	}
	if targetIdx < 0 {
		return res, nil // target column not present; passthrough
	}
	// Build capture column names from the pattern.
	captures := parseCaptureNames(n.Pattern)
	// Extend schema with capture columns.
	newCols := append([]string{}, res.Columns...)
	for _, cap := range captures {
		newCols = append(newCols, cap)
	}
	var outRows [][]interface{}
	for _, row := range res.Rows {
		target := ""
		if row[targetIdx] != nil {
			target = fmt.Sprintf("%v", row[targetIdx])
		}
		vals, matched := matchParsePattern(n.Pattern, target)
		if !matched && n.IsWhere {
			continue // parse-where: drop non-matching rows
		}
		newRow := make([]interface{}, len(row)+len(captures))
		copy(newRow, row)
		if matched {
			for i, v := range vals {
				newRow[len(row)+i] = v
			}
		}
		outRows = append(outRows, newRow)
	}
	return &Result{Columns: newCols, Rows: outRows}, nil
}

// ParseSpec is a thin adapter so exec doesn't import ir for the parse params.
// (Built from *ir.Parse in applyPostProc via a local helper.)
type ParseSpec struct {
	TargetCol string
	Pattern   string
	IsWhere   bool
}

// parseTokenKind classifies a parse-pattern token.
type parseTokenKind int

const (
	ptWildcard parseTokenKind = iota // `*` — match any text (non-capturing)
	ptLiteral                        // 'quoted' or "quoted" — match verbatim
	ptCapture                        // bare identifier — capture into a new column
)

type parseToken struct {
	kind parseTokenKind
	text string // literal text (ptLiteral) or capture name (ptCapture)
}

// tokenizeParsePattern splits a KQL parse pattern into tokens. Syntax:
//   - `*` → wildcard (non-capturing, matches any text)
//   - `'...'` or `"..."` → literal (matched verbatim)
//   - a bare word (letters/digits/_/:) → capture (captures text up to next token)
// Whitespace separates tokens.
func tokenizeParsePattern(pattern string) []parseToken {
	var toks []parseToken
	i := 0
	for i < len(pattern) {
		c := pattern[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '*':
			toks = append(toks, parseToken{kind: ptWildcard})
			i++
		case c == '\'' || c == '"':
			quote := c
			i++
			start := i
			for i < len(pattern) && pattern[i] != quote {
				i++
			}
			toks = append(toks, parseToken{kind: ptLiteral, text: pattern[start:i]})
			if i < len(pattern) {
				i++ // closing quote
			}
		default:
			// bare word: capture name (letters, digits, _, :, etc.)
			start := i
			for i < len(pattern) && pattern[i] != ' ' && pattern[i] != '\'' && pattern[i] != '"' && pattern[i] != '*' {
				i++
			}
			toks = append(toks, parseToken{kind: ptCapture, text: pattern[start:i]})
		}
	}
	return toks
}

// parseCaptureNames extracts capture column names from a KQL parse pattern.
func parseCaptureNames(pattern string) []string {
	var names []string
	for _, tk := range tokenizeParsePattern(pattern) {
		if tk.kind == ptCapture {
			names = append(names, tk.text)
		}
	}
	return names
}

// matchParsePattern matches a KQL parse pattern against target text. Builds a
// regex from the tokenized pattern: literals escaped, captures → (.*?),
// wildcards → (.*?). Returns capture values (in order) + match success.
func matchParsePattern(pattern, target string) ([]string, bool) {
	toks := tokenizeParsePattern(pattern)
	var sb strings.Builder
	sb.WriteString("^")
	for _, tk := range toks {
		switch tk.kind {
		case ptWildcard:
			sb.WriteString("(.*?)")
		case ptCapture:
			sb.WriteString("(.*?)")
		case ptLiteral:
			sb.WriteString(regexp.QuoteMeta(tk.text))
		}
	}
	sb.WriteString("$")
	re, err := regexp.Compile(sb.String())
	if err != nil {
		return nil, false
	}
	m := re.FindStringSubmatch(target)
	if m == nil {
		return nil, false
	}
	// m[0] is full match; m[1:] are the captures (wildcards + captures). Extract
	// only the capture-name positions.
	var captures []string
	ci := 0
	for _, tk := range toks {
		if tk.kind == ptCapture {
			ci++
			captures = append(captures, m[ci])
		}
		if tk.kind == ptWildcard {
			ci++
		}
	}
	return captures, true
}
