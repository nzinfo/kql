// Package kql is the public API for parsing, translating, and executing KQL
// queries against SQL backends.
//
// Minimal e2e (per docs/PROGRESS.md / user direction): Exec parses a KQL query,
// translates it to IR, emits SQLite SQL, and runs it. Only the SQLite backend
// is wired for now; pg/duckdb land with their backend lines.
//
// Example:
//
//	res, err := kql.Exec(ctx, ":memory:", `MyTable | where Count > 5 | take 10`)
//	for _, row := range res.Rows {
//	    fmt.Println(row)
//	}
package kql

import (
	"context"
	"fmt"

	"nzinfo/kql/internal/backend"
	"nzinfo/kql/internal/backend/sqlite"
	"nzinfo/kql/internal/frontend/diagnostic"
	"nzinfo/kql/internal/frontend/parser"
	"nzinfo/kql/internal/ir"
)

// Exec runs a KQL query against the SQLite database at dsn and returns the
// result. Parse/translate errors are surfaced as a kql.Error wrapping the
// diagnostic list.
//
// dsn examples: ":memory:", "file:/path/to.db", "file::memory:?cache=shared".
func Exec(ctx context.Context, dsn, query string) (*Result, error) {
	bk, err := sqlite.New(dsn)
	if err != nil {
		return nil, err
	}
	defer bk.Close()
	return ExecOn(ctx, bk, query)
}

// ExecOn runs a KQL query against an already-open backend. The caller owns the
// backend's lifecycle. This is the backend-agnostic entry point; Exec is the
// SQLite convenience wrapper.
func ExecOn(ctx context.Context, bk backend.Backend, query string) (*Result, error) {
	pipe, err := ParseTranslate(query)
	if err != nil {
		return nil, err
	}
	q, err := bk.Emit(pipe)
	if err != nil {
		return nil, fmt.Errorf("emit: %w", err)
	}
	r, err := bk.Exec(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("exec: %w", err)
	}
	return &Result{r}, nil
}

// ParseTranslate parses a KQL query and translates it to IR, returning the
// pipeline and any diagnostics as an error. Exposed so callers can dry-run /
// `kql explain` without executing.
func ParseTranslate(query string) (*ir.Pipeline, error) {
	p := parser.New("kql", query)
	script := p.Parse()
	var diags diagnostic.List
	// Surface parse errors first.
	pDiags := p.Diagnostics()
	for _, d := range pDiags.Items() {
		diags.Add(d)
	}
	if pDiags.HasErrors() {
		return nil, &Error{diags: diags, stage: "parse"}
	}
	pipe := ir.Translate(script, &diags)
	if diags.HasErrors() {
		return nil, &Error{diags: diags, stage: "translate"}
	}
	if pipe == nil {
		return nil, &Error{diags: diags, stage: "translate", msg: "no pipeline produced"}
	}
	return pipe, nil
}

// Result wraps a backend.Result.
type Result struct {
	inner *backend.Result
}

// Columns returns the result columns.
func (r *Result) Columns() []backend.ResultColumn { return r.inner.Columns }

// Rows returns the result rows. Each row is a slice of cell values (driver-
// native types: int64, float64, string, []byte, nil, time.Time).
func (r *Result) Rows() [][]interface{} { return r.inner.Rows }

// Error is returned by Exec/ParseTranslate when a parse or translate diagnostic
// is produced. It carries the full diagnostic list for inspection.
type Error struct {
	diags diagnostic.List
	stage string // "parse" or "translate"
	msg   string
}

// Error implements the error interface.
func (e *Error) Error() string {
	if e.msg != "" {
		return fmt.Sprintf("kql %s: %s", e.stage, e.msg)
	}
	return fmt.Sprintf("kql %s: %v", e.stage, e.diags.Render())
}

// Diagnostics returns the underlying diagnostic list.
func (e *Error) Diagnostics() []string { return e.diags.Render() }
