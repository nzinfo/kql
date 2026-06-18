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

	"nzinfo/kql/internal/frontend/builtin"
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
	return BindWith(pipe, prov, diags, Options{})
}

// BindWith is the configurable entry point (F5.S7). Options control strictness:
// Strict=true escalates KQL001/KQL003 from warnings to errors.
func BindWith(pipe *ir.Pipeline, prov SchemaProvider, diags *diagnostic.List, opts Options) (*ir.Pipeline, error) {
	b := &binder{prov: prov, diags: diags, opts: opts, scope: NewScope(nil)}
	return b.bindPipeline(pipe)
}

// Options configures the binder (F5.S7 strict mode).
type Options struct {
	// Strict escalates warnings (KQL002/KQL003/KQL004) to errors, blocking
	// execution. Default false — KQL's dynamic type system means warnings are
	// advisory, not blocking.
	Strict bool
}

type binder struct {
	prov  SchemaProvider
	diags *diagnostic.List
	opts  Options
	next  ir.ColID // ColID allocator counter (1-based; 0 = Invalid)
	scope *Scope   // F5.S1 scope stack (let-bindings + columns)
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
			// `project *` (and the pass-through form emitted by render/as/
			// invoke/getschema/externaldata/mv-expand/...) carries ALL input
			// columns forward unchanged.
			if _, ok := c.Expr.(*ir.Star); ok {
				if in != nil {
					out.Cols = append(out.Cols, in.Cols...)
				}
				continue
			}
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
		b.inferExprType(n) // F5.S4: set result type + KQL002 on mismatch
	case *ir.UnaryOp:
		b.checkExpr(n.X, in)
		b.inferExprType(n)
	case *ir.FuncCall:
		b.checkFuncCall(n)
		for _, a := range n.Args {
			b.checkExpr(a, in)
		}
		b.inferExprType(n)
	case *ir.Member:
		b.checkExpr(n.X, in)
	case *ir.Index:
		b.checkExpr(n.X, in)
		b.checkExpr(n.Index, in)
	case *ir.Case:
		b.checkExpr(n.Cond, in)
		b.checkExpr(n.Then, in)
		b.checkExpr(n.Else, in)
		b.inferExprType(n)
	case *ir.List:
		for _, el := range n.Elems {
			b.checkExpr(el, in)
		}
	case *ir.Lit, *ir.Star:
		// leaves — no column refs
	}
}

// checkFuncCall validates a function call against the builtin catalog (F5.S6).
// Two checks:
//   - KQL003 UnknownFunction: the name isn't in the catalog. Emitted as a
//     WARNING because unknown functions are frequently user-defined (via let)
//     or aggregates the catalog hasn't catalogued; the emit layer handles them
//     via best-effort pass-through, so this must NOT block execution.
//   - KQL004 ArgCount: the argument count is outside [MinArgs, MaxArgs]. Also a
//     WARNING for the same reason.
//
// Both are skipped when there is no diagnostic sink (binder used standalone).
func (b *binder) checkFuncCall(n *ir.FuncCall) {
	if b.diags == nil || n == nil || n.Name == "" {
		return
	}
	spec := builtin.Lookup(n.Name)
	if spec == nil {
		b.diagAt(n.Pos(), diagnostic.Warning, diagnostic.UnknownFunction,
			"function %q is not in the builtin catalog (may be user-defined; will pass through)",
			n.Name)
		return
	}
	argc := len(n.Args)
	if argc < spec.MinArgs || (spec.MaxArgs >= 0 && argc > spec.MaxArgs) {
		b.diagAt(n.Pos(), diagnostic.Warning, diagnostic.ArgCount,
			"function %q called with %d arg(s); expected %s",
			n.Name, argc, arityRange(spec.MinArgs, spec.MaxArgs))
	}
	// I2.S4: fill Caps from the F7 Spec table so the emit layer knows whether
	// this function NeedsPostProc (e.g. split, percentile, make-series). The
	// translator sets Caps via DefaultCaps(name, isAgg) which doesn't know about
	// PostProc — we enrich it here after the catalog lookup.
	if spec.NeedsPostProc {
		n.Caps.NeedsPostProc = true
	}
}

// arityRange renders a human-readable arity spec for KQL004 messages.
func arityRange(min, max int) string {
	if max < 0 {
		return fmt.Sprintf("at least %d", min)
	}
	if min == max {
		return fmt.Sprintf("%d", min)
	}
	return fmt.Sprintf("%d to %d", min, max)
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

// diagAt records a diagnostic with a caller-chosen code and severity. Used for
// KQL002/KQL003/KQL004 which are WARNINGS by default — they must not block
// execution, because KQL's dynamic type system means unknown functions/types
// may resolve at runtime and the emit layer handles them via best-effort
// pass-through.
//
// In StrictMode (F5.S7), warnings escalate to errors so the caller can enforce
// rigor (e.g. CI validation pipelines).
func (b *binder) diagAt(pos token.Pos, sev diagnostic.Severity, code diagnostic.Code, format string, args ...interface{}) {
	if b.diags == nil {
		return
	}
	if b.opts.Strict && sev == diagnostic.Warning {
		sev = diagnostic.Error
	}
	b.diags.Add(diagnostic.Diagnostic{
		Severity: sev,
		Code:     code,
		Pos:      token.Position{Offset: int(pos) - 1},
		Message:  fmt.Sprintf(format, args...),
	})
}
