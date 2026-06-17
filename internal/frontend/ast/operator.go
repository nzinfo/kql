package ast

import "nzinfo/kql/internal/frontend/token"

// JoinKind enumerates the kinds of joins KQL supports. Values mirror the
// Kusto-Query-Language/grammar join kind=... clause.
type JoinKind int

// Join kinds.
const (
	JoinDefault    JoinKind = iota // unspecified (KQL default is innerunique)
	JoinInnerUnique                // kind=innerunique
	JoinInner                      // kind=inner
	JoinLeftOuter                  // kind=leftouter (KQL "left")
	JoinRightOuter                 // kind=rightouter
	JoinFullOuter                  // kind=fullouter
)

// OrderExpr is one key in an `order by` / `sort by` clause: Expr [asc|desc]
// [nulls first|last].
type OrderExpr struct {
	Expr  Expr        // Sort key expression
	Order token.Token // ASC, DESC, or ILLEGAL for default
	Nulls token.Token // FIRST, LAST, or ILLEGAL for default
}

// Pos returns the sort key's start.
func (o *OrderExpr) Pos() token.Pos { return o.Expr.Pos() }

// End returns one past the end of the sort key (modifiers are bare keywords
// with no payload, so the key's end is a tight lower bound).
func (o *OrderExpr) End() token.Pos { return o.Expr.End() }

// OperatorParam represents a key=value hint/parameter on an operator, e.g.
// `kind=inner`, `hint.strategy=broadcast`, `withsource=TableName`.
type OperatorParam struct {
	Name   *Ident    // Parameter name (e.g. "hint.strategy", "kind")
	Assign token.Pos // Position of "="
	Value  Expr      // Parameter value (identifier or literal)
}

// Pos returns the name's start.
func (p *OperatorParam) Pos() token.Pos { return p.Name.Pos() }

// End returns one past the value's end.
func (p *OperatorParam) End() token.Pos { return p.Value.End() }

// ---- P0 tabular operators ----
//
// Per F2.S4 only the P0 operators (DESIGN.md §10) are defined here; the parser
// fills their fields in F4. Non-P0 operators (parse, mv-expand, make-series,
// graph-*, scan, evaluate, …) will be added as the parser grows. See the full
// kqlparser operator set (~55 types) for the eventual target surface.

// SourceExpr is the implicit table source at the head of a pipeline, e.g. the
// `StormEvents` in `StormEvents | where …`. Expr is typically an *Ident, a
// dotted database/table reference, or a parenthesised sub-pipeline.
type SourceExpr struct {
	Expr Expr
}

// Pos returns the source expression's start.
func (s *SourceExpr) Pos() token.Pos { return s.Expr.Pos() }

// End returns one past the source expression's end.
func (s *SourceExpr) End() token.Pos { return s.Expr.End() }

// WhereOp filters rows: `| where Predicate`.
type WhereOp struct {
	Pipe      token.Pos // Position of "|"
	Where     token.Pos // Position of "where" (or "filter")
	Predicate Expr      // Filter predicate
}

// Pos returns the position of "|".
func (x *WhereOp) Pos() token.Pos { return x.Pipe }

// End returns one past the end of the predicate.
func (x *WhereOp) End() token.Pos { return x.Predicate.End() }

// ProjectOp selects/renames columns: `| project c1 = e1, c2, …`.
type ProjectOp struct {
	Pipe    token.Pos    // Position of "|"
	Project token.Pos    // Position of "project"
	Columns []*NamedExpr // Projected columns (named or bare expressions)
}

// Pos returns the position of "|".
func (x *ProjectOp) Pos() token.Pos { return x.Pipe }

// End returns one past the end of the last column, or the project keyword if empty.
func (x *ProjectOp) End() token.Pos {
	if len(x.Columns) > 0 {
		return x.Columns[len(x.Columns)-1].End()
	}
	return token.Pos(int(x.Project) + len("project"))
}

// ExtendOp appends computed columns: `| extend c1 = e1, c2 = e2, …`.
type ExtendOp struct {
	Pipe    token.Pos    // Position of "|"
	Extend  token.Pos    // Position of "extend"
	Columns []*NamedExpr // Extended columns
}

// Pos returns the position of "|".
func (x *ExtendOp) Pos() token.Pos { return x.Pipe }

// End returns one past the end of the last column, or the extend keyword if empty.
func (x *ExtendOp) End() token.Pos {
	if len(x.Columns) > 0 {
		return x.Columns[len(x.Columns)-1].End()
	}
	return token.Pos(int(x.Extend) + len("extend"))
}

// TakeOp limits rows: `| take Count` (alias: `| limit Count`).
type TakeOp struct {
	Pipe  token.Pos // Position of "|"
	Take  token.Pos // Position of "take" or "limit"
	Count Expr      // Number of rows to take
}

// Pos returns the position of "|".
func (x *TakeOp) Pos() token.Pos { return x.Pipe }

// End returns one past the end of the count expression.
func (x *TakeOp) End() token.Pos { return x.Count.End() }

// SortOp orders rows: `| sort by k1 [desc] [nulls first], …`
// (alias: `| order by …`).
type SortOp struct {
	Pipe   token.Pos        // Position of "|"
	Sort   token.Pos        // Position of "sort" or "order"
	Params []*OperatorParam // Operator parameters (hints)
	ByPos  token.Pos        // Position of "by"
	Orders []*OrderExpr     // Sort keys with modifiers
}

// Pos returns the position of "|".
func (x *SortOp) Pos() token.Pos { return x.Pipe }

// End returns one past the last sort key, or "by" if no keys.
func (x *SortOp) End() token.Pos {
	if len(x.Orders) > 0 {
		return x.Orders[len(x.Orders)-1].End()
	}
	return token.Pos(int(x.ByPos) + len("by"))
}

// SummarizeOp aggregates: `| summarize agg1 = f(..), .. by k1, k2, ..`.
type SummarizeOp struct {
	Pipe       token.Pos        // Position of "|"
	Summarize  token.Pos        // Position of "summarize"
	Params     []*OperatorParam // Operator parameters (hints)
	Aggregates []*NamedExpr     // Aggregate expressions (named or bare)
	ByPos      token.Pos        // Position of "by" (NoPos if no by clause)
	GroupBy    []*NamedExpr     // Group-by keys
}

// Pos returns the position of "|".
func (x *SummarizeOp) Pos() token.Pos { return x.Pipe }

// End returns one past the last token of the operator.
func (x *SummarizeOp) End() token.Pos {
	if len(x.GroupBy) > 0 {
		return x.GroupBy[len(x.GroupBy)-1].End()
	}
	if len(x.Aggregates) > 0 {
		return x.Aggregates[len(x.Aggregates)-1].End()
	}
	return token.Pos(int(x.Summarize) + len("summarize"))
}

// JoinOp joins with another table:
// `| join kind=inner (Right) on Cond1, Cond2`.
type JoinOp struct {
	Pipe   token.Pos        // Position of "|"
	Join   token.Pos        // Position of "join"
	Params []*OperatorParam // Parameters (kind=…, hints)
	Kind   JoinKind         // Join kind (derived from kind parameter)
	Right  Expr             // Right side (table ref or sub-pipeline)
	OnPos  token.Pos        // Position of "on" (NoPos if no on clause)
	OnExpr []Expr           // Join conditions (equality predicates)
}

// Pos returns the position of "|".
func (x *JoinOp) Pos() token.Pos { return x.Pipe }

// End returns one past the end of the last on-condition, or the right side.
func (x *JoinOp) End() token.Pos {
	if len(x.OnExpr) > 0 {
		return x.OnExpr[len(x.OnExpr)-1].End()
	}
	return x.Right.End()
}

// UnionOp concatenates tables: `| union T1, T2, [T3, …]` (with optional
// withsource= and isfuzzy parameters).
type UnionOp struct {
	Pipe   token.Pos        // Position of "|"
	Union  token.Pos        // Position of "union"
	Params []*OperatorParam // Parameters (withsource=, isfuzzy)
	Tables []Expr           // Tables to union
}

// Pos returns the position of "|".
func (x *UnionOp) Pos() token.Pos { return x.Pipe }

// End returns one past the last table, or the union keyword if none.
func (x *UnionOp) End() token.Pos {
	if len(x.Tables) > 0 {
		return x.Tables[len(x.Tables)-1].End()
	}
	return token.Pos(int(x.Union) + len("union"))
}

// DistinctOp projects distinct rows over a column set: `| distinct c1, c2, *`.
type DistinctOp struct {
	Pipe     token.Pos // Position of "|"
	Distinct token.Pos // Position of "distinct"
	Columns  []Expr    // Columns to make distinct (may include *StarExpr)
}

// Pos returns the position of "|".
func (x *DistinctOp) Pos() token.Pos { return x.Pipe }

// End returns one past the last column, or the distinct keyword if none.
func (x *DistinctOp) End() token.Pos {
	if len(x.Columns) > 0 {
		return x.Columns[len(x.Columns)-1].End()
	}
	return token.Pos(int(x.Distinct) + len("distinct"))
}

// CountOp counts rows: `| count`. It is a standalone operator (no operands).
type CountOp struct {
	Pipe  token.Pos // Position of "|"
	Count token.Pos // Position of "count"
}

// Pos returns the position of "|".
func (x *CountOp) Pos() token.Pos { return x.Pipe }

// End returns one past "count".
func (x *CountOp) End() token.Pos { return token.Pos(int(x.Count) + len("count")) }

// TopOp takes the top N rows ordered by keys: `| top N by k [desc]`.
type TopOp struct {
	Pipe   token.Pos        // Position of "|"
	Top    token.Pos        // Position of "top"
	Count  Expr             // Number of rows
	ByPos  token.Pos        // Position of "by"
	Orders []*OrderExpr     // Sort keys
	Params []*OperatorParam // Hints (hint.progressive_top, …)
}

// Pos returns the position of "|".
func (x *TopOp) Pos() token.Pos { return x.Pipe }

// End returns one past the last sort key.
func (x *TopOp) End() token.Pos {
	if len(x.Orders) > 0 {
		return x.Orders[len(x.Orders)-1].End()
	}
	return x.Count.End()
}

// Operator-node markers. SourceExpr is an Expr, not an Operator (it heads the
// pipeline rather than piping in), so it gets expr() — see stmt.go's Pipeline.
func (*SourceExpr) node() {}
func (*SourceExpr) expr() {}

func (*WhereOp) node()     {}
func (*WhereOp) operator() {}
func (*ProjectOp) node()   {}
func (*ProjectOp) operator() {}
func (*ExtendOp) node()    {}
func (*ExtendOp) operator() {}
func (*TakeOp) node()      {}
func (*TakeOp) operator() {}
func (*SortOp) node()      {}
func (*SortOp) operator() {}
func (*SummarizeOp) node() {}
func (*SummarizeOp) operator() {}
func (*JoinOp) node()      {}
func (*JoinOp) operator() {}
func (*UnionOp) node()     {}
func (*UnionOp) operator() {}
func (*DistinctOp) node()  {}
func (*DistinctOp) operator() {}
func (*CountOp) node()     {}
func (*CountOp) operator() {}
func (*TopOp) node()       {}
func (*TopOp) operator() {}

// OrderExpr and OperatorParam are not Node-implementing themselves; they are
// payload structs inside SortOp/JoinOp/SummarizeOp/TopOp.
