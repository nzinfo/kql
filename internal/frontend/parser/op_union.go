package parser

import (
	"nzinfo/kql/internal/frontend/ast"
	"nzinfo/kql/internal/frontend/token"
)

// parseUnionOp: `| union T1, T2, [T3, …]` with optional withsource= / isfuzzy
// parameters (g4 unionOperator). Each table may be a bare reference, a
// parenthesised expression, or a wildcarded reference (wildcard support TODO).
func (p *Parser) parseUnionOp(pipePos token.Pos) *ast.UnionOp {
	opPos := p.pos
	p.next() // consume union
	params := p.parseOperatorParams()
	var tables []ast.Expr
	if !atOperatorEnd(p.cur) {
		tables = append(tables, p.ParseExpr())
		for p.accept(token.COMMA) {
			tables = append(tables, p.ParseExpr())
		}
	}
	return &ast.UnionOp{Pipe: pipePos, Union: opPos, Params: params, Tables: tables}
}

// atOperatorEnd reports whether the current token ends an operator's table list
// (a pipe to the next stage, a statement separator, or EOF).
func atOperatorEnd(t token.Token) bool {
	switch t {
	case token.PIPE, token.SEMI, token.EOF:
		return true
	}
	return false
}
