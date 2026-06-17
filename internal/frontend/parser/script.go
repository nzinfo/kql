package parser

import (
	"nzinfo/kql/internal/frontend/ast"
	"nzinfo/kql/internal/frontend/token"
)

// Parse parses the source as a KQL script and returns the AST root. Diagnostics
// are accumulated in the parser; callers MUST check p.Diagnostics().HasErrors()
// before trusting the AST. The parser never panics on bad input.
//
// F4: a script is a sequence of ;-separated statements, each either a
// let-binding or a query (tabular pipeline).
func (p *Parser) Parse() *ast.Script {
	script := &ast.Script{}
	for p.cur != token.EOF {
		stmt := p.parseStatement()
		if stmt != nil {
			script.Statements = append(script.Statements, stmt)
		}
		// Statements are ;-separated; tolerate missing trailing ;.
		if !p.accept(token.SEMI) {
			if p.cur != token.EOF {
				// Unexpected token after a statement — recover.
				p.synchroniseToStatementBoundary()
				p.accept(token.SEMI)
			}
		}
	}
	script.EOF = p.pos
	return script
}

// parseStatement parses one top-level statement: a let-binding or a query
// (tabular pipeline). The query form produces a QueryStmt wrapping a Pipeline.
func (p *Parser) parseStatement() ast.Stmt {
	if p.cur == token.LET {
		return p.parseLetStmt()
	}
	pipe := p.parsePipeline()
	return &ast.QueryStmt{Pipeline: pipe}
}

// parseLetStmt parses `let Name = Expr ;`. Expr may be a scalar expression or a
// tabular pipeline (`let X = T | where ...`); the pipeline form is detected by
// the same parsePipeline entry point used for queries.
func (p *Parser) parseLetStmt() *ast.LetStmt {
	let := p.expect(token.LET)
	name := p.parseIdentLike()
	assign := p.expect(token.ASSIGN)
	// The RHS may be a scalar expr or a pipeline; parsePipeline handles both
	// (it parses an expression for the source, then consumes any `|` stages).
	pipe := p.parsePipeline()
	var val ast.Expr = pipe
	// If the pipeline had no operators and a bare source, surface the source
	// expression directly so scalar lets (`let n = 5;`) keep a scalar shape.
	if len(pipe.Ops) == 0 && pipe.Source != nil {
		val = pipe.Source
	}
	return &ast.LetStmt{Let: let, Name: name, Assign: assign, Expr: val}
}
