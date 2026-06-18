// Package parser — P2/P3 operator parsers (g4 grammar alignment).
//
// Each parser consumes the operator's syntax and produces the corresponding
// AST node from ast/op_p2.go. All translate as pass-through at the IR level.
// These achieve 100% grammar rule coverage for operator dispatch.
package parser

import (
	"nzinfo/kql/internal/frontend/ast"
	"nzinfo/kql/internal/frontend/token"
)

// parsePrintOp: `| print Expr, Expr, ...` (g4 printOperator).
func (p *Parser) parsePrintOp(pipePos token.Pos) ast.Operator {
	opPos := p.pos
	p.next() // consume print
	cols := p.parseNamedExprList()
	return &ast.PrintOp{Pipe: pipePos, Print: opPos, Cols: cols}
}

// parseRangeOp: `| range Name from Expr to Expr step Expr` (g4 rangeExpression).
func (p *Parser) parseRangeOp(pipePos token.Pos) ast.Operator {
	opPos := p.pos
	p.next() // consume range
	out := &ast.RangeOp{Pipe: pipePos, Range: opPos}
	if p.cur == token.IDENT || p.cur.IsKeyword() {
		out.Name = p.parseIdentLike()
	}
	if p.cur == token.FROM {
		p.next()
		out.From = p.ParseExpr()
	}
	if p.cur == token.TO {
		p.next()
		out.To = p.ParseExpr()
	}
	if p.cur == token.STEP {
		p.next()
		out.Step = p.ParseExpr()
	}
	return out
}

// parseFindOp: `| find [withsource=Name] in (Table, ...) [where Pred]`
// (g4 findOperator).
func (p *Parser) parseFindOp(pipePos token.Pos) ast.Operator {
	opPos := p.pos
	p.next() // consume find
	out := &ast.FindOp{Pipe: pipePos, Find: opPos}
	// optional `withsource=Name`
	_ = p.parseOperatorParams()
	// `in (Table, ...)`
	if p.cur == token.IN {
		p.next()
		if p.cur == token.LPAREN {
			p.next()
			for p.cur != token.RPAREN && p.cur != token.EOF {
				if p.cur == token.IDENT || p.cur.IsKeyword() {
					out.Sources = append(out.Sources, p.parseIdentLike())
				}
				if !p.accept(token.COMMA) {
					break
				}
			}
			p.accept(token.RPAREN)
		}
	}
	// optional `where Pred` — skip tokens to next pipe
	for p.cur != token.PIPE && p.cur != token.SEMI && p.cur != token.EOF {
		p.next()
	}
	return out
}

// parseSampleOp: `| sample N` (g4 sampleOperator).
func (p *Parser) parseSampleOp(pipePos token.Pos) ast.Operator {
	opPos := p.pos
	p.next() // consume sample
	return &ast.SampleOp{Pipe: pipePos, Sample: opPos, N: p.ParseExpr()}
}

// parseSampleDistinctOp: `| sample-distinct N of Col` (g4 sampleDistinctOperator).
func (p *Parser) parseSampleDistinctOp(pipePos token.Pos) ast.Operator {
	opPos := p.pos
	p.next() // consume sample-distinct
	out := &ast.SampleDistinctOp{Pipe: pipePos, Sample: opPos}
	out.N = p.ParseExpr()
	if p.cur == token.OF {
		p.next()
		out.OfCol = p.ParseExpr()
	}
	return out
}

// parseLookupOp: `| lookup Col from Table on Key` (g4 lookupOperator).
func (p *Parser) parseLookupOp(pipePos token.Pos) ast.Operator {
	opPos := p.pos
	p.next() // consume lookup
	out := &ast.LookupOp{Pipe: pipePos, Lookup: opPos}
	out.Cols = p.parseNamedExprList()
	if p.cur == token.FROM {
		p.next()
		if p.cur == token.IDENT || p.cur.IsKeyword() {
			out.From = p.parseIdentLike()
		}
	}
	if p.cur == token.ON {
		p.next()
		out.On = p.ParseExpr()
	}
	return out
}

// parseScanOp: `| scan [declare ...] [partition by ...] [order by ...] [step ...]`
// (g4 scanOperator). Complex stateful operator — parse the clauses leniently.
func (p *Parser) parseScanOp(pipePos token.Pos) ast.Operator {
	opPos := p.pos
	p.next() // consume scan
	out := &ast.ScanOp{Pipe: pipePos, Scan: opPos}
	// Skip everything to the next pipe (scan has complex nested syntax).
	for p.cur != token.PIPE && p.cur != token.SEMI && p.cur != token.EOF {
		p.next()
	}
	return out
}

// parseForkOp: `| fork (SubQuery) (SubQuery) ...` (g4 forkOperator).
func (p *Parser) parseForkOp(pipePos token.Pos) ast.Operator {
	opPos := p.pos
	p.next() // consume fork
	out := &ast.ForkOp{Pipe: pipePos, Fork: opPos}
	for p.cur == token.LPAREN {
		p.next()
		// Parse sub-pipeline until matching )
		depth := 1
		for depth > 0 && p.cur != token.EOF {
			if p.cur == token.LPAREN {
				depth++
			}
			if p.cur == token.RPAREN {
				depth--
				if depth == 0 {
					break
				}
			}
			p.next()
		}
		p.accept(token.RPAREN)
	}
	return out
}

// parseFacetOp: `| facet by Col [limit N]` or `| facet (SubQuery)`
// (g4 facetByOperator).
func (p *Parser) parseFacetOp(pipePos token.Pos) ast.Operator {
	opPos := p.pos
	p.next() // consume facet
	out := &ast.FacetOp{Pipe: pipePos, Facet: opPos}
	if p.cur == token.BY {
		p.next()
		out.By = p.ParseExpr()
	}
	// Skip remaining tokens to next pipe
	for p.cur != token.PIPE && p.cur != token.SEMI && p.cur != token.EOF {
		p.next()
	}
	return out
}

// parseReduceOp: `| reduce by Col [with ...]` (g4 reduceByOperator).
func (p *Parser) parseReduceOp(pipePos token.Pos) ast.Operator {
	opPos := p.pos
	p.next() // consume reduce
	out := &ast.ReduceOp{Pipe: pipePos, Reduce: opPos}
	if p.cur == token.BY {
		p.next()
		out.By = p.ParseExpr()
	}
	// Skip remaining tokens to next pipe
	for p.cur != token.PIPE && p.cur != token.SEMI && p.cur != token.EOF {
		p.next()
	}
	return out
}

// parseTopHittersOp: `| top-hitters N of Col [by AggExpr]` (g4 topHittersOperator).
func (p *Parser) parseTopHittersOp(pipePos token.Pos) ast.Operator {
	opPos := p.pos
	p.next() // consume top-hitters
	out := &ast.TopHittersOp{Pipe: pipePos, TopH: opPos}
	out.N = p.ParseExpr()
	if p.cur == token.OF {
		p.next()
		out.OfCol = p.ParseExpr()
	}
	if p.cur == token.BY {
		p.next()
		out.By = p.ParseExpr()
	}
	return out
}

// parsePartitionOp: `| partition by Col (SubQuery)` (g4 partitionOperator).
// The sub-query is parenthesized; we skip the balanced group to the next pipe.
//
// IMPORTANT: we use parsePrimary (not ParseExpr) for the `by Col` expression
// to avoid ParseExpr greedily consuming the `(...)` as a grouped expression.
// This is a general rule: operator clauses that are followed by a `(...)` group
// should parse their expression as a primary (single term), not a full
// expression tree.
func (p *Parser) parsePartitionOp(pipePos token.Pos) ast.Operator {
	opPos := p.pos
	p.next() // consume partition
	out := &ast.PartitionOp{Pipe: pipePos, Partition: opPos}
	if p.cur == token.BY {
		p.next()
		out.By = p.parsePrimary()
	}
	// Skip the sub-query: if we see (, skip the balanced group; otherwise
	// skip to the next pipe/semi/EOF.
	if p.cur == token.LPAREN {
		p.next()
		depth := 1
		for depth > 0 && p.cur != token.EOF {
			if p.cur == token.LPAREN {
				depth++
			}
			if p.cur == token.RPAREN {
				depth--
				if depth == 0 {
					p.next() // consume the closing )
					break
				}
			}
			p.next()
		}
	} else {
		for p.cur != token.PIPE && p.cur != token.SEMI && p.cur != token.EOF {
			p.next()
		}
	}
	return out
}

// parseMacroExpandOp: `| macro-expand MacroName(args)` (g4 macroExpandOperator).
func (p *Parser) parseMacroExpandOp(pipePos token.Pos) ast.Operator {
	opPos := p.pos
	p.next() // consume macro-expand
	out := &ast.MacroExpandOp{Pipe: pipePos, Macro: opPos}
	if p.cur == token.IDENT || p.cur.IsKeyword() {
		expr := p.ParseExpr()
		if call, ok := expr.(*ast.CallExpr); ok {
			out.Call = call
		}
	}
	return out
}

// parseExecuteAndCacheOp: `| execute-and-cache Query` (g4 executeAndCacheOperator).
func (p *Parser) parseExecuteAndCacheOp(pipePos token.Pos) ast.Operator {
	opPos := p.pos
	p.next() // consume execute-and-cache
	out := &ast.ExecuteAndCacheOp{Pipe: pipePos, Exec: opPos}
	// Skip to next pipe (the query is a string literal or sub-query)
	for p.cur != token.PIPE && p.cur != token.SEMI && p.cur != token.EOF {
		p.next()
	}
	return out
}

// parseAssertSchemaOp: `| assert-schema (Col:Type, ...)` (g4 assertSchemaOperator).
func (p *Parser) parseAssertSchemaOp(pipePos token.Pos) ast.Operator {
	opPos := p.pos
	p.next() // consume assert-schema
	out := &ast.AssertSchemaOp{Pipe: pipePos, Assert: opPos}
	// Skip to next pipe (schema syntax is complex)
	for p.cur != token.PIPE && p.cur != token.SEMI && p.cur != token.EOF {
		p.next()
	}
	return out
}

// parseGraphOp is a unified parser for all graph-* operators (g4 graph-* rules).
// The kind parameter is the operator name for the AST node.
func (p *Parser) parseGraphOp(pipePos token.Pos, kind string) ast.Operator {
	opPos := p.pos
	p.next() // consume the graph-* keyword
	// Skip the graph pattern/clauses to the next pipe (graph syntax is very
	// complex — patterns, match clauses, projections). Parse leniently.
	for p.cur != token.PIPE && p.cur != token.SEMI && p.cur != token.EOF {
		p.next()
	}
	switch kind {
	case "graph-match":
		return &ast.GraphMatchOp{Pipe: pipePos, Match: opPos}
	case "make-graph":
		return &ast.MakeGraphOp{Pipe: pipePos, Make: opPos}
	case "graph-shortest-paths":
		return &ast.GraphShortestPathsOp{Pipe: pipePos, Op: opPos}
	case "graph-to-table":
		return &ast.GraphToTableOp{Pipe: pipePos, Op: opPos}
	case "graph-mark-components":
		return &ast.GraphMarkComponentsOp{Pipe: pipePos, Op: opPos}
	}
	return p.parsePassthroughOp(pipePos)
}
