package parser

import (
	"nzinfo/kql/internal/frontend/ast"
	"nzinfo/kql/internal/frontend/token"
)

// P1+ operator parsers. These implement enough of the g4 grammar for each
// operator to parse real queries; emit/execution semantics are best-effort for
// the minimal loop (see NOTES.md §6).

// parseMvExpandOp: `| mv-expand [kind=...] Name = Expr [to typeof(T)], ... [limit N]`
// (g4 mvexpandOperator).
func (p *Parser) parseMvExpandOp(pipePos token.Pos) *ast.MvExpandOp {
	opPos := p.pos
	p.next() // consume mv-expand
	_ = p.parseOperatorParams()
	cols := p.parseNamedExprList()
	// optional `to typeof(T)` per column (g4 mvapplyOperatorExpressionToClause)
	for p.cur == token.TO {
		p.next()
		_ = p.ParseExpr() // typeof(double) etc. — skip
	}
	// optional `limit N`
	var limit ast.Expr
	if p.cur == token.LIMIT {
		p.next()
		limit = p.ParseExpr()
	}
	return &ast.MvExpandOp{Pipe: pipePos, MvExp: opPos, Cols: cols, Limit: limit}
}

// parseMakeSeriesOp: `| make-series <aggs> on <col> [from .. to .. step .. | in range(..)] [by ...]`
// (g4 makeSeriesOperator).
func (p *Parser) parseMakeSeriesOp(pipePos token.Pos) *ast.MakeSeriesOp {
	opPos := p.pos
	p.next() // consume make-series
	_ = p.parseOperatorParams()
	// aggregations until ON
	aggs := p.parseNamedExprList(token.ON)
	out := &ast.MakeSeriesOp{Pipe: pipePos, MakeSeries: opPos, Aggregates: aggs}
	// ON <expr>
	if p.cur == token.ON {
		out.OnPos = p.pos
		p.next()
		out.OnExpr = p.ParseExpr()
	}
	// range: either `in range (from, to, step)` or `from X to Y step Z`
	if p.cur == token.IN {
		p.next()
		if p.cur == token.RANGE { // RANGE lexes as IDENT (not a keyword token)
			p.next()
		}
		out.InRange = true
		if p.cur == token.LPAREN {
			p.next()
			out.From = p.ParseExpr()
			p.expect(token.COMMA)
			out.To = p.ParseExpr()
			p.expect(token.COMMA)
			out.Step = p.ParseExpr()
			p.expect(token.RPAREN)
		}
	} else {
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
	}
	// BY <keys>
	if p.cur == token.BY {
		out.ByPos = p.pos
		p.next()
		out.ByKeys = p.parseNamedExprList()
	}
	return out
}

// parseParseOp: `| parse [Kind=...] Expr with <pattern>` (g4 parseOperator) or
// `| parse-where ...` (parseWhereOperator). The pattern is captured as raw text
// between `with` and the next operator boundary (| or ; or EOF) — full segment
// parsing is deferred; emit will best-effort a regex/extraction.
func (p *Parser) parseParseOp(pipePos token.Pos, isWhere bool) *ast.ParseOp {
	opPos := p.pos
	p.next() // consume parse / parse-where
	out := &ast.ParseOp{Pipe: pipePos, Parse: opPos, IsWhere: isWhere}
	// optional Kind=... Flags=...
	params := p.parseOperatorParams()
	for _, prm := range params {
		if prm.Name != nil {
			switch prm.Name.Name {
			case "kind", "Kind":
				if id, ok := prm.Value.(*ast.Ident); ok {
					out.Kind = id.Name
				}
			case "flags", "Flags":
				if id, ok := prm.Value.(*ast.Ident); ok {
					out.Flags = id.Name
				}
			}
		}
	}
	// target expression until WITH
	out.Target = p.parseExprUntil(token.WITH)
	if p.cur == token.WITH {
		out.WithPos = p.pos
		p.next()
		out.Pattern = p.scanParsePattern()
	}
	return out
}

// parseExprUntil parses an expression, stopping (without consuming) at stopTok.
// Used for parse's target-before-WITH and similar clause-delimited exprs.
func (p *Parser) parseExprUntil(stopTok token.Token) ast.Expr {
	// Save state; ParseExpr consumes greedily but the precedence layers stop at
	// any keyword that isn't an operator. WITH is a keyword, so ParseExpr should
	// stop naturally. If not, fall back to a single primary.
	return p.ParseExpr()
}

// scanParsePattern captures the raw text of a parse pattern (between `with` and
// the next `|`, `;`, or EOF). This is a pragmatic text capture: the g4 pattern
// grammar (parseOperatorPattern with segments and `*`) is complex; for the
// minimal loop we keep the raw source and let emit best-effort it.
func (p *Parser) scanParsePattern() string {
	// Re-lex from the current position by reading source bytes until | ; EOF.
	// We approximate: collect token literals until a stage boundary.
	var parts []string
	for {
		switch p.cur {
		case token.PIPE, token.SEMI, token.EOF:
			goto done
		}
		parts = append(parts, p.lit)
		if p.lit == "" {
			parts = append(parts, p.cur.String())
		}
		// preserve spacing approximation
		parts = append(parts, " ")
		p.next()
	}
done:
	if len(parts) == 0 {
		return ""
	}
	// join and trim trailing space
	s := joinStrings(parts)
	return trimRightSpace(s)
}

// parseParseKvOp: `| parse-kv Expr Keys [with (...)]` (g4 parseKvOperator).
// Minimal: capture the expression + a rowSchema (column list) + optional with.
func (p *Parser) parseParseKvOp(pipePos token.Pos) *ast.ParseOp {
	opPos := p.pos
	p.next() // consume parse-kv
	out := &ast.ParseOp{Pipe: pipePos, Parse: opPos}
	out.Target = p.ParseExpr()
	// Keys schema: (name:type, ...) — parse as a parenthesised list for now.
	if p.cur == token.LPAREN {
		// consume the schema paren block as raw-ish via parseExprList
		_ = p.ParseExpr()
	}
	// optional with (...)
	if p.cur == token.WITH {
		p.next()
		if p.cur == token.LPAREN {
			// skip the with(...) block
			depth := 0
			for {
				if p.cur == token.LPAREN {
					depth++
				}
				if p.cur == token.RPAREN {
					depth--
					if depth == 0 {
						p.next()
						break
					}
				}
				if p.cur == token.EOF {
					break
				}
				p.next()
			}
		}
	}
	return out
}

// parseRenderOp: `| render <chart> [with (...) | legacy props]` (g4 renderOperator).
// Presentation-only; parsed and dropped at emit (no-op).
func (p *Parser) parseRenderOp(pipePos token.Pos) *ast.RenderOp {
	opPos := p.pos
	p.next() // consume render
	out := &ast.RenderOp{Pipe: pipePos, Render: opPos}
	// chart kind: an IDENT or a chart keyword (table/list/barchart/...).
	if p.cur == token.IDENT || p.cur.IsKeyword() {
		out.ChartKind = p.lit
		p.next()
	}
	// optional with (...) or legacy name=value props — consume greedily until
	// next stage boundary.
	if p.cur == token.WITH {
		p.next()
		if p.cur == token.LPAREN {
			depth := 0
			for {
				if p.cur == token.LPAREN {
					depth++
				}
				if p.cur == token.RPAREN {
					depth--
					if depth == 0 {
						p.next()
						break
					}
				}
				if p.cur == token.EOF {
					break
				}
				p.next()
			}
		}
	}
	return out
}

// parseConsumeOp: `| consume` (g4 consumeOperator).
func (p *Parser) parseConsumeOp(pipePos token.Pos) *ast.ConsumeOp {
	opPos := p.pos
	p.next()
	_ = p.parseOperatorParams()
	return &ast.ConsumeOp{Pipe: pipePos, Consume: opPos}
}

// parseGetSchemaOp: `| getschema` (g4 getSchemaOperator).
func (p *Parser) parseGetSchemaOp(pipePos token.Pos) *ast.GetSchemaOp {
	opPos := p.pos
	p.next()
	return &ast.GetSchemaOp{Pipe: pipePos, GetSchema: opPos}
}

// parseSerializeOp: `| serialize [Name = Expr, ...]` (g4 serializeOperator).
func (p *Parser) parseSerializeOp(pipePos token.Pos) *ast.SerializeOp {
	opPos := p.pos
	p.next()
	_ = p.parseOperatorParams()
	var cols []*ast.NamedExpr
	if p.cur != token.PIPE && p.cur != token.SEMI && p.cur != token.EOF {
		cols = p.parseNamedExprList()
	}
	return &ast.SerializeOp{Pipe: pipePos, Serialize: opPos, Cols: cols}
}

// parseExternalDataOp: `| externaldata(Schema) [StorageClause]` (g4 externalDataOperator).
func (p *Parser) parseExternalDataOp(pipePos token.Pos) *ast.ExternalDataOp {
	opPos := p.pos
	p.next() // consume externaldata
	out := &ast.ExternalDataOp{Pipe: pipePos, ExternalData: opPos}
	if p.cur == token.LPAREN {
		// schema: Name:Type, ... — capture idents and skip.
		p.next()
		var schema []*ast.Ident
		for p.cur != token.RPAREN && p.cur != token.EOF {
			id := p.parseIdentLike()
			schema = append(schema, id)
			if p.cur == token.COLON {
				p.next()
				_ = p.parseIdentLike() // skip type
			}
			p.accept(token.COMMA)
		}
		out.Schema = schema
		p.expect(token.RPAREN)
	}
	// optional storage clause: [StorageAccounts(...) ...] or a string URI
	for p.cur == token.LBRACKET || p.cur == token.STRING || p.cur == token.IDENT {
		out.Storage = append(out.Storage, p.ParseExpr())
		if p.cur == token.COMMA {
			p.next()
		}
	}
	return out
}

// parseEvaluateOp: `| evaluate PluginCall(...) [: schema]` (g4 evaluateOperator).
// Minimal: parse the plugin call expression; emit best-effort (likely NeedsPostProc).
func (p *Parser) parseEvaluateOp(pipePos token.Pos) *ast.RenderOp {
	// Reuse RenderOp shape (a generic "operator with an expression body") for
	// evaluate since both are dropped at emit in the minimal loop. Tag via
	// ChartKind="evaluate".
	opPos := p.pos
	p.next() // consume evaluate
	_ = p.parseOperatorParams()
	out := &ast.RenderOp{Pipe: pipePos, Render: opPos, ChartKind: "evaluate"}
	if p.cur == token.IDENT || p.cur.IsKeyword() {
		// the plugin call (IDENT(args))
		_ = p.ParseExpr()
	}
	// optional : schema — skip to end of op
	for p.cur != token.PIPE && p.cur != token.SEMI && p.cur != token.EOF {
		p.next()
	}
	return out
}

// parseAsOp: `| as [Name(=value) ...] Name` (g4 asOperator). Binds a name to the
// current result; the parameters (hints) are optional and ignored at emit. The
// name is an identifier or an escaped/keyword name. This is a row-wise no-op —
// the translator emits a pass-through and records the name for downstream
// symbol-table use.
func (p *Parser) parseAsOp(pipePos token.Pos) *ast.AsOp {
	opPos := p.pos
	p.next() // consume 'as'
	out := &ast.AsOp{Pipe: pipePos, As: opPos}
	// Optional parameters: `( hint.remote = true )` before the name (rare).
	out.Params = p.parseOperatorParams()
	// Name (required by the grammar, but be lenient: if missing, leave nil and
	// let the diagnostic surface).
	if p.cur == token.IDENT || p.cur.IsKeyword() {
		out.Name = p.parseIdentLike()
	} else {
		p.error(p.pos, "expected name after 'as'")
	}
	return out
}

// parseInvokeOp: `| invoke FunctionName(args...)` (g4 invokeOperator). The
// function reference is a dotCompositeFunctionCallExpression — for the minimal
// loop we capture it as a *CallExpr via ParseExpr (which handles `a.b.c(x)` and
// `Name(args)`).
func (p *Parser) parseInvokeOp(pipePos token.Pos) *ast.InvokeOp {
	opPos := p.pos
	p.next() // consume 'invoke'
	out := &ast.InvokeOp{Pipe: pipePos, Invoke: opPos}
	// The function call: an expression beginning with IDENT (possibly dotted).
	if p.cur == token.IDENT || p.cur.IsKeyword() {
		expr := p.ParseExpr()
		if call, ok := expr.(*ast.CallExpr); ok {
			out.Call = call
		} else {
			// Wrap a bare function reference as a zero-arg call so IR/emit has a
			// uniform shape.
			if id, ok := expr.(*ast.Ident); ok {
				out.Call = &ast.CallExpr{Fun: id}
			} else {
				// Best-effort: stash as a Fun=Ident with the source text.
				out.Call = &ast.CallExpr{}
			}
		}
	} else {
		p.error(p.pos, "expected function name after 'invoke'")
	}
	return out
}

// parsePassthroughOp is a catch-all for P2 operators we parse only to keep
// real queries flowing (top-nested/partition/fork/lookup/facet/sample/...).
// It records the operator keyword (for explain) and consumes tokens to the
// next stage boundary. The translator renders it as a no-op pass-through
// (NeedsPostProc flag); real semantics come with their backend lines.
func (p *Parser) parsePassthroughOp(pipePos token.Pos) *ast.RenderOp {
	opPos := p.pos
	kind := p.lit
	p.next() // consume the operator keyword
	_ = p.parseOperatorParams()
	out := &ast.RenderOp{Pipe: pipePos, Render: opPos, ChartKind: kind}
	// Consume the operator body up to the next stage boundary. Nested parens
	// (e.g. partition by X (subquery), fork ( ... ), ( ... )) are skipped as a
	// balanced group so the `|` inside them doesn't end the op prematurely.
	for p.cur != token.PIPE && p.cur != token.SEMI && p.cur != token.EOF {
		if p.cur == token.LPAREN {
			depth := 0
			for {
				if p.cur == token.LPAREN {
					depth++
				}
				if p.cur == token.RPAREN {
					depth--
					if depth == 0 {
						p.next()
						break
					}
				}
				if p.cur == token.EOF {
					break
				}
				p.next()
			}
			continue
		}
		p.next()
	}
	return out
}

// joinStrings / trimRightSpace: tiny helpers to avoid importing strings here.
func joinStrings(s []string) string {
	var b []byte
	for _, x := range s {
		b = append(b, x...)
	}
	return string(b)
}
func trimRightSpace(s string) string {
	n := len(s)
	for n > 0 && (s[n-1] == ' ') {
		n--
	}
	return s[:n]
}
