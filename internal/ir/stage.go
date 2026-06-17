package ir

import "nzinfo/kql/internal/frontend/token"

// Column describes one output column of a source or stage. ColID is the stable
// physical identifier (DESIGN.md §5 / I1.S3) — backends emit columns by ColID,
// never by name, to avoid cross-dialect case/reserved-word/quoting issues.
type Column struct {
	ColID ColID  // Stable physical column identifier (binder-assigned)
	Name  string // Display name (diagnostics, pretty-print, output header)
	Type  Type   // Data type (set by binder; Unknown pre-bind)
}

// ColID is a stable integer identifier for a column within a pipeline. Same
// name in different tables has different ColIDs. Zero is reserved invalid.
type ColID uint32

// Invalid is the zero ColID, representing an unbound/invalid column reference.
const Invalid ColID = 0

// IsValid reports whether the ColID is bound.
func (c ColID) IsValid() bool { return c != Invalid }

// JoinKind enumerates join kinds. Mirrors ast.JoinKind but lives in IR so the
// backends don't depend on the frontend ast package.
type JoinKind int

// Join kinds.
const (
	JoinDefault JoinKind = iota
	JoinInnerUnique
	JoinInner
	JoinLeftOuter
	JoinRightOuter
	JoinFullOuter
)

// SortKey is one ordering key: an expression with direction and null placement.
type SortKey struct {
	Expr       Expr
	Desc       bool // ascending if false
	NullsFirst bool
}

// Stage implementations (P0). Position fields are named `Position` to avoid
// clashing with the Node.Pos() interface method. Non-P0 stages will be added
// as the parser and translator grow; the interface won't change.

// Filter is `| where <expr>` (predicate over input columns).
type Filter struct {
	Position  token.Pos
	Predicate Expr
}

// Pos returns the stage position.
func (s *Filter) Pos() token.Pos { return s.Position }

// Project selects/renames columns: `| project c1 = e1, c2, …`. Cols are the
// projected named expressions; the output schema is exactly Cols (in order).
type Project struct {
	Position token.Pos
	Cols     []*NamedExpr
}

// Pos returns the stage position.
func (s *Project) Pos() token.Pos { return s.Position }

// Extend appends computed columns, keeping all input columns:
// `| extend c1 = e1, …`.
type Extend struct {
	Position token.Pos
	Cols     []*NamedExpr
}

// Pos returns the stage position.
func (s *Extend) Pos() token.Pos { return s.Position }

// Aggregate is `| summarize <aggs> by <keys>`: groups by Keys, produces one
// output row per group with the aggregate expressions.
type Aggregate struct {
	Position   token.Pos
	Aggregates []*NamedExpr // aggregate expressions (e.g. count(), sum(x))
	Keys       []*NamedExpr // group-by keys (may include bin() calls)
}

// Pos returns the stage position.
func (s *Aggregate) Pos() token.Pos { return s.Position }

// Join combines two pipelines on equality conditions.
type Join struct {
	Position token.Pos
	Kind     JoinKind   // innerunique/inner/left/right/full
	Right    *Pipeline  // right side (sub-pipeline or table ref)
	On       []Expr     // join conditions (typically Col == Col)
}

// Pos returns the stage position.
func (s *Join) Pos() token.Pos { return s.Position }

// Sort orders rows: `| sort by k1 [desc], k2 …`.
type Sort struct {
	Position token.Pos
	Keys     []SortKey
}

// Pos returns the stage position.
func (s *Sort) Pos() token.Pos { return s.Position }

// Limit is `| take N` / `| limit N`.
type Limit struct {
	Position token.Pos
	Count    Expr // integer expression (typically a literal)
}

// Pos returns the stage position.
func (s *Limit) Pos() token.Pos { return s.Position }

// Union concatenates pipelines: `| union T1, T2, …`.
type Union struct {
	Position token.Pos
	Inputs   []*Pipeline // additional inputs (the first is the pipeline itself)
}

// Pos returns the stage position.
func (s *Union) Pos() token.Pos { return s.Position }

// Distinct projects distinct rows over a column set: `| distinct c1, c2, *`.
type Distinct struct {
	Position token.Pos
	Cols     []Expr // projected columns (may include a Star)
}

// Pos returns the stage position.
func (s *Distinct) Pos() token.Pos { return s.Position }

// Stage markers.
func (*Filter) node()    {}
func (*Filter) stage()   {}
func (*Project) node()   {}
func (*Project) stage()  {}
func (*Extend) node()    {}
func (*Extend) stage()   {}
func (*Aggregate) node() {}
func (*Aggregate) stage() {}
func (*Join) node()      {}
func (*Join) stage()     {}
func (*Sort) node()      {}
func (*Sort) stage()     {}
func (*Limit) node()     {}
func (*Limit) stage()    {}
func (*Union) node()     {}
func (*Union) stage()    {}
func (*Distinct) node()  {}
func (*Distinct) stage() {}
