package parser

import (
	"nzinfo/kql/internal/frontend/ast"
	"nzinfo/kql/internal/frontend/token"
)

// ParseExpr is the entry point for scalar-expression parsing.
// It implements the g4 `unnamedExpression` → logicalOrExpression ladder
// (Kql.g4:883-987). See internal/frontend/NOTES.md §2.8 for the precedence
// table and why we use explicit layers instead of a flat Pratt table.
//
// Precedence (low → high), matching the g4 rule chain:
//
//	parseOr         logicalOrExpression        (or)
//	parseAnd        logicalAndExpression       (and)
//	parseEquality   equalityExpression         (==, !=, <>, in, !in, between, …)
//	parseRelational relationalExpression       (<, >, <=, >=)
//	parseAdditive   additiveExpression         (+, -)
//	parseMulti      multiplicativeExpression   (*, /, %)
//	parseStringOp   stringOperatorExpression   (has, contains, =~, !~, matches regex, …)
//	parseUnary      invocationExpression       (unary +, -)
//	parsePostfix    functionCallOrPathExpression (., [])
//	parsePrimary    primaryExpression
func (p *Parser) ParseExpr() ast.Expr {
	return p.parseOr()
}

// parseOr: expr (or expr)*
func (p *Parser) parseOr() ast.Expr {
	left := p.parseAnd()
	for p.cur == token.OR {
		opPos := p.pos
		p.next()
		right := p.parseAnd()
		left = &ast.BinaryExpr{X: left, OpPos: opPos, Op: token.OR, Y: right}
	}
	return left
}

// parseAnd: expr (and expr)*
func (p *Parser) parseAnd() ast.Expr {
	left := p.parseEquality()
	for p.cur == token.AND {
		opPos := p.pos
		p.next()
		right := p.parseEquality()
		left = &ast.BinaryExpr{X: left, OpPos: opPos, Op: token.AND, Y: right}
	}
	return left
}

// parseEquality handles ==, !=, <>, plus the list/range operators in / !in /
// in~ / !in~ / has_any / has_all / between / !between. Per g4 these all sit at
// the equality layer and carry their own parenthesised or `..` syntax.
func (p *Parser) parseEquality() ast.Expr {
	left := p.parseRelational()
	switch p.cur {
	case token.EQL, token.NEQ:
		opPos := p.pos
		op := p.cur
		p.next()
		right := p.parseRelational()
		return &ast.BinaryExpr{X: left, OpPos: opPos, Op: op, Y: right}
	case token.IN, token.INCI, token.NOTIN, token.NOTINCI, token.HASANY, token.HASALL:
		return p.parseInList(left)
	case token.BETWEEN, token.NOTBETWEEN:
		return p.parseBetween(left)
	}
	return left
}

// parseInList parses `X in (e1, e2, …)` and its variants
// (in/!in/in~/!in~/has_any/has_all). The operator has already been identified
// by the caller via cur; this consumes it and the parenthesised list.
func (p *Parser) parseInList(left ast.Expr) ast.Expr {
	opPos := p.pos
	op := p.cur
	p.next()
	lparen := p.expect(token.LPAREN)
	elems := p.parseExprListUntil(token.RPAREN)
	rparen := p.expect(token.RPAREN)
	// Represent as BinaryExpr with the list as a ListExpr on the right, so the
	// AST stays uniform for the binder/translator. The operator token carries
	// the specific kind (IN/NOTIN/NOTINCI/HASANY/HASALL).
	list := &ast.ListExpr{Lparen: lparen, Elems: elems, Rparen: rparen}
	return &ast.BinaryExpr{X: left, OpPos: opPos, Op: op, Y: list}
}

// parseBetween parses `X between (low .. high)` / `X !between (low .. high)`.
// g4 betweenEqualityExpression uses `..` between the bounds (not a comma).
func (p *Parser) parseBetween(left ast.Expr) ast.Expr {
	opPos := p.pos
	not := p.cur == token.NOTBETWEEN
	op := p.cur
	p.next()
	lparen := p.expect(token.LPAREN)
	low := p.parsePostfix() // g4: invocationExpression (no binary ops inside)
	if !p.accept(token.DOTDOT) {
		p.error(p.pos, "expected '..' in between range")
	}
	high := p.parsePostfix()
	rparen := p.expect(token.RPAREN)
	// Use BetweenExpr to preserve the range structure distinctly from in-lists.
	_ = op // captured via Not flag
	return &ast.BetweenExpr{
		X:      left,
		OpPos:  opPos,
		Not:    not,
		Lparen: lparen,
		Low:    low,
		High:   high,
		Rparen: rparen,
	}
}

// parseRelational: expr (< > <= >=)? expr   (non-associative per g4)
func (p *Parser) parseRelational() ast.Expr {
	left := p.parseAdditive()
	switch p.cur {
	case token.LSS, token.GTR, token.LEQ, token.GEQ:
		opPos := p.pos
		op := p.cur
		p.next()
		right := p.parseAdditive()
		return &ast.BinaryExpr{X: left, OpPos: opPos, Op: op, Y: right}
	}
	return left
}

// parseAdditive: expr (+|- expr)*
func (p *Parser) parseAdditive() ast.Expr {
	left := p.parseMulti()
	for p.cur == token.ADD || p.cur == token.SUB {
		opPos := p.pos
		op := p.cur
		p.next()
		right := p.parseMulti()
		left = &ast.BinaryExpr{X: left, OpPos: opPos, Op: op, Y: right}
	}
	return left
}

// parseMulti: expr (*|/|% expr)*
func (p *Parser) parseMulti() ast.Expr {
	left := p.parseStringOp()
	for p.cur == token.MUL || p.cur == token.QUO || p.cur == token.REM {
		opPos := p.pos
		op := p.cur
		p.next()
		right := p.parseStringOp()
		left = &ast.BinaryExpr{X: left, OpPos: opPos, Op: op, Y: right}
	}
	return left
}

// parseStringOp: expr (string-operator expr)?   — the g4 stringOperatorExpression
// layer. String operators (has, contains, =~, matches regex, …) bind *tighter
// than multiplicative; this layer sits between multi and unary. They are
// non-associative (at most one trailing operation per g4 stringBinaryOperatorExpression).
func (p *Parser) parseStringOp() ast.Expr {
	left := p.parseUnary()
	if isStringOpToken(p.cur) {
		opPos := p.pos
		op := p.cur
		p.next()
		right := p.parseUnary()
		return &ast.BinaryExpr{X: left, OpPos: opPos, Op: op, Y: right}
	}
	return left
}

// isStringOpToken reports whether t is one of the g4 stringBinaryOperator
// tokens (stringOperatorExpression layer).
func isStringOpToken(t token.Token) bool {
	switch t {
	case token.TILDE, token.NTILDE,
		token.HAS, token.HASCS,
		token.HASPREFIX, token.HASPREFIXCS,
		token.HASSUFFIX, token.HASSUFFIXCS,
		token.CONTAINS, token.CONTAINSCS,
		token.STARTSWITH, token.STARTSWITHCS,
		token.ENDSWITH, token.ENDSWITHCS,
		token.LIKE, token.LIKECS,
		token.MATCHESREGEX,
		token.NOTHAS, token.NOTHASCS,
		token.NOTHASPREFIX, token.NOTHASPREFIXCS,
		token.NOTHASSUFFIX, token.NOTHASSUFFIXCS,
		token.NOTCONTAINS, token.NOTCONTAINSCS,
		token.NOTSTARTSWITH, token.NOTSTARTSWITCS,
		token.NOTENDSWITH, token.NOTENDSWITHCS,
		token.NOTLIKE, token.NOTLIKECS:
		return true
	}
	return false
}

// parseUnary: (+|-)? postfix   — g4 invocationExpression. KQL has no unary
// logical not (see NOTES.md §2.9); that's the not() function.
func (p *Parser) parseUnary() ast.Expr {
	if p.cur == token.ADD || p.cur == token.SUB {
		opPos := p.pos
		op := p.cur
		p.next()
		x := p.parseUnary()
		return &ast.UnaryExpr{OpPos: opPos, Op: op, X: x}
	}
	return p.parsePostfix()
}

// parsePostfix: primary (. sel | [ idx ])*   — g4 functionCallOrPathPathExpression.
func (p *Parser) parsePostfix() ast.Expr {
	expr := p.parsePrimary()
	for {
		switch p.cur {
		case token.DOT:
			dot := p.pos
			p.next()
			// Member access: X.Sel. Sel is an identifier-like name; accept IDENT
			// or any keyword usable as a name.
			sel := p.parseIdentLike()
			expr = &ast.SelectorExpr{X: expr, Dot: dot, Sel: sel}
		case token.LBRACKET:
			lbr := p.pos
			p.next()
			first := p.ParseExpr()
			// `X[a, b, c, ...]` — a comma-list in brackets (datatable data,
			// dynamic arrays). Treat as an Index whose Index is a ListExpr.
			if p.cur == token.COMMA {
				elems := []ast.Expr{first}
				for p.accept(token.COMMA) {
					elems = append(elems, p.ParseExpr())
				}
				rbr := p.expect(token.RBRACKET)
				expr = &ast.IndexExpr{X: expr, Lbracket: lbr, Index: &ast.ListExpr{Lparen: lbr, Elems: elems, Rparen: rbr}, Rbracket: rbr}
			} else {
				rbr := p.expect(token.RBRACKET)
				expr = &ast.IndexExpr{X: expr, Lbracket: lbr, Index: first, Rbracket: rbr}
			}
		default:
			return expr
		}
	}
}

// parseExprListUntil parses a comma-separated list of expressions until it
// reaches (without consuming) endTok. Returns nil for an empty list.
func (p *Parser) parseExprListUntil(endTok token.Token) []ast.Expr {
	if p.cur == endTok {
		return nil
	}
	out := []ast.Expr{p.ParseExpr()}
	for p.accept(token.COMMA) {
		out = append(out, p.ParseExpr())
	}
	return out
}
