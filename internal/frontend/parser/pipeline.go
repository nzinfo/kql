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
//
// Wildcarded table names (g4 wildcardedName): `App*`, `Security*`, `*` — used
// in `union App*` and as a bare source `App* | take 10`. The wildcard `*` here
// is a name suffix, NOT a multiplication operator. We detect this at the source
// position: IDENT immediately followed by `*` (no operator gap) followed by a
// non-expression token (|, comma, ), EOF) is a wildcardedName.
func (p *Parser) parsePipelineSource() ast.Expr {
	// Bare `*` as a source (rare: `* | take 10`).
	if p.cur == token.MUL {
		pos := p.pos
		p.next()
		return &ast.Ident{NamePos: pos, Name: "*", Tok: token.IDENT}
	}
	return p.parseTableNameOrExpr()
}

// isExprStart reports whether a token can start a multiplication operand.
// If `*` is followed by one of these, it's multiplication; otherwise it's a
// wildcard name suffix.
func isExprStart(t token.Token) bool {
	switch t {
	case token.IDENT, token.INT, token.REAL, token.STRING, token.BOOL,
		token.LPAREN, token.LBRACKET, token.SUB, token.ADD, token.NOT:
		return true
	}
	if t.IsLiteral() {
		return true
	}
	return false
}

// isWildcardSegment reports whether a token is a valid wildcardedNameSegment
// (part of a wildcarded table name like `App*Event*`).
func isWildcardSegment(t token.Token) bool {
	switch t {
	case token.IDENT, token.MUL, token.INT:
		return true
	}
	return t.IsKeyword()
}

// parseTableNameOrExpr parses a table reference in a union argument list or
// source position. Handles wildcarded names (App*, Security*, App*Event*) —
// the `*` here is a name suffix, not multiplication. Falls through to ParseExpr
// for normal expressions.
//
// Ambiguity: `App * Event` could be multiplication OR a wildcard name
// `App*Event`. KQL resolves this by context: at source position and in union
// lists, IDENT immediately followed by `*` (no whitespace) is a wildcard name.
// We detect: IDENT * where the char right after IDENT is `*` (no gap) →
// wildcard. This matches the g4 wildcardedNamePrefix rule (IDENT then `*` as
// part of the name, not a separate multiplication operator).
func (p *Parser) parseTableNameOrExpr() ast.Expr {
	if p.cur == token.IDENT || p.cur.IsKeyword() {
		// Check if IDENT is immediately followed by `*` (no whitespace gap).
		// The lexer tracks positions; if the `*` is at IDENT_end, it's adjacent.
		name := p.lit
		namePos := p.pos
		nameEnd := int(namePos) + len(name)
		s := p.save()
		p.next() // consume IDENT
		if p.cur == token.MUL && int(p.pos) == nameEnd {
			// Adjacent `*` → wildcarded name. Consume `*` and any segments.
			p.next() // consume *
			full := name + "*"
			for isWildcardSegment(p.cur) {
				// Only consume segments that are adjacent (no whitespace gap).
				segStart := int(p.pos)
				if segStart != nameEnd+len(full)-len(name) {
					break
				}
				full += p.lit
				nameEnd = segStart + len(p.lit)
				p.next()
			}
			return &ast.Ident{NamePos: namePos, Name: full, Tok: token.IDENT}
		}
		p.restore(s)
	}
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
