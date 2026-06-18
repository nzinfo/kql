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
	case token.PROJECT, token.PROJECTAWAY, token.PROJECTKEEP, token.PROJECTRENAME, token.PROJECTREORDER, token.PROJECTSMART:
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
	// P1 operators
	case token.MVEXPAND:
		return p.parseMvExpandOp(pipePos)
	case token.MVAPPLY:
		return p.parseMvApplyOp(pipePos)
	case token.MAKESERIES:
		return p.parseMakeSeriesOp(pipePos)
	case token.PARSE:
		return p.parseParseOp(pipePos, false)
	case token.PARSEWHERE:
		return p.parseParseOp(pipePos, true)
	case token.PARSEKV:
		return p.parseParseKvOp(pipePos)
	case token.RENDER:
		return p.parseRenderOp(pipePos)
	case token.CONSUME:
		return p.parseConsumeOp(pipePos)
	case token.GETSCHEMA:
		return p.parseGetSchemaOp(pipePos)
	case token.SERIALIZE:
		return p.parseSerializeOp(pipePos)
	case token.EXTERNALDATA:
		return p.parseExternalDataOp(pipePos)
	case token.EVALUATE:
		return p.parseEvaluateOp(pipePos)
	case token.AS:
		return p.parseAsOp(pipePos)
	case token.INVOKE:
		return p.parseInvokeOp(pipePos)
	// P2/P3 operators — parsed to AST nodes for full grammar coverage (g4
	// alignment). Translated as pass-through (semantics need PostProc / lateral
	// joins / plugin frameworks — deferred). See ast/op_p2.go.
	case token.PRINT:
		return p.parsePrintOp(pipePos)
	case token.RANGE:
		return p.parseRangeOp(pipePos)
	case token.FIND:
		return p.parseFindOp(pipePos)
	case token.SAMPLE:
		return p.parseSampleOp(pipePos)
	case token.SAMPLEDISTINCT:
		return p.parseSampleDistinctOp(pipePos)
	case token.LOOKUP:
		return p.parseLookupOp(pipePos)
	case token.SCAN:
		return p.parseScanOp(pipePos)
	case token.FORK:
		return p.parseForkOp(pipePos)
	case token.FACET:
		return p.parseFacetOp(pipePos)
	case token.REDUCE:
		return p.parseReduceOp(pipePos)
	case token.TOPHITTERS:
		return p.parseTopHittersOp(pipePos)
	case token.PARTITION:
		return p.parsePartitionOp(pipePos)
	case token.MACROEXPAND:
		return p.parseMacroExpandOp(pipePos)
	case token.EXECUTEANDCACHE:
		return p.parseExecuteAndCacheOp(pipePos)
	case token.ASSERTSCHEMA:
		return p.parseAssertSchemaOp(pipePos)
	// Graph operators (g4 graph-* rules) — parsed to AST, pass-through.
	case token.GRAPHMATCH:
		return p.parseGraphOp(pipePos, "graph-match")
	case token.MAKEGRAPH:
		return p.parseGraphOp(pipePos, "make-graph")
	case token.GRAPHSHORTESTPATHS:
		return p.parseGraphOp(pipePos, "graph-shortest-paths")
	case token.GRAPHTOTABLE:
		return p.parseGraphOp(pipePos, "graph-to-table")
	case token.GRAPHMARKCOMPONENTS:
		return p.parseGraphOp(pipePos, "graph-mark-components")
	// Remaining passthroughs (tokens exist but grammar too complex for now).
	case token.TOPNESTED:
		return p.parsePassthroughOp(pipePos)
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
