package parser

import (
	"nzinfo/kql/internal/frontend/ast"
	"nzinfo/kql/internal/frontend/token"
)

// parseSummarizeOp: `| summarize agg1 = f(..), .. by k1, k2, ..`
// (g4 summarizeOperator). Aggregates and group-by are namedExpression lists;
// an optional legacy `bin = <literal>` clause after `by` is accepted but not
// yet captured (TODO: store on SummarizeOp when needed by the binder).
func (p *Parser) parseSummarizeOp(pipePos token.Pos) *ast.SummarizeOp {
	opPos := p.pos
	p.next() // consume summarize
	params := p.parseOperatorParams()
	aggregates := p.parseNamedExprList(token.BY)
	var byPos token.Pos
	var groupBy []*ast.NamedExpr
	if p.cur == token.BY {
		byPos = p.pos
		p.next()
		groupBy = p.parseNamedExprList()
		// Legacy `bin = <literal>` clause (g4 summarizeOperatorLegacyBinClause).
		// "bin" lexes as IDENT (not a keyword token in our table). If present,
		// skip `bin = <expr>` so it doesn't leak into the next operator.
		if p.cur == token.IDENT && p.lit == "bin" {
			s := p.save()
			p.next()
			if p.cur == token.ASSIGN {
				p.next()
				_ = p.ParseExpr()
			} else {
				p.restore(s) // not a bin clause; leave for caller
			}
		}
	}
	return &ast.SummarizeOp{
		Pipe:       pipePos,
		Summarize:  opPos,
		Params:     params,
		Aggregates: aggregates,
		ByPos:      byPos,
		GroupBy:    groupBy,
	}
}
