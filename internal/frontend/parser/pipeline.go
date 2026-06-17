package parser

import (
	"nzinfo/kql/internal/frontend/ast"
	"nzinfo/kql/internal/frontend/token"
)

// parsePipeline parses a tabular query pipeline: SourceExpr followed by zero
// or more `| operator` stages (g4 pipeExpression). The source is an expression
// (typically a table name, dotted reference, or a parenthesised sub-pipeline).
// Returns a *ast.Pipeline.
//
// This is the F4 top-level tabular entry point, replacing F3's bare-expression
// parseExprOrPipeline.
func (p *Parser) parsePipeline() *ast.Pipeline {
	pipe := &ast.Pipeline{}
	pipe.Source = p.parsePipelineSource()
	for p.cur == token.PIPE {
		op := p.parsePipedOperator()
		if op != nil {
			pipe.Ops = append(pipe.Ops, op)
		}
	}
	return pipe
}

// parsePipelineSource parses the head of a pipeline (the table reference before
// any `|`). It is an expression but stops at `|` — achieved because parseExpr's
// precedence layers never treat `|` as an operator.
func (p *Parser) parsePipelineSource() ast.Expr {
	return p.ParseExpr()
}

// parsePipedOperator consumes the `|` (already known to be cur) and dispatches
// on the operator keyword that follows to the specific operator parser.
func (p *Parser) parsePipedOperator() ast.Operator {
	pipePos := p.expect(token.PIPE)
	switch p.cur {
	case token.WHERE, token.FILTER:
		return p.parseWhereOp(pipePos)
	case token.PROJECT:
		return p.parseProjectOp(pipePos)
	case token.EXTEND:
		return p.parseExtendOp(pipePos)
	case token.TAKE, token.LIMIT:
		return p.parseTakeOp(pipePos)
	case token.SORT, token.ORDER:
		return p.parseSortOp(pipePos)
	case token.SUMMARIZE:
		return p.parseSummarizeOp(pipePos)
	case token.JOIN:
		return p.parseJoinOp(pipePos)
	case token.UNION:
		return p.parseUnionOp(pipePos)
	case token.DISTINCT:
		return p.parseDistinctOp(pipePos)
	case token.COUNT:
		return p.parseCountOp(pipePos)
	case token.TOP:
		return p.parseTopOp(pipePos)
	case token.PROJECTREORDER:
		return p.parseProjectReorderOp(pipePos)
	}
	// Unknown operator: record an error and recover to the next | or ;.
	p.error(p.pos, "unknown tabular operator "+p.cur.String())
	p.synchroniseToPipeOrSemi()
	return nil
}

// synchroniseToPipeOrSemi skips tokens until `|`, `;`, or EOF — used to recover
// from a malformed operator so subsequent pipeline stages / statements survive.
func (p *Parser) synchroniseToPipeOrSemi() {
	for {
		switch p.cur {
		case token.PIPE, token.SEMI, token.EOF:
			return
		}
		p.next()
	}
}
