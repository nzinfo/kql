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

// translateNamedList converts a slice of AST NamedExpr to IR NamedExpr.
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
// table reference or a parenthesised sub-pipeline.
func (t *translator) translateJoin(n *ast.JoinOp) *Join {
	var right *Pipeline
	if n.Right != nil {
		// If the right side is a parenthesised pipeline expression, translate
		// it; else wrap a bare table ref as a single-source pipeline.
		if subPipe, ok := n.Right.(*ast.Pipeline); ok {
			right = t.translatePipeline(subPipe)
		} else {
			right = &Pipeline{
				Position: n.Right.Pos(),
				Source:   t.translateSource(n.Right),
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
