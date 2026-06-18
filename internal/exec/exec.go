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
	case *ir.Project:
		// A passthrough Project{*} (from the P1 passthrough translation of
		// mv-expand/parse/etc.) is a PostProc marker. We detect it by checking
		// if it's a single-col Star project (the passthrough shape).
		// Real projects have actual NamedExpr columns; passthroughs have {Star}.
		return false // handled below via stage-type check
	}
	return false
}

// applyPostProc runs one stage client-side on the current result set.
func applyPostProc(res *Result, st ir.Stage) (*Result, error) {
	// Currently no stages reach here because the P1 passthroughs (mv-expand,
	// parse, etc.) are translated to Project{*} which isn't flagged as PostProc
	// in isPostProc above. When the translator is updated to emit dedicated
	// PostProc IR nodes (MvExpand/Parse stages), this switch will handle them.
	//
	// For now, this is the framework: the split logic works, the passthrough
	// stages stay in SQL, and the hook is here for when we wire real PostProc.
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
// explode the named array column into multiple rows (one per element).
// Non-array values produce a single row with the original value.
// This is used when the backend lacks UNNEST (sqlite) or for complex types.
func mvExpandRows(res *Result, colName string) (*Result, error) {
	colIdx := -1
	for i, c := range res.Columns {
		if c == colName {
			colIdx = i
			break
		}
	}
	if colIdx < 0 {
		return res, nil // column not found; passthrough
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
