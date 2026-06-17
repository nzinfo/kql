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

// parseLetStmt parses `let Name = Expr ;` or `let Name = (params) { body }` (a
// function-form lambda). For lambdas, the params and body are captured (the
// body as an expression/pipeline); full lambda semantics (calling, type
// inference) come later — the goal is to PARSE real queries that define helper
// functions. For scalar/tabular lets, parsePipeline handles the RHS.
func (p *Parser) parseLetStmt() *ast.LetStmt {
	let := p.expect(token.LET)
	name := p.parseIdentLike()
	assign := p.expect(token.ASSIGN)
	// Lambda form: `= ( params ) { body }` — detect via lookahead.
	if p.cur == token.LPAREN && p.isLambdaForm() {
		val := p.parseLambda()
		return &ast.LetStmt{Let: let, Name: name, Assign: assign, Expr: val}
	}
	// Scalar or tabular let.
	pipe := p.parsePipeline()
	var val ast.Expr = pipe
	if len(pipe.Ops) == 0 && pipe.Source != nil {
		val = pipe.Source
	}
	return &ast.LetStmt{Let: let, Name: name, Assign: assign, Expr: val}
}

// isLambdaForm reports whether the current `(` begins a lambda `( params ) {`.
// Lookahead: skip a balanced `(...)`, check for `{`.
func (p *Parser) isLambdaForm() bool {
	s := p.save()
	p.next() // consume (
	depth := 1
	for depth > 0 && p.cur != token.EOF {
		if p.cur == token.LPAREN {
			depth++
		}
		if p.cur == token.RPAREN {
			depth--
		}
		p.next()
	}
	isLambda := p.cur == token.LBRACE
	p.restore(s)
	return isLambda
}

// parseLambda parses `( params ) { body }`. Params have optional `: type`
// annotations (`score: int`). The body is captured as a single expression
// (lambdas in KQL are expression-bodied via `{ expr }`); we don't yet support
// multi-statement bodies, but the common single-expr form parses.
func (p *Parser) parseLambda() ast.Expr {
	pos := p.pos
	p.expect(token.LPAREN)
	// params: name [: type], ... — capture names, skip types.
	for p.cur != token.RPAREN && p.cur != token.EOF {
		_ = p.parseIdentLike() // param name
		if p.cur == token.COLON {
			p.next()
			_ = p.parseIdentLike() // type (skip)
		}
		if !p.accept(token.COMMA) {
			break
		}
	}
	p.expect(token.RPAREN)
	// body: { expr } — KQL lambdas are `{ <expr> }`.
	p.expect(token.LBRACE)
	// Parse the body expression up to `}`. For the minimal loop, parse one
	// expression (the common case); if there are more tokens, skip to `}`.
	body := p.ParseExpr()
	for p.cur != token.RBRACE && p.cur != token.EOF {
		p.next()
	}
	p.expect(token.RBRACE)
	return &ast.ParenExpr{Lparen: pos, X: body}
}
