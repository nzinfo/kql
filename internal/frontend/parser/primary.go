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

	// Parenthesised expression or sub-expression
	case token.LPAREN:
		lparen := p.pos
		p.next()
		// Could be a grouped expr `( e )` or a list `( e1, e2 )` (used by in,
		// but those are handled in parseInList; a bare paren list isn't valid
		// as a primary in KQL, so treat the first element as start of an expr
		// and see if a comma follows).
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
// via NamedExpr, matching g4 argumentExpression → namedExpression.
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

// parseArgument parses one call argument, which may be a named binding
// `name = expr` (g4 namedExpression) or a bare expression / `*`.
func (p *Parser) parseArgument() ast.Expr {
	if p.cur == token.MUL {
		pos := p.pos
		p.next()
		return &ast.NamedExpr{Expr: &ast.StarExpr{Star: pos}}
	}
	// Named argument?  IDENT '=' expr   (lookahead without committing)
	if p.cur == token.IDENT {
		s := p.save()
		name := p.parseIdentLike()
		if p.cur == token.ASSIGN {
			assign := p.pos
			p.next()
			val := p.ParseExpr()
			return &ast.NamedExpr{Name: name, Assign: assign, Expr: val}
		}
		p.restore(s) // not a named arg; fall through
	}
	return &ast.NamedExpr{Expr: p.ParseExpr()}
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
