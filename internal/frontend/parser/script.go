package parser

import (
	"nzinfo/kql/internal/frontend/ast"
	"nzinfo/kql/internal/frontend/token"
)

// Parse parses the source as a KQL script and returns the AST root. Diagnostics
// are accumulated in the parser; callers MUST check p.Diagnostics().HasErrors()
// before trusting the AST. The parser never panics on bad input.
//
// Currently implements the expression path (F3); the tabular-operator path
// (F4) is wired via parseStatement/parsePipeline as they land. For F3 the
// entry exercises the expression layers via ParseExpr (see expr_test.go).
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

// parseStatement parses one top-level statement. F3 implements the expression-
// statement form (so expression tests can go through Parse()); F4 will add
// let-statement and query-statement (pipeline) forms.
func (p *Parser) parseStatement() ast.Stmt {
	// let Name = expr ;
	if p.cur == token.LET {
		return p.parseLetStmt()
	}
	// Otherwise treat as an expression statement (F3 expression path) or, in
	// F4, a pipeline query statement.
	expr := p.parseExprOrPipeline()
	if expr == nil {
		return nil
	}
	return &ast.ExprStmt{Expr: expr}
}

// parseLetStmt parses `let Name = Expr ;`.
func (p *Parser) parseLetStmt() *ast.LetStmt {
	let := p.expect(token.LET)
	name := p.parseIdentLike()
	assign := p.expect(token.ASSIGN)
	val := p.ParseExpr()
	return &ast.LetStmt{Let: let, Name: name, Assign: assign, Expr: val}
}

// parseExprOrPipeline parses either a tabular pipeline (`Source | op | op …`)
// or a bare expression. For F3 (expression-only) it parses a single expression.
// F4 extends this to detect a following `|` and parse operators.
func (p *Parser) parseExprOrPipeline() ast.Expr {
	// F4 will handle the pipeline form here. For now (F3) parse an expression.
	return p.ParseExpr()
}
