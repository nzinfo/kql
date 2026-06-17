// Package ir defines the dialect-agnostic relational-algebra intermediate
// representation that sits between the KQL AST and the SQL-emitting backends.
//
// Design (DESIGN.md §5, docs/phases/ir/I1-core.md):
//   - A Pipeline is a Source followed by a chain of Stages.
//   - Stages are the relational operators (Filter/Project/Extend/Aggregate/
//     Join/Sort/Limit/Union). Each carries simplified, type-inferred Expr trees
//     (not AST nodes).
//   - Column references bind to a stable integer ColID, NOT a string name.
//     This is the key multi-backend improvement over rust-kql (which uses
//     Ident(String) because it targets a single backend): ColIDs avoid case
//     folding, reserved-word, and quoting differences across pg/duckdb/sqlite.
//   - FuncCall carries capability bits (Caps) that tell each backend whether a
//     function can be a plain SQL expr, needs a UDF, or needs client post-
//     processing — also new vs rust-kql/kqlparser.
//
// IR is an internal intermediate representation, NOT a runtime product: the
// runtime output is always the SQL the backends emit. IR's serialisable form
// (pretty-print) is for `kql explain` and golden snapshots only.
package ir

import "nzinfo/kql/internal/frontend/token"

// Pipeline is the root of an IR query: a Source plus an ordered list of Stages
// applied left-to-right (mirrors g4 pipeExpression / rust-kql TabularExpression).
type Pipeline struct {
	Source Source      // Head; nil only for source-less forms (print/range)
	Stages []Stage     // Operators applied in order
	Position token.Pos // Source position of the pipeline head (for diagnostics)
}

// Pos returns the pipeline head position.
func (p *Pipeline) Pos() token.Pos { return p.Position }

// node marks *Pipeline as an IR Node (a pipeline can be a sub-input to Join/Union).
func (*Pipeline) node() {}

// Source is the interface for pipeline sources. Concrete sources live in
// source.go. MVP implements SourceTable; others are reserved for future phases
// (see source.go for the full reserved set).
type Source interface {
	Node
	source()
}

// Stage is the interface for relational operators in a pipeline.
// Each Stage implementation lives in stage.go.
type Stage interface {
	Node
	stage()
}

// Node is the common interface for all IR nodes (sources, stages, exprs).
// IR nodes carry a source position for diagnostics and a position-less form
// for IR built programmatically (Pos()==NoPos).
type Node interface {
	Pos() token.Pos
	node()
}
