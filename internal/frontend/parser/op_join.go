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

// parseJoinRight parses the right side of a join: a parenthesised expression
// (often a sub-pipeline) or a bare table reference.
func (p *Parser) parseJoinRight() ast.Expr {
	return p.ParseExpr()
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
