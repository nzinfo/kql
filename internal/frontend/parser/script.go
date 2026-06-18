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

// parseStatement parses one top-level statement: a let-binding, a set-option,
// a declare-parameters, or a query (tabular pipeline). The query form produces
// a QueryStmt wrapping a Pipeline. `set` and `declare` are query metadata and
// do not produce rows.
func (p *Parser) parseStatement() ast.Stmt {
	switch p.cur {
	case token.LET:
		return p.parseLetStmt()
	case token.SET:
		return p.parseSetStmt()
	case token.DECLARE:
		return p.parseDeclareStmt()
	case token.ALIAS:
		return p.parseAliasDatabaseStmt()
	case token.RESTRICT:
		return p.parseRestrictAccessStmt()
	}
	pipe := p.parsePipeline()
	return &ast.QueryStmt{Pipeline: pipe}
}

// parseDeclareStmt parses `declare query_parameters(Name:Type[=Default], ...)`
// (g4 declareQueryParametersStatement). `query_parameters` is its own token in
// the gold grammar but we don't carry it as a keyword (it lexes as IDENT), so
// we recognise it by spelling after consuming `declare`. The statement is query
// metadata: the translator skips it (no IR stage, no SQL). Parameter
// substitution at exec time is deferred; for now we parse + capture.
//
// Unknown `declare <kind>` forms (e.g. `declare pattern`) are parsed leniently:
// the kind name is captured and the parenthesised group is skipped so the rest
// of the script survives.
func (p *Parser) parseDeclareStmt() ast.Stmt {
	declPos := p.expect(token.DECLARE)
	kind := "query_parameters"
	// The kind name: an IDENT (query_parameters / pattern / ...).
	if p.cur == token.IDENT || p.cur.IsKeyword() {
		kind = p.lit
		p.next()
	}
	out := &ast.DeclareStmt{Declare: declPos, Kind: kind}
	// query_parameters has the form `declare query_parameters(...)`: the (
	// immediately follows the kind. Other declare forms (e.g. `declare pattern
	// Name(...)`) have tokens between the kind and the first (. For those we
	// skip ahead to the first ( and then skip the balanced group.
	if kind == "query_parameters" {
		if p.cur != token.LPAREN {
			p.error(p.pos, "expected '(' after declare query_parameters")
			return out
		}
		p.next() // consume (
		out.Params = p.parseQueryParams()
	} else {
		// Scan forward to the first '(' (skipping e.g. the pattern name), then
		// skip the balanced group. Stop at a statement boundary if no '(' found.
		for p.cur != token.LPAREN && p.cur != token.SEMI && p.cur != token.EOF {
			p.next()
		}
		if p.cur == token.LPAREN {
			p.next() // consume (
			depth := 1
			for depth > 0 && p.cur != token.EOF {
				if p.cur == token.LPAREN {
					depth++
				}
				if p.cur == token.RPAREN {
					depth--
					if depth == 0 {
						break
					}
				}
				p.next()
			}
		}
	}
	if p.cur == token.RPAREN {
		out.Rparen = p.pos
		p.next()
	}
	return out
}

// parseAliasDatabaseStmt parses `alias database Name = Expr` (g4
// aliasDatabaseStatement). Database-level aliasing — parsed + skipped (query
// metadata, like SetStmt). The translator ignores it.
func (p *Parser) parseAliasDatabaseStmt() ast.Stmt {
	pos := p.expect(token.ALIAS)
	_ = pos
	if p.cur == token.DATABASE {
		p.next()
	}
	// Consume Name = Expr to the next ; or EOF.
	for p.cur != token.SEMI && p.cur != token.EOF {
		p.next()
	}
	return &ast.SetStmt{Set: pos, Name: &ast.Ident{Name: "alias_database"}}
}

// parseRestrictAccessStmt parses `restrict access to (Entity, ...)` (g4
// restrictAccessStatement). Row-level security directive — parsed + skipped.
func (p *Parser) parseRestrictAccessStmt() ast.Stmt {
	pos := p.expect(token.RESTRICT)
	if p.cur == token.ACCESS {
		p.next()
	}
	if p.cur == token.TO {
		p.next()
	}
	// Consume (Entity, ...) to the next ; or EOF.
	for p.cur != token.SEMI && p.cur != token.EOF {
		p.next()
	}
	return &ast.SetStmt{Set: pos, Name: &ast.Ident{Name: "restrict_access"}}
}

// parseQueryParams parses `Name:Type[=Default], ...` up to the closing ).
// Types are captured as ident spellings (not validated); defaults are any
// primary expression.
func (p *Parser) parseQueryParams() []*ast.QueryParam {
	var out []*ast.QueryParam
	for p.cur != token.RPAREN && p.cur != token.EOF {
		qp := &ast.QueryParam{}
		if p.cur == token.IDENT || p.cur.IsKeyword() {
			qp.Name = p.parseIdentLike()
		}
		if p.cur == token.COLON {
			p.next()
			if p.cur == token.IDENT || p.cur.IsKeyword() {
				qp.Type = p.parseIdentLike()
			}
		}
		if p.cur == token.ASSIGN {
			p.next()
			qp.Default = p.parsePrimary()
		}
		out = append(out, qp)
		if !p.accept(token.COMMA) {
			break
		}
	}
	return out
}

// parseSetStmt parses `set Name [= Value]` (g4 setStatement). Value is optional
// and, when present, is an identifier or a literal (setStatementOptionValue).
// The statement is query metadata and does not produce rows; the translator
// skips it entirely (no IR stage, no SQL).
func (p *Parser) parseSetStmt() *ast.SetStmt {
	setPos := p.expect(token.SET)
	name := p.parseIdentLike()
	out := &ast.SetStmt{Set: setPos, Name: name}
	if p.cur == token.ASSIGN {
		out.Assign = p.pos
		p.next() // consume '='
		// Value: identifier or literal (setStatementOptionValue). parsePrimary
		// handles both forms uniformly; we accept any primary to be lenient.
		out.Value = p.parsePrimary()
	}
	return out
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
