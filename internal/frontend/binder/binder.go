// Package binder resolves column references and validates a KQL pipeline
// against a known schema, assigning physical ColIDs to each *ir.Col reference.
//
// ColID binding (DESIGN.md §5) is the multi-dialect abstraction: a column is
// referenced by a stable integer (ColID) within a pipeline, not by its string
// name. The binder allocates ColIDs as it walks the pipeline and resolves each
// *ir.Col name CASE-INSENSITIVELY against the current stage's schema, so a KQL
// reference like `EventType` resolves to the same ColID whether the backend
// stored it as `EventType` (sqlite) or `eventtype` (PostgreSQL lowercased it).
// It writes the backend's PHYSICAL name back into Col.Name, so emit produces
// dialect-correct SQL without any per-backend special-casing.
//
// Scope: ColID allocation + name resolution + unknown-column errors (KQL001).
// NOT yet: type inference (Col.T stays Unknown), join $left/$right
// qualification (left-biased — tracked), PhysicalPlan integration.
package binder

import (
	"fmt"
	"strings"

	"nzinfo/kql/internal/frontend/diagnostic"
	"nzinfo/kql/internal/frontend/token"
	"nzinfo/kql/internal/ir"
)

// ColBinding is one column's binding in a schema: its ColID plus the physical
// name (backend's stored case), display name (KQL source spelling), and the
// inferred data type.
type ColBinding struct {
	ColID        ir.ColID
	PhysicalName string // backend-stored name (pg lowercases; sqlite keeps) — emit uses this
	DisplayName  string // KQL source spelling — diagnostics/pretty-print
	Type         ir.Type // data type (from schema or inferred) — Unknown if not determined
}

// Schema is an ordered set of column bindings (the output shape of a stage).
// Nil schema is permissive (Lookup returns true) so binding can run against
// unintrospectable sources without over-rejecting.
type Schema struct {
	Cols []ColBinding
}

// Lookup resolves name CASE-INSENSITIVELY against the schema (the fix for pg
// case-folding: `EventType` matches `eventtype`). Returns the binding + true
// on hit. A nil schema is permissive (returns a synthetic binding, true).
func (s *Schema) Lookup(name string) (ColBinding, bool) {
	if s == nil {
		// Permissive: unknown schema → assume present, allocate no ColID
		// (ColID stays Invalid; emit falls back to the name as-is).
		return ColBinding{PhysicalName: name, DisplayName: name}, true
	}
	for _, c := range s.Cols {
		if strings.EqualFold(c.PhysicalName, name) || strings.EqualFold(c.DisplayName, name) {
			return c, true
		}
	}
	return ColBinding{}, false
}

// Has reports whether name resolves (case-insensitive). Kept for compatibility.
func (s *Schema) Has(name string) bool {
	_, ok := s.Lookup(name)
	return ok
}

// SchemaProvider resolves a table name to its column schema. The sqlite
// backend implements this via PRAGMA table_info (returns original case); pg
// via information_schema.columns (returns lowercased case). The binder threads
// these physical names through so emit produces dialect-correct SQL.
type SchemaProvider interface {
	Schema(table string) (*Schema, error)
}

// Bind walks the pipeline allocating ColIDs and resolving column references.
// Each *ir.Col that resolves gets its ColID set and its Name rewritten to the
// backend's physical name. Unknown columns produce KQL001 diagnostics. Returns
// the pipeline (mutated in place) — the binder is a resolver/rewriter.
func Bind(pipe *ir.Pipeline, prov SchemaProvider, diags *diagnostic.List) (*ir.Pipeline, error) {
	b := &binder{prov: prov, diags: diags}
	return b.bindPipeline(pipe)
}

type binder struct {
	prov  SchemaProvider
	diags *diagnostic.List
	next  ir.ColID // ColID allocator counter (1-based; 0 = Invalid)
}

// alloc assigns a fresh ColID.
func (b *binder) alloc() ir.ColID {
	b.next++
	return b.next
}

// bindPipeline resolves the source schema then walks each stage, threading the
// output schema (with ColIDs) forward.
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

// sourceSchema returns the column bindings of the pipeline source. For a table
// reference it queries the provider and allocates a ColID per physical column;
// the PhysicalName carries the backend's stored case.
func (b *binder) sourceSchema(src ir.Source) *Schema {
	if src == nil {
		return nil
	}
	if tbl, ok := src.(*ir.SourceTable); ok {
		if b.prov == nil {
			return nil // permissive
		}
		s, err := b.prov.Schema(tbl.Table)
		if err != nil {
			b.errorf(tbl.Pos(), "cannot resolve schema for table %q: %v", tbl.Table, err)
			return nil
		}
		// Allocate a ColID per column; keep the provider's physical name.
		for i := range s.Cols {
			s.Cols[i].ColID = b.alloc()
			if s.Cols[i].DisplayName == "" {
				s.Cols[i].DisplayName = s.Cols[i].PhysicalName
			}
		}
		return s
	}
	return nil
}

// bindStage validates column references against the input schema, then returns
// the stage's OUTPUT schema (with ColIDs threaded forward).
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
			// Output column: named binding gets a new ColID; a bare Col
			// reference inherits the source binding's ColID.
			out.Cols = append(out.Cols, b.projectBinding(c, in))
		}
		return out
	case *ir.Extend:
		out := &Schema{}
		if in != nil {
			out.Cols = append(out.Cols, in.Cols...) // input columns carried forward
		}
		for _, c := range s.Cols {
			b.checkExpr(c.Expr, in)
			if c.Name != "" {
				out.Cols = append(out.Cols, ColBinding{ColID: b.alloc(), PhysicalName: c.Name, DisplayName: c.Name})
			}
		}
		return out
	case *ir.Aggregate:
		out := &Schema{}
		for _, k := range s.Keys {
			b.checkExpr(k.Expr, in)
			out.Cols = append(out.Cols, b.namedBinding(k, in))
		}
		for _, a := range s.Aggregates {
			b.checkExpr(a.Expr, in)
			out.Cols = append(out.Cols, b.namedBinding(a, in))
		}
		return out
	case *ir.Join:
		// Recurse into the right pipeline (own schema/ColIDs).
		var rightSchema *Schema
		if s.Right != nil {
			_, _ = b.bindPipeline(s.Right)
			// Derive the right side's output schema by re-walking it (its source
			// schema). This is approximate — the right pipeline's *output* may
			// differ from its source if it has aggregating stages, but for the
			// common case (a filtered sub-pipeline) the source columns persist.
			rightSchema = b.sourceSchema(s.Right.Source)
		}
		// ON-conditions reference left + right columns via $left/$right (which
		// the binder treats permissively) or unqualified (left-default).
		for _, c := range s.On {
			b.checkExpr(c, in)
		}
		// The join's OUTPUT schema is the union of left + right columns, so that
		// a following `project region` (right-only) resolves. Both sides'
		// columns are visible post-join (KQL join semantics).
		if in != nil {
			merged := &Schema{}
			merged.Cols = append(merged.Cols, in.Cols...)
			if rightSchema != nil {
				merged.Cols = append(merged.Cols, rightSchema.Cols...)
			}
			return merged
		}
		return in
	case *ir.Union:
		for _, in2 := range s.Inputs {
			_, _ = b.bindPipeline(in2)
		}
		return in
	}
	return in
}

// projectBinding builds the output binding for a Project column. A named
// binding (`s = state`) gets a fresh ColID; a bare column reference inherits
// the source binding (same ColID, same physical name).
func (b *binder) projectBinding(n *ir.NamedExpr, in *Schema) ColBinding {
	if n == nil {
		return ColBinding{ColID: b.alloc()}
	}
	if n.Name != "" {
		return ColBinding{ColID: b.alloc(), PhysicalName: n.Name, DisplayName: n.Name}
	}
	// Bare expression: if it's a single column, inherit its binding.
	if col, ok := n.Expr.(*ir.Col); ok {
		if bd, ok := in.Lookup(col.Name); ok {
			return bd
		}
	}
	return ColBinding{ColID: b.alloc(), PhysicalName: exprName(n.Expr), DisplayName: exprName(n.Expr)}
}

// namedBinding builds the output binding for a summarize key/agg NamedExpr.
// Named → fresh ColID + that name; bare col → inherit source binding.
func (b *binder) namedBinding(n *ir.NamedExpr, in *Schema) ColBinding {
	if n == nil {
		return ColBinding{ColID: b.alloc()}
	}
	if n.Name != "" {
		return ColBinding{ColID: b.alloc(), PhysicalName: n.Name, DisplayName: n.Name}
	}
	if col, ok := n.Expr.(*ir.Col); ok {
		if bd, ok := in.Lookup(col.Name); ok {
			return bd
		}
	}
	return ColBinding{ColID: b.alloc(), PhysicalName: exprName(n.Expr), DisplayName: exprName(n.Expr)}
}

// exprName returns a display name for a bare expression (used when an unnamed
// projection/agg can't inherit a column binding).
func exprName(e ir.Expr) string {
	if col, ok := e.(*ir.Col); ok {
		return col.Name
	}
	return ""
}

// checkExpr walks an expression resolving *ir.Col references. On hit it writes
// the ColID and rewrites Col.Name to the backend's PHYSICAL name (so emit is
// dialect-correct). On miss it records KQL001.
func (b *binder) checkExpr(e ir.Expr, in *Schema) {
	if e == nil {
		return
	}
	switch n := e.(type) {
	case *ir.Col:
		// $left / $right are join-side qualifiers; they're valid only in join ON
		// conditions (handled by emitJoinOnExpr). The binder treats them as
		// permissively present so they don't trigger KQL001 here.
		if n.Name == "$left" || n.Name == "$right" {
			return
		}
		bd, ok := in.Lookup(n.Name)
		if !ok {
			b.errorf(n.Pos(), "column %q not found in current scope", n.Name)
			return
		}
		// RESOLVE: stamp the ColID, rewrite to physical name, set the type.
		n.ColID = bd.ColID
		n.Name = bd.PhysicalName
		n.T = bd.Type
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
