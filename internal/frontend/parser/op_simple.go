package parser

import (
	"nzinfo/kql/internal/frontend/ast"
	"nzinfo/kql/internal/frontend/token"
)

// parseWhereOp: `| where <predicate>` (alias: filter). Optional operator params
// precede the predicate (g4 whereOperator).
func (p *Parser) parseWhereOp(pipePos token.Pos) *ast.WhereOp {
	opPos := p.pos
	p.next() // consume where/filter
	_ = p.parseOperatorParams() // where params are rare; ignored for now
	pred := p.ParseExpr()
	return &ast.WhereOp{Pipe: pipePos, Where: opPos, Predicate: pred}
}

// parseProjectOp: `| project c1 = e1, c2, …` (g4 projectOperator).
func (p *Parser) parseProjectOp(pipePos token.Pos) *ast.ProjectOp {
	opPos := p.pos
	p.next() // consume project
	cols := p.parseNamedExprList()
	return &ast.ProjectOp{Pipe: pipePos, Project: opPos, Columns: cols}
}

// parseExtendOp: `| extend c1 = e1, c2 = e2, …` (g4 extendOperator).
func (p *Parser) parseExtendOp(pipePos token.Pos) *ast.ExtendOp {
	opPos := p.pos
	p.next()
	cols := p.parseNamedExprList()
	return &ast.ExtendOp{Pipe: pipePos, Extend: opPos, Columns: cols}
}

// parseTakeOp: `| take N` (alias: limit). N is an expression (usually an int).
func (p *Parser) parseTakeOp(pipePos token.Pos) *ast.TakeOp {
	opPos := p.pos
	p.next() // consume take/limit
	_ = p.parseOperatorParams()
	count := p.ParseExpr()
	return &ast.TakeOp{Pipe: pipePos, Take: opPos, Count: count}
}

// parseCountOp: `| count` (standalone, no operands; g4 countOperator).
func (p *Parser) parseCountOp(pipePos token.Pos) *ast.CountOp {
	opPos := p.pos
	p.next() // consume count
	// count may take trailing params but has no body; ignore params.
	return &ast.CountOp{Pipe: pipePos, Count: opPos}
}

// parseDistinctOp: `| distinct c1, c2, *` (g4 distinctOperator).
func (p *Parser) parseDistinctOp(pipePos token.Pos) *ast.DistinctOp {
	opPos := p.pos
	p.next()
	_ = p.parseOperatorParams()
	var cols []ast.Expr
	if p.cur == token.MUL {
		star := p.pos
		p.next()
		cols = append(cols, &ast.StarExpr{Star: star})
	} else {
		for _, ne := range p.parseNamedExprList() {
			cols = append(cols, ne.Expr)
		}
	}
	return &ast.DistinctOp{Pipe: pipePos, Distinct: opPos, Columns: cols}
}

// parseTopOp: `| top N by k [asc|desc] [nulls first|last]`
// (g4 topOperator: TOP namedExpression BY orderedExpression).
func (p *Parser) parseTopOp(pipePos token.Pos) *ast.TopOp {
	opPos := p.pos
	p.next() // consume top
	params := p.parseOperatorParams()
	count := p.ParseExpr()
	var byPos token.Pos
	var orders []*ast.OrderExpr
	if p.cur == token.BY {
		byPos = p.pos
		p.next()
		orders = p.parseOrderedList()
	}
	return &ast.TopOp{Pipe: pipePos, Top: opPos, Count: count, ByPos: byPos, Orders: orders, Params: params}
}
