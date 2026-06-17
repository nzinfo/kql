package parser

import (
	"nzinfo/kql/internal/frontend/ast"
	"nzinfo/kql/internal/frontend/token"
)

// parseOperatorParams parses zero or more leading operator parameters of the
// form `name = value` (g4 relaxedQueryOperatorParameter / strict variant).
// `name` may be a keyword (kind) or an identifier; `value` is an identifier or
// literal. Stops at the first token that isn't a parameter name.
//
// Returns the parsed parameters; the caller decides which to consume (e.g.
// join reads `kind=` from the result).
func (p *Parser) parseOperatorParams() []*ast.OperatorParam {
	var out []*ast.OperatorParam
	for {
		name := p.tryParamName()
		if name == nil {
			break
		}
		// tryParamName returned non-nil without consuming; consume the name
		// token now so p.cur advances to '='.
		p.next()
		if p.cur != token.ASSIGN {
			// A param-name keyword not followed by '=' is malformed; emit a
			// diagnostic and stop (leave cur for the operator body to handle
			// gracefully if it can).
			p.error(p.pos, "expected '=' after operator parameter "+name.Name)
			break
		}
		assign := p.pos
		p.next() // consume '='
		val := p.parseParamValue()
		out = append(out, &ast.OperatorParam{Name: name, Assign: assign, Value: val})
	}
	return out
}

// tryParamName returns an *Ident if the current token can begin a parameter
// name, else nil. Does NOT consume unless it returns non-nil.
//
// Per g4 strictQueryOperatorParameter / relaxedQueryOperatorParameter, param
// names are specific keyword tokens (kind, withsource, datascope, hint.*, …).
// We only recognise the keyword tokens we actually have (kind/withsource/
// datascope); the dotted hint.* forms are g4 lexer tokens (HINT_STRATEGY etc.)
// that our token table doesn't carry yet, so they lex as IDENT and are left
// for the operator body to handle (binder can flag them later). Crucially we
// do NOT probe generic IDENT here — that would mis-grab named bindings like
// `summarize c = count()` where `c` is an aggregate name, not a param.
func (p *Parser) tryParamName() *ast.Ident {
	switch p.cur {
	case token.KIND, token.WITHSOURCE, token.DATASCOPE:
		pos, lit := p.pos, p.lit
		tok := p.cur
		return &ast.Ident{NamePos: pos, Name: lit, Tok: tok}
	}
	return nil
}

// parseParamValue parses a parameter value. Per g4 (queryOperatorProperty),
// values are a single identifier or literal — NOT a function call. So for
// `kind=inner (T2)`, the value is the bare identifier `inner` and `(T2)` is
// left for the operator body (the join's right side). We therefore consume
// exactly one primary token without the postfix/call layers.
func (p *Parser) parseParamValue() ast.Expr {
	switch p.cur {
	case token.IDENT:
		// Bare identifier value (inner, leftouter, broadcast, …). Consume
		// exactly this token — do NOT descend into a call form.
		pos, lit := p.pos, p.lit
		p.next()
		return &ast.Ident{NamePos: pos, Name: lit, Tok: token.IDENT}
	default:
		if p.cur.IsKeyword() {
			pos, lit := p.pos, p.lit
			tok := p.cur
			p.next()
			return &ast.Ident{NamePos: pos, Name: lit, Tok: tok}
		}
	}
	// Literal value (number, string, …).
	return p.parsePrimary()
}

// parseNamedExprList parses a comma-separated list of named expressions
// `name = expr` or bare `expr` (g4 namedExpression list). Used by project,
// extend, summarize aggregates, summarize group-by. Stops at endTok (without
// consuming), `BY`, PIPE, SEMI, or EOF.
func (p *Parser) parseNamedExprList(stops ...token.Token) []*ast.NamedExpr {
	var out []*ast.NamedExpr
	if p.atListEnd(stops) {
		return out
	}
	out = append(out, p.parseNamedExpr())
	for p.accept(token.COMMA) {
		out = append(out, p.parseNamedExpr())
	}
	return out
}

// parseNamedExpr parses one `name = expr` or bare `expr` (g4 namedExpression).
func (p *Parser) parseNamedExpr() *ast.NamedExpr {
	// Tuple unpacking: (a, b) = expr
	if p.cur == token.LPAREN {
		s := p.save()
		lparen := p.pos
		p.next()
		var names []*ast.Ident
		if p.cur == token.IDENT || p.cur.IsKeyword() {
			names = append(names, p.parseIdentLike())
			for p.accept(token.COMMA) {
				names = append(names, p.parseIdentLike())
			}
		}
		if p.cur == token.RPAREN {
			p.next()
			if p.cur == token.ASSIGN {
				assign := p.pos
				p.next()
				val := p.ParseExpr()
				return &ast.NamedExpr{Names: names, Assign: assign, Expr: val}
			}
		}
		// Not a tuple binding; rewind and parse as a bare expression.
		_ = lparen
		p.restore(s)
	}

	// Single name = expr?
	if p.cur == token.IDENT {
		s := p.save()
		name := p.parseIdentLike()
		if p.cur == token.ASSIGN {
			assign := p.pos
			p.next()
			val := p.ParseExpr()
			return &ast.NamedExpr{Name: name, Assign: assign, Expr: val}
		}
		p.restore(s)
	}
	// Bare expression.
	return &ast.NamedExpr{Expr: p.ParseExpr()}
}

// atListEnd reports whether cur is one of the stop tokens, BY, or a structural
// boundary.
func (p *Parser) atListEnd(stops []token.Token) bool {
	if p.cur == token.BY || p.cur == token.PIPE || p.cur == token.SEMI || p.cur == token.EOF {
		return true
	}
	for _, s := range stops {
		if p.cur == s {
			return true
		}
	}
	return false
}
