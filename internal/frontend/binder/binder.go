// Package binder resolves column references and validates a KQL pipeline
// against a known schema, producing friendly errors (KQL009 unknown column)
// BEFORE execution — instead of letting a raw SQLite "no such column" surface
// at runtime with no KQL context.
//
// Scope (F5-minimal): the binder walks an *ir.Pipeline stage by stage, tracking
// the output schema (set of column names) of each stage. It resolves each
// *ir.Col reference against the current stage's input schema, emitting a
// diagnostic for unknown columns. It does NOT yet do full type inference or
// physical ColID assignment (those come with the optimizer); for the minimal
// loop it validates names so users get "column 'foo' not found in events" with
// the table/operator context, not a SQLite parse-time error.
//
// Source schema: provided by the caller (the backend knows its tables). A
// nil/empty schema makes the binder permissive (no unknown-column errors) so
// it can run against sources whose schema isn't introspected.
package binder

import (
	"fmt"

	"nzinfo/kql/internal/frontend/diagnostic"
	"nzinfo/kql/internal/frontend/token"
	"nzinfo/kql/internal/ir"
)

// Schema is an ordered set of column names (the output shape of a stage).
type Schema struct {
	Cols []string // in declaration order
}

// Has reports whether name is in the schema (case-sensitive — KQL identifiers
// are case-sensitive; the binder matches exactly to surface typos).
func (s *Schema) Has(name string) bool {
	if s == nil {
		return true // permissive: unknown schema → assume present
	}
	for _, c := range s.Cols {
		if c == name {
			return true
		}
	}
	return false
}

// SchemaProvider resolves a table name to its column schema. The sqlite
// backend implements this via PRAGMA table_info; a nil provider makes binding
// permissive (no unknown-column checks).
type SchemaProvider interface {
	Schema(table string) (*Schema, error)
}

// Bind walks the pipeline validating column references. Diagnostics for
// unknown columns are added to diags. Returns the pipeline unchanged (the
// binder is a validator, not a rewriter, for the minimal loop).
func Bind(pipe *ir.Pipeline, prov SchemaProvider, diags *diagnostic.List) (*ir.Pipeline, error) {
	b := &binder{prov: prov, diags: diags}
	return b.bindPipeline(pipe)
}

type binder struct {
	prov SchemaProvider
	diags *diagnostic.List
}

// bindPipeline resolves the source schema then walks each stage, threading the
// output schema forward.
func (b *binder) bindPipeline(pipe *ir.Pipeline) (*ir.Pipeline, error) {
	if pipe == nil {
		return nil, nil
	}
	schema := b.sourceSchema(pipe.Source)
	for _, st := range pipe.Stages {
		schema = b.bindStage(st, schema)
	}
	return pipe, nil
}

// sourceSchema returns the column set of the pipeline source. For a table
// reference it queries the provider; for other sources it returns nil
// (permissive). nil schema ⇒ Has() always true (no unknown-col errors).
func (b *binder) sourceSchema(src ir.Source) *Schema {
	if src == nil {
		return nil
	}
	if tbl, ok := src.(*ir.SourceTable); ok {
		if b.prov == nil {
			return nil
		}
		s, err := b.prov.Schema(tbl.Table)
		if err != nil {
			b.errorf(tbl.Pos(), "cannot resolve schema for table %q: %v", tbl.Table, err)
			return nil
		}
		return s
	}
	return nil
}

// bindStage validates column references in a stage against the input schema,
// then returns the stage's OUTPUT schema (what the next stage sees).
func (b *binder) bindStage(st ir.Stage, in *Schema) *Schema {
	switch s := st.(type) {
	case *ir.Filter:
		b.checkExpr(s.Predicate, in)
		return in
	case *ir.Limit:
		b.checkExpr(s.Count, in)
		return in
	case *ir.Sort:
		for _, k := range s.Keys {
			b.checkExpr(k.Expr, in)
		}
		return in
	case *ir.Distinct:
		for _, c := range s.Cols {
			b.checkExpr(c, in)
		}
		return in
	case *ir.Project:
		out := &Schema{}
		for _, c := range s.Cols {
			b.checkExpr(c.Expr, in)
			out.Cols = append(out.Cols, projName(c))
		}
		return out
	case *ir.Extend:
		out := &Schema{}
		if in != nil {
			out.Cols = append(out.Cols, in.Cols...)
		}
		for _, c := range s.Cols {
			b.checkExpr(c.Expr, in)
			if c.Name != "" {
				out.Cols = append(out.Cols, c.Name)
			}
		}
		return out
	case *ir.Aggregate:
		out := &Schema{}
		for _, k := range s.Keys {
			b.checkExpr(k.Expr, in)
			out.Cols = append(out.Cols, keyName(k))
		}
		for _, a := range s.Aggregates {
			b.checkExpr(a.Expr, in)
			out.Cols = append(out.Cols, aggName(a))
		}
		return out
	case *ir.Join:
		// Recurse into the right pipeline (own schema); the join's output is
		// the union of left + right columns (for the minimal binder we don't
		// resolve ambiguity yet — that's tracked; emit left-biases, which is a
		// known limitation).
		if s.Right != nil {
			_, _ = b.bindPipeline(s.Right)
		}
		// on-conditions reference left columns; we only check loosely here.
		for _, c := range s.On {
			b.checkExpr(c, in)
		}
		return in // approx: keep left schema (real impl unions left+right)
	case *ir.Union:
		for _, in2 := range s.Inputs {
			_, _ = b.bindPipeline(in2)
		}
		return in
	}
	return in
}

// checkExpr walks an expression validating *ir.Col references against schema.
func (b *binder) checkExpr(e ir.Expr, in *Schema) {
	if e == nil {
		return
	}
	switch n := e.(type) {
	case *ir.Col:
		if !in.Has(n.Name) {
			b.errorf(n.Pos(), "column %q not found in current scope", n.Name)
		}
	case *ir.BinOp:
		b.checkExpr(n.X, in)
		b.checkExpr(n.Y, in)
	case *ir.UnaryOp:
		b.checkExpr(n.X, in)
	case *ir.FuncCall:
		for _, a := range n.Args {
			b.checkExpr(a, in)
		}
	case *ir.Member:
		b.checkExpr(n.X, in)
	case *ir.Index:
		b.checkExpr(n.X, in)
		b.checkExpr(n.Index, in)
	case *ir.Case:
		b.checkExpr(n.Cond, in)
		b.checkExpr(n.Then, in)
		b.checkExpr(n.Else, in)
	case *ir.List:
		for _, el := range n.Elems {
			b.checkExpr(el, in)
		}
	case *ir.Lit, *ir.Star:
		// leaves — no column refs
	}
}

// projName / keyName / aggName return the output column name a NamedExpr
// contributes. Bare (unnamed) expressions keep "" (so the schema doesn't claim
// a name they don't have — those columns are emit-only).
func projName(n *ir.NamedExpr) string {
	if n == nil {
		return ""
	}
	if n.Name != "" {
		return n.Name
	}
	if c, ok := n.Expr.(*ir.Col); ok {
		return c.Name
	}
	return ""
}
func keyName(n *ir.NamedExpr) string  { return projName(n) }
func aggName(n *ir.NamedExpr) string  { return projName(n) }

func (b *binder) errorf(pos token.Pos, format string, args ...interface{}) {
	if b.diags == nil {
		return
	}
	b.diags.Add(diagnostic.Diagnostic{
		Severity: diagnostic.Error,
		Code:     diagnostic.UnknownColumn,
		Pos:      token.Position{Offset: int(pos) - 1},
		Message:  fmt.Sprintf(format, args...),
	})
}
