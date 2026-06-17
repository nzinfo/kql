package parser

import (
	"strings"

	"nzinfo/kql/internal/frontend/ast"
	"nzinfo/kql/internal/frontend/token"
)

// parseJoinOp: `| join [kind=…] (Right) on k1, k2`  (g4 joinOperator).
// Right may be a table reference or a parenthesised sub-pipeline. The kind
// parameter (if present) is extracted from the parsed params and mapped to a
// JoinKind; remaining params are retained for the binder/hints.
func (p *Parser) parseJoinOp(pipePos token.Pos) *ast.JoinOp {
	opPos := p.pos
	p.next() // consume join
	params := p.parseOperatorParams()
	kind := joinKindFromParams(params)

	right := p.parseJoinRight()

	var onPos token.Pos
	var onExpr []ast.Expr
	if p.cur == token.ON {
		onPos = p.pos
		p.next()
		// on conditions: comma-separated expressions (g4 joinOperatorOnClause).
		onExpr = append(onExpr, p.ParseExpr())
		for p.accept(token.COMMA) {
			onExpr = append(onExpr, p.ParseExpr())
		}
	} else if p.cur == token.WHERE {
		// join ... where <pred>  (joinOperatorWhereClause variant) — treat the
		// predicate as a single on-condition for simplicity.
		p.next()
		onPos = p.pos
		onExpr = append(onExpr, p.ParseExpr())
	}

	return &ast.JoinOp{
		Pipe:   pipePos,
		Join:   opPos,
		Params: params,
		Kind:   kind,
		Right:  right,
		OnPos:  onPos,
		OnExpr: onExpr,
	}
}

// parseJoinRight parses the right side of a join. KQL accepts:
//   - a bare table reference:        join ... on ...
//   - a parenthesised table ref:     join (...) on ...
//   - a parenthesised SUB-PIPELINE:  join (T | where ... | summarize ...) on ...
//
// The sub-pipeline form is common in real queries (the corpus shows it in most
// join examples). We distinguish it from a plain parenthesised expression by
// lookahead: if the `(` is followed by `<source> |`, it's a pipeline; otherwise
// it's an expression (e.g. `join (MyTable) on k`).
func (p *Parser) parseJoinRight() ast.Expr {
	if p.cur == token.LPAREN {
		if p.isParenPipeline() {
			// Parse a full pipeline and wrap it in parens (ParenExpr) so the
			// translator can recognise the sub-pipeline form.
			lparen := p.pos
			p.next() // consume (
			pipe := p.parsePipeline()
			rparen := p.expect(token.RPAREN)
			return &ast.ParenExpr{Lparen: lparen, X: pipe, Rparen: rparen}
		}
	}
	return p.ParseExpr()
}

// isParenPipeline reports whether the current `(` begins a sub-pipeline, i.e.
// the tokens after `(` form `<source> |`. It uses save/restore lookahead.
func (p *Parser) isParenPipeline() bool {
	s := p.save()
	p.next() // consume (
	// Parse a source expression; if the next token is PIPE, it's a pipeline.
	_ = p.ParseExpr()
	isPipe := p.cur == token.PIPE
	p.restore(s)
	return isPipe
}

// joinKindFromParams maps a `kind=<value>` parameter (case-insensitive) to a
// JoinKind. KQL kind values: innerunique, inner, left (leftouter), leftouter,
// rightouter, fullouter. Returns JoinDefault if absent or unrecognised.
func joinKindFromParams(params []*ast.OperatorParam) ast.JoinKind {
	for _, prm := range params {
		if prm.Name == nil || !strings.EqualFold(prm.Name.Name, "kind") {
			continue
		}
		val := paramIdentName(prm.Value)
		switch strings.ToLower(val) {
		case "innerunique":
			return ast.JoinInnerUnique
		case "inner":
			return ast.JoinInner
		case "left", "leftouter":
			return ast.JoinLeftOuter
		case "right", "rightouter":
			return ast.JoinRightOuter
		case "full", "fullouter":
			return ast.JoinFullOuter
		}
	}
	return ast.JoinDefault
}

// paramIdentName returns the textual value of a parameter value expression if
// it is a simple identifier, else "". Used to read kind=inner etc.
func paramIdentName(e ast.Expr) string {
	if id, ok := e.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}
