package parser

import (
	"nzinfo/kql/internal/frontend/ast"
	"nzinfo/kql/internal/frontend/token"
)

// parseSortOp: `| sort by k1 [asc|desc] [nulls first|last], k2 …`
// (alias: order by). g4 sortOperator: (SORT|ORDER) params BY orderedExpression+.
func (p *Parser) parseSortOp(pipePos token.Pos) *ast.SortOp {
	opPos := p.pos
	p.next() // consume sort/order
	params := p.parseOperatorParams()
	var byPos token.Pos
	var orders []*ast.OrderExpr
	if p.cur == token.BY {
		byPos = p.pos
		p.next()
		orders = p.parseOrderedList()
	}
	return &ast.SortOp{Pipe: pipePos, Sort: opPos, Params: params, ByPos: byPos, Orders: orders}
}

// parseOrderedList parses a comma-separated list of ordered expressions
// (g4 orderedExpression: namedExpression sortOrdering), where sortOrdering is
// `(asc|desc)? (nulls (first|last))?`.
func (p *Parser) parseOrderedList() []*ast.OrderExpr {
	var out []*ast.OrderExpr
	out = append(out, p.parseOrderedExpr())
	for p.accept(token.COMMA) {
		out = append(out, p.parseOrderedExpr())
	}
	return out
}

// parseOrderedExpr parses one sort key: expr [asc|desc] [nulls first|last].
func (p *Parser) parseOrderedExpr() *ast.OrderExpr {
	// g4 wraps a namedExpression; we parse a NamedExpr and unwrap to its Expr.
	ne := p.parseNamedExpr()
	oe := &ast.OrderExpr{Expr: ne.Expr}
	// optional ASC / DESC
	if p.cur == token.ASC || p.cur == token.DESC {
		oe.Order = p.cur
		p.next()
	}
	// optional NULLS FIRST / LAST
	if p.cur == token.NULLS {
		p.next()
		if p.cur == token.FIRST || p.cur == token.LAST {
			oe.Nulls = p.cur
			p.next()
		} else {
			p.error(p.pos, "expected first or last after nulls")
		}
	}
	return oe
}
