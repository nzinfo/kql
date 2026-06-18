package ir

import (
	"nzinfo/kql/internal/frontend/ast"
	"nzinfo/kql/internal/frontend/token"
)

// translateStage converts an AST tabular operator to an IR Stage (or, for top,
// appends multiple stages). Returns the primary stage; for top the caller
// (translatePipeline) handles the extra Limit. P0 operators covered.
func (t *translator) translateStage(op ast.Operator) Stage {
	if op == nil {
		return nil
	}
	switch n := op.(type) {
	case *ast.WhereOp:
		return &Filter{Position: n.Pos(), Predicate: t.translateExpr(n.Predicate)}

	case *ast.ProjectOp:
		return &Project{Position: n.Pos(), Cols: t.translateNamedList(n.Columns)}

	case *ast.ExtendOp:
		return &Extend{Position: n.Pos(), Cols: t.translateNamedList(n.Columns)}

	case *ast.TakeOp:
		return &Limit{Position: n.Pos(), Count: t.translateExpr(n.Count)}

	case *ast.SortOp:
		return &Sort{Position: n.Pos(), Keys: t.translateSortKeys(n.Orders)}

	case *ast.SummarizeOp:
		return &Aggregate{
			Position:   n.Pos(),
			Aggregates: t.translateNamedList(n.Aggregates),
			Keys:       t.translateNamedList(n.GroupBy),
		}

	case *ast.JoinOp:
		return t.translateJoin(n)

	case *ast.UnionOp:
		return t.translateUnion(n)

	case *ast.DistinctOp:
		cols := make([]Expr, len(n.Columns))
		for i, c := range n.Columns {
			cols[i] = t.translateExpr(c)
		}
		return &Distinct{Position: n.Pos(), Cols: cols}

	case *ast.CountOp:
		// `| count` == summarize count(). Represent as an Aggregate with a
		// single count() FuncCall and no group keys.
		return &Aggregate{
			Position: n.Pos(),
			Aggregates: []*NamedExpr{
				{Position: n.Pos(), Name: "Count_",
					Expr: &FuncCall{Position: n.Pos(), Name: "count", Caps: DefaultCaps("count", true)}},
			},
		}

	// P1 operators: parse + translate, best-effort emit (see NOTES.md §6).
	case *ast.RenderOp:
		// Presentation/control-flow hint — drop at emit (no-op).
		return t.translatePassthrough(n.Pos())
	case *ast.ConsumeOp:
		// `| consume` discards rows — represent as a Limit 0 so emit yields no rows.
		return &Limit{Position: n.Pos(), Count: &Lit{Position: n.Pos(), T: TypeLong, Value: int64(0), HasValue: true}}
	case *ast.GetSchemaOp:
		// Schema introspection — no SQL equivalent in the minimal loop; pass-through.
		return t.translatePassthrough(n.Pos())
	case *ast.SerializeOp:
		// Forces ordering; if columns given, behaves like project (ordered).
		if len(n.Cols) > 0 {
			return &Project{Position: n.Pos(), Cols: t.translateNamedList(n.Cols)}
		}
		return t.translatePassthrough(n.Pos())
	case *ast.ExternalDataOp:
		// External source — best-effort pass-through (real impl needs the
		// storage connector; flagged NeedsPostProc).
		return t.translatePassthrough(n.Pos())
	case *ast.AsOp:
		// `| as Name` binds a name to the current result; rows are unchanged.
		// The name lives in the query's symbol table (for later `invoke` /
		// re-reference), not in the SQL — so this is a row-wise no-op.
		return t.translatePassthrough(n.Pos())
	case *ast.InvokeOp:
		// `| invoke F(...)` calls a stored function/plugin. Real semantics need
		// a function registry + PostProc; emit pass-through for now.
		return t.translatePassthrough(n.Pos())
	case *ast.MvExpandOp:
		// mv-expand: explode array/dynamic column → multiple rows. Emit a real
		// MvExpand IR stage (PostProc — client-side for sqlite, future lateral
		// join for pg/duckdb). The first column's expr is the source array;
		// its name is the exploded output column.
		if len(n.Cols) > 0 && n.Cols[0].Name != nil {
			return &MvExpand{
				Position: n.Pos(),
				ColName:  n.Cols[0].Name.Name,
				Source:   t.translateExpr(n.Cols[0].Expr),
			}
		}
		return t.translatePassthrough(n.Pos())
	case *ast.MvApplyOp:
		// mv-apply: iterate an array column, apply a sub-pipeline to each element.
		// Emit a MvApply IR stage (PostProc boundary — client-side iteration).
		colName := ""
		var src Expr
		if len(n.Cols) > 0 && n.Cols[0].Name != nil {
			colName = n.Cols[0].Name.Name
			src = t.translateExpr(n.Cols[0].Expr)
		}
		return &MvApply{
			Position: n.Pos(),
			ColName:  colName,
			Source:   src,
			OnPipe:   translatePipelineExpr(t, n.OnExpr),
		}
	// P2/P3 operators — parsed to AST for full grammar coverage, translated as
	// pass-through. Real semantics need PostProc / lateral joins / plugins.
	case *ast.PrintOp, *ast.RangeOp, *ast.FindOp, *ast.SampleOp,
		*ast.SampleDistinctOp, *ast.LookupOp, *ast.ScanOp, *ast.ForkOp,
		*ast.FacetOp, *ast.ReduceOp, *ast.TopHittersOp, *ast.PartitionOp,
		*ast.MacroExpandOp, *ast.ExecuteAndCacheOp, *ast.AssertSchemaOp,
		*ast.GraphMatchOp, *ast.MakeGraphOp, *ast.GraphShortestPathsOp,
		*ast.GraphToTableOp, *ast.GraphMarkComponentsOp:
		return t.translatePassthrough(n.Pos())
	case *ast.MakeSeriesOp:
		// make-series: time-series aggregation. Emit a MakeSeries IR stage
		// (PostProc — client-side bin + series fill).
		return &MakeSeries{
			Position:   n.Pos(),
			Aggregates: t.translateNamedList(n.Aggregates),
			On:         t.translateExpr(n.OnExpr),
			From:       t.translateOptExpr(n.From),
			To:         t.translateOptExpr(n.To),
			Step:       t.translateOptExpr(n.Step),
			ByKeys:     t.translateNamedList(n.ByKeys),
		}
	case *ast.ParseOp:
		// parse [Kind] Target with Pattern: regex/string extraction into new
		// columns. Emit a real Parse IR stage (PostProc — client-side regex).
		return &Parse{
			Position: n.Pos(),
			Kind:     n.Kind,
			Target:   t.translateExpr(n.Target),
			Pattern:  n.Pattern,
			IsWhere:  n.IsWhere,
		}
	}
	t.errorf(op.Pos(), "KQL010: unsupported operator %T in IR translation", op)
	return nil
}

// translateTopOp is handled specially by translatePipeline because top expands
// to two IR stages (Sort + Limit). Returns the stages in order.
func (t *translator) translateTopOp(n *ast.TopOp) []Stage {
	keys := t.translateSortKeys(n.Orders)
	sort := &Sort{Position: n.Pos(), Keys: keys}
	limit := &Limit{Position: n.Pos(), Count: t.translateExpr(n.Count)}
	return []Stage{sort, limit}
}

// translatePassthrough returns a no-op stage (Project of *Star) for operators
// the minimal loop parses but can't emit meaningfully (render/consume-passthrough/
// getschema/mv-expand/make-series/parse/externaldata/evaluate). It keeps the
// pipeline shape intact so downstream stages still resolve columns; the actual
// semantics are flagged NeedsPostProc (see NOTES.md §6).
func (t *translator) translatePassthrough(pos token.Pos) Stage {
	return &Project{Position: pos, Cols: []*NamedExpr{{Position: pos, Expr: &Star{Position: pos}}}}
}

// translateNamedList converts a slice of AST NamedExpr to IR NamedExpr.
// translateOptExpr translates an optional AST expression (nil → nil).
func (t *translator) translateOptExpr(e ast.Expr) Expr {
	if e == nil {
		return nil
	}
	return t.translateExpr(e)
}

func (t *translator) translateNamedList(in []*ast.NamedExpr) []*NamedExpr {
	out := make([]*NamedExpr, len(in))
	for i, n := range in {
		name := ""
		if n.IsNamed() && n.Name != nil {
			name = n.Name.Name
		}
		out[i] = &NamedExpr{Position: n.Pos(), Name: name, Expr: t.translateExpr(n.Expr)}
	}
	return out
}

// translateSortKeys converts AST OrderExpr list to IR SortKey list.
func (t *translator) translateSortKeys(in []*ast.OrderExpr) []SortKey {
	out := make([]SortKey, len(in))
	for i, o := range in {
		out[i] = SortKey{
			Expr:       t.translateExpr(o.Expr),
			Desc:       o.Order == token.DESC,
			NullsFirst: o.Nulls == token.FIRST,
		}
	}
	return out
}

// translateJoin converts an AST JoinOp to an IR Join. The right side may be a
// table reference, a parenthesised sub-pipeline (ParenExpr{X: *Pipeline}), or
// a bare Pipeline.
func (t *translator) translateJoin(n *ast.JoinOp) *Join {
	var right *Pipeline
	if n.Right != nil {
		rightExpr := n.Right
		// Unwrap a ParenExpr to find the inner Pipeline if present.
		if pe, ok := rightExpr.(*ast.ParenExpr); ok {
			if inner, ok := pe.X.(*ast.Pipeline); ok {
				rightExpr = inner
			}
		}
		if subPipe, ok := rightExpr.(*ast.Pipeline); ok {
			right = t.translatePipeline(subPipe)
		} else {
			right = &Pipeline{
				Position: rightExpr.Pos(),
				Source:   t.translateSource(rightExpr),
			}
		}
	}
	on := make([]Expr, len(n.OnExpr))
	for i, c := range n.OnExpr {
		on[i] = t.translateExpr(c)
	}
	return &Join{
		Position: n.Pos(),
		Kind:     mapJoinKind(n.Kind),
		Right:    right,
		On:       on,
	}
}

// mapJoinKind converts ast.JoinKind to ir.JoinKind.
func mapJoinKind(k ast.JoinKind) JoinKind {
	switch k {
	case ast.JoinInnerUnique:
		return JoinInnerUnique
	case ast.JoinInner:
		return JoinInner
	case ast.JoinLeftOuter:
		return JoinLeftOuter
	case ast.JoinRightOuter:
		return JoinRightOuter
	case ast.JoinFullOuter:
		return JoinFullOuter
	}
	return JoinDefault
}

// translateUnion converts an AST UnionOp to an IR Union. Each table becomes a
// one-source Pipeline.
func (t *translator) translateUnion(n *ast.UnionOp) *Union {
	inputs := make([]*Pipeline, len(n.Tables))
	for i, tbl := range n.Tables {
		if subPipe, ok := tbl.(*ast.Pipeline); ok {
			inputs[i] = t.translatePipeline(subPipe)
		} else {
			inputs[i] = &Pipeline{Position: tbl.Pos(), Source: t.translateSource(tbl)}
		}
	}
	return &Union{Position: n.Pos(), Inputs: inputs}
}


// translatePipelineExpr translates an ast.Expr that is expected to be a
// sub-pipeline (used by mv-apply's ON clause). Returns nil if the expression
// is not a pipeline (the executor then treats mv-apply as a passthrough).
func translatePipelineExpr(t *translator, e ast.Expr) *Pipeline {
	if e == nil {
		return nil
	}
	// The ON expression for mv-apply is a pipeline in expression position.
	// We attempt to translate it; if it's not a recognizable pipeline, return nil.
	return nil // TODO: full pipeline-lambda translation (placeholder safe)
}
