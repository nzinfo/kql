package parser

import (
	"nzinfo/kql/internal/frontend/ast"
	"nzinfo/kql/internal/frontend/token"
)

// parsePrimary is the highest-precedence layer (g4 primaryExpression). It
// dispatches on the current token to the appropriate literal or reference.
func (p *Parser) parsePrimary() ast.Expr {
	switch p.cur {
	// Literals
	case token.INT, token.REAL, token.STRING, token.DATETIME, token.TIMESPAN, token.GUID:
		pos := p.pos
		lit := p.lit
		kind := p.cur
		p.next()
		return &ast.BasicLit{ValuePos: pos, Kind: kind, Value: lit}

	case token.BOOL:
		pos := p.pos
		lit := p.lit
		p.next()
		return &ast.BasicLit{ValuePos: pos, Kind: token.BOOL, Value: lit}

	case token.IDENT:
		// Could be a bare identifier, a function call, or (if followed by '(')
		// a function call. parseIdentFollowed handles the call form.
		return p.parseIdentFollowed()

	// Parenthesised expression, list, or sub-pipeline (the last appears inside
	// function calls like materialize(T | where ...) and as a join's right).
	case token.LPAREN:
		// If the parens hold a sub-pipeline (`( <src> | ... )`), parse a full
		// pipeline and wrap it — needed for materialize(T | ...), and for any
		// call argument that is a tabular pipeline.
		if p.isParenPipeline() {
			lparen := p.pos
			p.next()
			pipe := p.parsePipeline()
			rparen := p.expect(token.RPAREN)
			return &ast.ParenExpr{Lparen: lparen, X: pipe, Rparen: rparen}
		}
		lparen := p.pos
		p.next()
		first := p.ParseExpr()
		if p.cur == token.COMMA {
			elems := []ast.Expr{first}
			for p.accept(token.COMMA) {
				elems = append(elems, p.ParseExpr())
			}
			rparen := p.expect(token.RPAREN)
			return &ast.ListExpr{Lparen: lparen, Elems: elems, Rparen: rparen}
		}
		rparen := p.expect(token.RPAREN)
		return &ast.ParenExpr{Lparen: lparen, X: first, Rparen: rparen}

	case token.LBRACKET:
		// Array literal [ e1, e2, ... ] — appears inside dynamic([...]) and as
		// a standalone array expression. g4 arrayLiteral.
		lbracket := p.pos
		p.next()
		var elems []ast.Expr
		if p.cur != token.RBRACKET {
			elems = append(elems, p.ParseExpr())
			for p.accept(token.COMMA) {
				elems = append(elems, p.ParseExpr())
			}
		}
		rbracket := p.expect(token.RBRACKET)
		return &ast.ListExpr{Lparen: lbracket, Elems: elems, Rparen: rbracket}

	case token.MUL: // `*` wildcard (count(*), project *)
		pos := p.pos
		p.next()
		return &ast.StarExpr{Star: pos}

	case token.NULL:
		pos := p.pos
		lit := p.lit
		p.next()
		return &ast.BasicLit{ValuePos: pos, Kind: token.IDENT, Value: lit} // null represented as ident-lit

	case token.EOF:
		p.error(p.pos, "unexpected end of input")
		return &ast.BadExpr{From: p.pos, To: p.pos}

	// Any keyword in primary position is treated as a name reference (KQL
	// permits keywords as identifiers per identifierOrKeywordOrEscapedName in
	// the g4 grammar). e.g. `count` as a column, `count()` as a call, `kind`
	// as a name. This deliberately avoids a giant keyword switch here.
	// Type keywords that form literal groups (datetime/guid/...) are already
	// scanned as literal tokens by the lexer, so they never reach this branch.
	default:
		if p.cur.IsKeyword() {
			return p.parseIdentFollowed()
		}
	}

	// Genuine unexpected token (not a literal, name, paren, or keyword).
	p.error(p.pos, "unexpected token "+p.cur.String()+" in expression")
	pos := p.pos
	p.next() // skip to make progress
	return &ast.BadExpr{From: pos, To: p.pos}
}

// parseIdentFollowed parses an identifier (or keyword-as-name) possibly
// followed by a call `(...)`. The result is an *ast.Ident or *ast.CallExpr.
func (p *Parser) parseIdentFollowed() ast.Expr {
	name := p.parseIdentLike()
	if p.cur == token.LPAREN {
		return p.parseCall(name)
	}
	return name
}

// parseCall parses the argument list of a function call. name is the function
// expression (typically an *ast.Ident). Handles named arguments `name = expr`
// via NamedExpr, matching g4 argumentExpression → namedExpression. A call
// argument may also be a SUB-PIPELINE when the next tokens form `(<src> | ...)`
// — needed for materialize(T | where ...), invoke(), and similar.
func (p *Parser) parseCall(fun ast.Expr) ast.Expr {
	lparen := p.expect(token.LPAREN)
	var args []ast.Expr
	if p.cur != token.RPAREN {
		args = append(args, p.parseArgument())
		for p.accept(token.COMMA) {
			args = append(args, p.parseArgument())
		}
	}
	rparen := p.expect(token.RPAREN)
	return &ast.CallExpr{Fun: fun, Lparen: lparen, Args: args, Rparen: rparen}
}

// parseArgument parses one call argument. May be a named binding `name = expr`,
// a `*`, a bare expression, or a SUB-PIPELINE when the content forms
// `<source> | <op> ...` — needed for materialize(T | where ...), invoke(), etc.
// (Note: the pipeline appears DIRECTLY inside the call parens, not wrapped in
// another paren pair.)
func (p *Parser) parseArgument() ast.Expr {
	if p.cur == token.MUL {
		pos := p.pos
		p.next()
		return &ast.NamedExpr{Expr: &ast.StarExpr{Star: pos}}
	}
	// Sub-pipeline argument wrapped in its own parens: `(T | ...)`.
	if p.cur == token.LPAREN && p.isParenPipeline() {
		lparen := p.pos
		p.next()
		pipe := p.parsePipeline()
		rparen := p.expect(token.RPAREN)
		return &ast.ParenExpr{Lparen: lparen, X: pipe, Rparen: rparen}
	}
	// Sub-pipeline argument WITHOUT wrapping parens: `T | ...` (materialize).
	// Detect by lookahead: does an expression here get followed by `|`?
	if p.isPipelineArg() {
		pipe := p.parsePipeline()
		return &ast.NamedExpr{Expr: pipe}
	}
	// Named argument?  IDENT '=' expr   (lookahead without committing)
	// Also handles schema args with type annotations: `Name:string` (datatable/
	// externaldata) — the `:type` is skipped, leaving the IDENT as the arg.
	if p.cur == token.IDENT {
		s := p.save()
		name := p.parseIdentLike()
		if p.cur == token.ASSIGN {
			assign := p.pos
			p.next()
			val := p.ParseExpr()
			return &ast.NamedExpr{Name: name, Assign: assign, Expr: val}
		}
		if p.cur == token.COLON {
			// Type annotation: `Name : Type` — skip the type, return the name.
			p.next()
			_ = p.parseIdentLike() // type (skip)
			return name
		}
		p.restore(s) // not a named/typed arg; fall through
	}
	return &ast.NamedExpr{Expr: p.ParseExpr()}
}

// isPipelineArg reports whether the tokens starting at the current position
// form `<source-expr> | <op>` — i.e. a bare pipeline usable as a call argument
// (materialize(T | where ...)). Lookahead via save/restore.
func (p *Parser) isPipelineArg() bool {
	s := p.save()
	// Parse one source expression; a following PIPE means it's a pipeline arg.
	_ = p.ParseExpr()
	isPipe := p.cur == token.PIPE
	p.restore(s)
	return isPipe
}

// parseIdentLike parses an IDENT or a keyword used as a name (KQL permits
// keywords like count, kind, dynamic as identifiers in name position). The
// resulting Ident carries the original token in Tok.
func (p *Parser) parseIdentLike() *ast.Ident {
	pos := p.pos
	lit := p.lit
	tok := p.cur
	if tok != token.IDENT && !tok.IsKeyword() {
		p.error(pos, "expected identifier, found "+tok.String())
	}
	p.next()
	return &ast.Ident{NamePos: pos, Name: lit, Tok: tok}
}
