package ast

import "nzinfo/kql/internal/frontend/token"

// P1+ tabular operator nodes. These are added to keep real-world queries
// parsing past P0; their semantics are partially implemented (see NOTES.md §6).
// Each carries its position + the structurally-parsed fields.

// ParseOp is `| parse [Kind=...] Expr with <pattern>` / `| parse-where ...`.
// Pattern is the raw text between `with` and the next operator boundary; full
// pattern-segment parsing (g4 parseOperatorPattern) is deferred — the minimal
// path captures it for round-trip/explain and emits a best-effort regex.
type ParseOp struct {
	Pipe     token.Pos // |
	Parse    token.Pos // parse / parse-where
	Kind     string    // "", "simple", "regex", "relaxed" (from Kind=...)
	Flags    string    // flags (from Flags=...)
	Target   Expr      // expression to parse (usually a column)
	WithPos  token.Pos // position of "with"
	Pattern  string    // raw pattern text (between with and end-of-op)
	IsWhere  bool      // true for parse-where (filter-on-match)
}

// Pos returns the position of |.
func (x *ParseOp) Pos() token.Pos { return x.Pipe }

// End returns one past the pattern's last char (best-effort).
func (x *ParseOp) End() token.Pos { return token.Pos(int(x.WithPos) + len("with") + len(x.Pattern)) }

// MvExpandOp is `| mv-expand [kind=array|bag] Name = Expr [to typeof(T)], ... [limit N]`.
type MvExpandOp struct {
	Pipe   token.Pos
	MvExp  token.Pos // mv-expand / mvexpand
	Cols   []*NamedExpr // expanded column expressions (name = expr)
	ToType string    // optional `to typeof(T)` per column (simplified: single)
	Limit  Expr      // optional limit N (nil if absent)
}

// Pos returns the position of |.
func (x *MvExpandOp) Pos() token.Pos { return x.Pipe }

// End returns one past the last column or limit.
func (x *MvExpandOp) End() token.Pos {
	if x.Limit != nil {
		return x.Limit.End()
	}
	if len(x.Cols) > 0 {
		return x.Cols[len(x.Cols)-1].End()
	}
	return token.Pos(int(x.MvExp) + len("mv-expand"))
}

// MakeSeriesOp is `| make-series <aggs> on <col> [from/to/step|in range(...)] [by ...]`.
// Complex; minimal capture keeps the aggregation list + on column + by keys.
type MakeSeriesOp struct {
	Pipe        token.Pos
	MakeSeries  token.Pos
	Aggregates  []*NamedExpr
	OnPos       token.Pos
	OnExpr      Expr
	From, To, Step Expr // from/to/step bounds (any may be nil)
	InRange     bool    // true if `in range (...)` form used
	ByPos       token.Pos
	ByKeys      []*NamedExpr
}

// Pos returns the position of |.
func (x *MakeSeriesOp) Pos() token.Pos { return x.Pipe }

// End returns one past the last by-key or step bound.
func (x *MakeSeriesOp) End() token.Pos {
	if len(x.ByKeys) > 0 {
		return x.ByKeys[len(x.ByKeys)-1].End()
	}
	if x.Step != nil {
		return x.Step.End()
	}
	return x.OnExpr.End()
}

// RenderOp is `| render <chart> [with (...)] / [legacy props]`. A pure
// presentation hint; the minimal path parses and ignores it (no-op at runtime).
type RenderOp struct {
	Pipe        token.Pos
	Render      token.Pos
	ChartKind   string   // table/list/barchart/... or an identifier
	Properties  []*OperatorParam // with (...) or legacy name=value pairs
}

// Pos returns the position of |.
func (x *RenderOp) Pos() token.Pos { return x.Pipe }

// End returns one past the last property or the chart kind.
func (x *RenderOp) End() token.Pos {
	if len(x.Properties) > 0 {
		return x.Properties[len(x.Properties)-1].End()
	}
	return token.Pos(int(x.Render) + len("render") + 1 + len(x.ChartKind))
}

// ConsumeOp is `| consume` — discards results (no-op emit; the backend just
// returns nothing, or the operator is dropped).
type ConsumeOp struct {
	Pipe    token.Pos
	Consume token.Pos
}

// Pos returns the position of |.
func (x *ConsumeOp) Pos() token.Pos { return x.Pipe }

// End returns one past "consume".
func (x *ConsumeOp) End() token.Pos { return token.Pos(int(x.Consume) + len("consume")) }

// GetSchemaOp is `| getschema` — returns the row schema. No-op emit for the
// minimal path (would need a schema-introspection SQL).
type GetSchemaOp struct {
	Pipe      token.Pos
	GetSchema token.Pos
}

// Pos returns the position of |.
func (x *GetSchemaOp) Pos() token.Pos { return x.Pipe }

// End returns one past "getschema".
func (x *GetSchemaOp) End() token.Pos { return token.Pos(int(x.GetSchema) + len("getschema")) }

// SerializeOp is `| serialize [Name = Expr, ...]` — forces row order
// preservation through the pipeline. Minimal: parse + treat as a Project that
// keeps order (a SELECT with the named exprs, or pass-through).
type SerializeOp struct {
	Pipe      token.Pos
	Serialize token.Pos
	Cols      []*NamedExpr
}

// Pos returns the position of |.
func (x *SerializeOp) Pos() token.Pos { return x.Pipe }

// End returns one past the last column or "serialize".
func (x *SerializeOp) End() token.Pos {
	if len(x.Cols) > 0 {
		return x.Cols[len(x.Cols)-1].End()
	}
	return token.Pos(int(x.Serialize) + len("serialize"))
}

// DatatableLit is a datatable(Type,...)[...] literal source. Rows is a list of
// row-expressions (each a comma list). Minimal: captured for round-trip.
type DatatableLit struct {
	Datatable token.Pos
	Lparen    token.Pos
	Schema    []*Ident // column type specs (name? : type) — simplified to idents
	Rows      [][]Expr // each row's cells
	Rparen    token.Pos
}

// Pos returns the position of datatable.
func (x *DatatableLit) Pos() token.Pos { return x.Datatable }

// End returns one past the closing ).
func (x *DatatableLit) End() token.Pos { return token.Pos(int(x.Rparen) + 1) }

// ExternalDataOp is `| externaldata(Schema) [StorageClause]`. Minimal: parse +
// surface as a no-op source.
type ExternalDataOp struct {
	Pipe          token.Pos
	ExternalData  token.Pos
	Schema        []*Ident
	Storage       []Expr // storage clause expressions
}

// Pos returns the position of |.
func (x *ExternalDataOp) Pos() token.Pos { return x.Pipe }

// End returns one past the last storage clause.
func (x *ExternalDataOp) End() token.Pos {
	if len(x.Storage) > 0 {
		return x.Storage[len(x.Storage)-1].End()
	}
	return token.Pos(int(x.ExternalData) + len("externaldata"))
}

// Operator-node markers.
func (*ParseOp) node()        {}
func (*ParseOp) operator()    {}
func (*MvExpandOp) node()     {}
func (*MvExpandOp) operator() {}
func (*MakeSeriesOp) node()   {}
func (*MakeSeriesOp) operator() {}
func (*RenderOp) node()       {}
func (*RenderOp) operator()   {}
func (*ConsumeOp) node()      {}
func (*ConsumeOp) operator() {}
func (*GetSchemaOp) node()    {}
func (*GetSchemaOp) operator() {}
func (*SerializeOp) node()    {}
func (*SerializeOp) operator() {}
func (*ExternalDataOp) node() {}
func (*ExternalDataOp) operator() {}

// AsOp is `| as Name` (g4 asOperator). It binds a name to the current result so
// the pipeline can be referenced later (e.g. as a sub-expression, or for
// `invoke`). It carries optional operator parameters (`| as (hint.remote=true)
// Name`) per the grammar; those are ignored at emit. Semantically it is a
// no-op over rows — the name lives in the query's symbol table, not the SQL.
type AsOp struct {
	Pipe   token.Pos
	As     token.Pos
	Params []*OperatorParam // optional ( ... ) before the name
	Name   *Ident
}

// Pos returns the position of |.
func (x *AsOp) Pos() token.Pos { return x.Pipe }

// End returns one past the name.
func (x *AsOp) End() token.Pos {
	if x.Name != nil {
		return x.Name.End()
	}
	return token.Pos(int(x.As) + len("as"))
}

// InvokeOp is `| invoke FunctionName(args...)` (g4 invokeOperator). It calls a
// stored function / plugin on the current rowset. For the minimal loop we parse
// the call and treat it as a pass-through at emit (real semantics need a
// function registry; flagged via the translator's NeedsPostProc path).
type InvokeOp struct {
	Pipe token.Pos
	Invoke token.Pos
	Call *CallExpr // the dotCompositeFunctionCallExpression, captured as a call
}

// Pos returns the position of |.
func (x *InvokeOp) Pos() token.Pos { return x.Pipe }

// End returns one past the call expression.
func (x *InvokeOp) End() token.Pos {
	if x.Call != nil {
		return x.Call.End()
	}
	return token.Pos(int(x.Invoke) + len("invoke"))
}

// DatatableLit is an Expr (a source usable in expression position).
func (*DatatableLit) node() {}
func (*DatatableLit) expr() {}

// AsOp / InvokeOp node markers.
func (*AsOp) node()     {}
func (*AsOp) operator() {}
func (*InvokeOp) node() {}
func (*InvokeOp) operator() {}
