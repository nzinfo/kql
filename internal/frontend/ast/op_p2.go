// Package ast — P2/P3 operators (g4 grammar alignment).
//
// These operators are parsed + captured (AST nodes exist) but translated as
// pass-through (real semantics need client-side PostProc, lateral joins, or
// plugin frameworks — deferred). Having AST nodes means:
//   1. Queries using them PARSE (no syntax error)
//   2. The IR tree captures them (Explain shows what was used)
//   3. They translate to a pass-through Project{Star} (rows flow through)
//
// This achieves 100% grammar rule coverage for operator dispatch — every g4
// `*Operator` rule has a corresponding AST node + parser route. The
// emit-layer pass-through means results are correct (rows flow unchanged) but
// the operator's effect isn't applied. This is the "parse-but-no-op" pattern
// used by kqlparser for advanced operators.
package ast

import "nzinfo/kql/internal/frontend/token"

// PrintOp is `| print Expr, Expr, ...` — outputs a single row with the given
// expressions as columns (g4 printOperator).
type PrintOp struct {
	Pipe  token.Pos
	Print token.Pos
	Cols  []*NamedExpr
}

func (x *PrintOp) Pos() token.Pos { return x.Pipe }
func (x *PrintOp) End() token.Pos {
	if len(x.Cols) > 0 {
		return x.Cols[len(x.Cols)-1].End()
	}
	return token.Pos(int(x.Print) + len("print"))
}

// RangeOp is `| range Name from Expr to Expr step Expr` — generates a series
// of rows (g4 rangeExpression).
type RangeOp struct {
	Pipe   token.Pos
	Range  token.Pos
	Name   *Ident
	From   Expr
	To     Expr
	Step   Expr
}

func (x *RangeOp) Pos() token.Pos { return x.Pipe }
func (x *RangeOp) End() token.Pos {
	if x.Step != nil {
		return x.Step.End()
	}
	if x.To != nil {
		return x.To.End()
	}
	return token.Pos(int(x.Range) + len("range"))
}

// FindOp is `| find [Kind] in (Table, ...) [where Pred] [project Col, ...]`
// (g4 findOperator). Searches multiple tables for matching rows.
type FindOp struct {
	Pipe    token.Pos
	Find    token.Pos
	Kind    string // "withsource", "", etc.
	Sources []*Ident
	Where   Expr
	Project []string
}

func (x *FindOp) Pos() token.Pos { return x.Pipe }
func (x *FindOp) End() token.Pos {
	if len(x.Project) > 0 {
		return token.Pos(int(x.Pipe) + 1)
	}
	if len(x.Sources) > 0 {
		return x.Sources[len(x.Sources)-1].End()
	}
	return token.Pos(int(x.Find) + len("find"))
}

// SampleOp is `| sample N` — returns N random rows (g4 sampleOperator).
type SampleOp struct {
	Pipe   token.Pos
	Sample token.Pos
	N      Expr
}

func (x *SampleOp) Pos() token.Pos { return x.Pipe }
func (x *SampleOp) End() token.Pos {
	if x.N != nil {
		return x.N.End()
	}
	return token.Pos(int(x.Sample) + len("sample"))
}

// SampleDistinctOp is `| sample-distinct N of Col` — returns N distinct values
// of Col (g4 sampleDistinctOperator).
type SampleDistinctOp struct {
	Pipe  token.Pos
	Sample token.Pos
	N     Expr
	OfCol Expr
}

func (x *SampleDistinctOp) Pos() token.Pos { return x.Pipe }
func (x *SampleDistinctOp) End() token.Pos {
	if x.OfCol != nil {
		return x.OfCol.End()
	}
	if x.N != nil {
		return x.N.End()
	}
	return token.Pos(int(x.Sample) + len("sample-distinct"))
}

// LookupOp is `| lookup Col from Table on Key` — left-join style lookup
// (g4 lookupOperator).
type LookupOp struct {
	Pipe   token.Pos
	Lookup token.Pos
	Cols   []*NamedExpr
	From   *Ident
	On     Expr
}

func (x *LookupOp) Pos() token.Pos { return x.Pipe }
func (x *LookupOp) End() token.Pos {
	if x.On != nil {
		return x.On.End()
	}
	if x.From != nil {
		return x.From.End()
	}
	return token.Pos(int(x.Lookup) + len("lookup"))
}

// ScanOp is `| scan [declare ...] [partition by ...] [order by ...] [step ...]`
// (g4 scanOperator). Stateful row-by-row processing.
type ScanOp struct {
	Pipe    token.Pos
	Scan    token.Pos
	Declare []*NamedExpr
	PartitionBy []Expr
	OrderBy []SortTerm
	Steps   []Expr // step conditions
}

func (x *ScanOp) Pos() token.Pos { return x.Pipe }
func (x *ScanOp) End() token.Pos {
	if len(x.Steps) > 0 && x.Steps[len(x.Steps)-1] != nil {
		return x.Steps[len(x.Steps)-1].End()
	}
	return token.Pos(int(x.Scan) + len("scan"))
}

// SortTerm is one sort key (expr + desc/asc).
type SortTerm struct {
	Expr Expr
	Desc bool
}

func (x *SortTerm) End() token.Pos {
	if x.Expr != nil {
		return x.Expr.End()
	}
	return 0
}

// ForkOp is `| fork (SubQuery1) (SubQuery2) ...` — splits the pipeline into
// multiple parallel sub-queries (g4 forkOperator).
type ForkOp struct {
	Pipe  token.Pos
	Fork  token.Pos
	Subs  []*Pipeline
}

func (x *ForkOp) Pos() token.Pos { return x.Pipe }
func (x *ForkOp) End() token.Pos {
	if len(x.Subs) > 0 && x.Subs[len(x.Subs)-1] != nil {
		return x.Subs[len(x.Subs)-1].End()
	}
	return token.Pos(int(x.Fork) + len("fork"))
}

// FacetOp is `| facet by Col [order by ...] [limit N]` or
// `| facet (SubQuery)` — runs a sub-query per facet value (g4 facetByOperator).
type FacetOp struct {
	Pipe  token.Pos
	Facet token.Pos
	By    Expr
	Limit Expr
	Sub   *Pipeline // optional sub-query form
}

func (x *FacetOp) Pos() token.Pos { return x.Pipe }
func (x *FacetOp) End() token.Pos {
	if x.Sub != nil {
		return x.Sub.End()
	}
	if x.Limit != nil {
		return x.Limit.End()
	}
	if x.By != nil {
		return x.By.End()
	}
	return token.Pos(int(x.Facet) + len("facet"))
}

// ReduceOp is `| reduce by Col [with ...]` — groups + summarizes similar rows
// (g4 reduceByOperator).
type ReduceOp struct {
	Pipe   token.Pos
	Reduce token.Pos
	By     Expr
}

func (x *ReduceOp) Pos() token.Pos { return x.Pipe }
func (x *ReduceOp) End() token.Pos {
	if x.By != nil {
		return x.By.End()
	}
	return token.Pos(int(x.Reduce) + len("reduce"))
}

// TopHittersOp is `| top-hitters N of Col by AggExpr` — finds the top N values
// by an aggregate (g4 topHittersOperator).
type TopHittersOp struct {
	Pipe   token.Pos
	TopH   token.Pos
	N      Expr
	OfCol  Expr
	By     Expr
}

func (x *TopHittersOp) Pos() token.Pos { return x.Pipe }
func (x *TopHittersOp) End() token.Pos {
	if x.By != nil {
		return x.By.End()
	}
	if x.OfCol != nil {
		return x.OfCol.End()
	}
	if x.N != nil {
		return x.N.End()
	}
	return token.Pos(int(x.TopH) + len("top-hitters"))
}

// PartitionOp is `| partition [Hint] by Col (SubQuery)` — runs a sub-query per
// partition (g4 partitionOperator, uses token __partitionby).
type PartitionOp struct {
	Pipe      token.Pos
	Partition token.Pos
	Hint      string
	By        Expr
	Sub       *Pipeline
}

func (x *PartitionOp) Pos() token.Pos { return x.Pipe }
func (x *PartitionOp) End() token.Pos {
	if x.Sub != nil {
		return x.Sub.End()
	}
	if x.By != nil {
		return x.By.End()
	}
	return token.Pos(int(x.Partition) + len("partition"))
}

// MacroExpandOp is `| macro-expand MacroName(args)` (g4 macroExpandOperator).
type MacroExpandOp struct {
	Pipe  token.Pos
	Macro token.Pos
	Call  *CallExpr
}

func (x *MacroExpandOp) Pos() token.Pos { return x.Pipe }
func (x *MacroExpandOp) End() token.Pos {
	if x.Call != nil {
		return x.Call.End()
	}
	return token.Pos(int(x.Macro) + len("macro-expand"))
}

// ExecuteAndCacheOp is `| execute-and-cache Query` (g4 executeAndCacheOperator).
type ExecuteAndCacheOp struct {
	Pipe     token.Pos
	Exec     token.Pos
	CacheKey string
	Query    Expr
}

func (x *ExecuteAndCacheOp) Pos() token.Pos { return x.Pipe }
func (x *ExecuteAndCacheOp) End() token.Pos {
	if x.Query != nil {
		return x.Query.End()
	}
	return token.Pos(int(x.Exec) + len("execute-and-cache"))
}

// AssertSchemaOp is `| assert-schema (Col:Type, ...)` (g4 assertSchemaOperator).
type AssertSchemaOp struct {
	Pipe   token.Pos
	Assert token.Pos
	Cols   []*NamedExpr
}

func (x *AssertSchemaOp) Pos() token.Pos { return x.Pipe }
func (x *AssertSchemaOp) End() token.Pos {
	if len(x.Cols) > 0 {
		return x.Cols[len(x.Cols)-1].End()
	}
	return token.Pos(int(x.Assert) + len("assert-schema"))
}

// --- Graph operators (g4 graph-* rules) ---

// GraphMatchOp is `| graph-match Pattern` (g4 graphMatchOperator).
type GraphMatchOp struct {
	Pipe   token.Pos
	Match  token.Pos
	Pattern Expr // the match pattern (captured loosely)
	Where  Expr
	Project []string
}

func (x *GraphMatchOp) Pos() token.Pos { return x.Pipe }
func (x *GraphMatchOp) End() token.Pos { return token.Pos(int(x.Match) + len("graph-match")) }

// MakeGraphOp is `| make-graph SourceCol on EdgeCol from Table to Table`
// (g4 makeGraphOperator).
type MakeGraphOp struct {
	Pipe  token.Pos
	Make  token.Pos
	Source Expr
	Target Expr
	On    Expr
}

func (x *MakeGraphOp) Pos() token.Pos { return x.Pipe }
func (x *MakeGraphOp) End() token.Pos { return token.Pos(int(x.Make) + len("make-graph")) }

// GraphShortestPathsOp is `| graph-shortest-paths ...` (g4).
type GraphShortestPathsOp struct {
	Pipe token.Pos
	Op   token.Pos
	Args []Expr
}

func (x *GraphShortestPathsOp) Pos() token.Pos { return x.Pipe }
func (x *GraphShortestPathsOp) End() token.Pos { return token.Pos(int(x.Op) + len("graph-shortest-paths")) }

// GraphToTableOp is `| graph-to-table Col as Name` (g4).
type GraphToTableOp struct {
	Pipe token.Pos
	Op   token.Pos
	Cols []string
	As   string
}

func (x *GraphToTableOp) Pos() token.Pos { return x.Pipe }
func (x *GraphToTableOp) End() token.Pos { return token.Pos(int(x.Op) + len("graph-to-table")) }

// GraphMarkComponentsOp is `| graph-mark-components ...` (g4).
type GraphMarkComponentsOp struct {
	Pipe token.Pos
	Op   token.Pos
	Args []Expr
}

func (x *GraphMarkComponentsOp) Pos() token.Pos { return x.Pipe }
func (x *GraphMarkComponentsOp) End() token.Pos { return token.Pos(int(x.Op) + len("graph-mark-components")) }

// --- Node markers ---

func (*PrintOp) node()             {}
func (*PrintOp) operator()         {}
func (*RangeOp) node()             {}
func (*RangeOp) operator()         {}
func (*FindOp) node()              {}
func (*FindOp) operator()          {}
func (*SampleOp) node()            {}
func (*SampleOp) operator()        {}
func (*SampleDistinctOp) node()    {}
func (*SampleDistinctOp) operator() {}
func (*LookupOp) node()            {}
func (*LookupOp) operator()        {}
func (*ScanOp) node()              {}
func (*ScanOp) operator()          {}
func (*ForkOp) node()              {}
func (*ForkOp) operator()          {}
func (*FacetOp) node()             {}
func (*FacetOp) operator()         {}
func (*ReduceOp) node()            {}
func (*ReduceOp) operator()        {}
func (*TopHittersOp) node()        {}
func (*TopHittersOp) operator()    {}
func (*PartitionOp) node()         {}
func (*PartitionOp) operator()     {}
func (*MacroExpandOp) node()       {}
func (*MacroExpandOp) operator()   {}
func (*ExecuteAndCacheOp) node()   {}
func (*ExecuteAndCacheOp) operator() {}
func (*AssertSchemaOp) node()      {}
func (*AssertSchemaOp) operator()  {}
func (*GraphMatchOp) node()        {}
func (*GraphMatchOp) operator()    {}
func (*MakeGraphOp) node()         {}
func (*MakeGraphOp) operator()     {}
func (*GraphShortestPathsOp) node()    {}
func (*GraphShortestPathsOp) operator() {}
func (*GraphToTableOp) node()          {}
func (*GraphToTableOp) operator()       {}
func (*GraphMarkComponentsOp) node()    {}
func (*GraphMarkComponentsOp) operator() {}
