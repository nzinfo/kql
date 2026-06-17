package ast

import "nzinfo/kql/internal/frontend/token"

// Pipeline is the body of a tabular query: a source followed by a chain of
// piped operators, e.g. `T | where x > 0 | take 10`. Source may be nil when
// the statement is a `print`/`range`/`datatable` form that has no table input.
type Pipeline struct {
	Source Expr       // Head expression (typically *Ident or a table ref); may be nil
	Ops    []Operator // Operators applied left to right
}

// Pos returns the source's start (or the first operator's if source is nil).
func (p *Pipeline) Pos() token.Pos {
	if p.Source != nil {
		return p.Source.Pos()
	}
	if len(p.Ops) > 0 {
		return p.Ops[0].Pos()
	}
	return token.NoPos
}

// End returns one past the last operator (or the source).
func (p *Pipeline) End() token.Pos {
	if len(p.Ops) > 0 {
		return p.Ops[len(p.Ops)-1].End()
	}
	if p.Source != nil {
		return p.Source.End()
	}
	return token.NoPos
}

func (*Pipeline) node() {}
func (*Pipeline) expr() {} // a pipeline can be passed where a table is expected

// QueryStmt is a top-level query statement: a pipeline (with an optional
// trailing `| as Name` / `| render …`, handled by its operator chain).
type QueryStmt struct {
	Pipeline *Pipeline
}

// Pos returns the pipeline's start.
func (q *QueryStmt) Pos() token.Pos { return q.Pipeline.Pos() }

// End returns the pipeline's end.
func (q *QueryStmt) End() token.Pos { return q.Pipeline.End() }

// LetStmt is a `let Name = Expr;` binding. Expr is most often a scalar
// expression or a *Pipeline (tabular let). Function-form lets
// (`let f(x) = { … }`) will be added with F4; the scalar/tabular form covers P0.
type LetStmt struct {
	Let   token.Pos // Position of "let"
	Name  *Ident    // Bound name
	Assign token.Pos // Position of "="
	Expr  Expr      // Bound expression (scalar or pipeline)
	Semi  token.Pos // Position of trailing ";" (NoPos if absent)
}

// Pos returns the position of "let".
func (l *LetStmt) Pos() token.Pos { return l.Let }

// End returns one past ";" if present, else the bound expression's end.
func (l *LetStmt) End() token.Pos {
	if l.Semi.IsValid() {
		return token.Pos(int(l.Semi) + 1)
	}
	return l.Expr.End()
}

// ExprStmt is a bare expression used as a statement (e.g. a table reference on
// its own line). Its Expr is typically an *Ident or *Pipeline.
type ExprStmt struct {
	Expr Expr
}

// Pos returns the expression's start.
func (s *ExprStmt) Pos() token.Pos { return s.Expr.Pos() }

// End returns one past the expression's end.
func (s *ExprStmt) End() token.Pos { return s.Expr.End() }

// Script is the root of a KQL script: a sequence of statements separated by
// ';', matching the authoritative grammar's `query: statement (';' statement)*`.
type Script struct {
	Statements []Stmt
	EOF        token.Pos
}

// Pos returns the first statement's start (or EOF if empty).
func (s *Script) Pos() token.Pos {
	if len(s.Statements) > 0 {
		return s.Statements[0].Pos()
	}
	return s.EOF
}

// End returns the EOF position.
func (s *Script) End() token.Pos { return s.EOF }

// Statement-node markers.
func (*Script) node() {} // Script is the root Node, not a Stmt (it contains Stmts)
func (*QueryStmt) node() {}
func (*QueryStmt) stmt() {}
func (*LetStmt) node()   {}
func (*LetStmt) stmt()   {}
func (*ExprStmt) node()  {}
func (*ExprStmt) stmt()  {}
