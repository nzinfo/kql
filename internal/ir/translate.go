// Package ir translator: AST → IR (I2). See docs/phases/ir/I2-translate.md.
//
// This is the P0 translator: it covers all P0 tabular operators (where/project/
// extend/take/sort/summarize/join/union/distinct/count/top) and the full
// expression layer. Column references use the AST's string names as ColID
// placeholders (ColID = Invalid, Name set) until the F5 binder is wired in
// (PROGRESS.md §2: I2 deliberately doesn't block on F5). FuncCall Caps use
// DefaultCaps until F7 lands.
package ir

import (
	"nzinfo/kql/internal/frontend/ast"
	"nzinfo/kql/internal/frontend/diagnostic"
	"nzinfo/kql/internal/frontend/token"
	"strings"
)

// Translate converts an AST node (a *ast.Script, *ast.QueryStmt, or *ast.Pipeline)
// into an IR Pipeline. Parse errors are recorded into diags; the returned
// Pipeline may be partial. The translator never panics on unsupported
// constructs — it records a KQL010 diagnostic instead.
func Translate(node ast.Node, diags *diagnostic.List) *Pipeline {
	t := &translator{diags: diags}
	switch n := node.(type) {
	case *ast.Pipeline:
		return t.translatePipeline(n)
	case *ast.QueryStmt:
		if n.Pipeline == nil {
			return nil
		}
		return t.translatePipeline(n.Pipeline)
	case *ast.Script:
		// Translate the first query statement; lets tests go through Parse().
		for _, stmt := range n.Statements {
			if q, ok := stmt.(*ast.QueryStmt); ok && q.Pipeline != nil {
				return t.translatePipeline(q.Pipeline)
			}
		}
		return nil
	}
	t.errorf(token.NoPos, "KQL010: cannot translate %T to IR (expected a pipeline)", node)
	return nil
}

// translator carries the diagnostic sink through a translation run.
type translator struct {
	diags *diagnostic.List
}

func (t *translator) errorf(pos token.Pos, format string, args ...interface{}) {
	if t.diags == nil {
		return
	}
	t.diags.Add(diagnostic.Diagnostic{
		Severity: diagnostic.Error,
		Code:     diagnostic.SyntaxError, // KQL005 for now; reserve KQL010 later
		Pos:      posOf(t.diags, pos),    // best-effort position
		Message:  sprintf(format, args...),
	})
}

// translatePipeline converts an *ast.Pipeline to an *ir.Pipeline.
func (t *translator) translatePipeline(p *ast.Pipeline) *Pipeline {
	out := &Pipeline{Position: p.Pos()}
	if p.Source != nil {
		out.Source = t.translateSource(p.Source)
	}
	for _, op := range p.Ops {
		// Top is special: one AST op expands to two IR stages (Sort + Limit).
		if top, ok := op.(*ast.TopOp); ok {
			out.Stages = append(out.Stages, t.translateTopOp(top)...)
			continue
		}
		if st := t.translateStage(op); st != nil {
			out.Stages = append(out.Stages, st)
		}
	}
	return out
}

// translateSource converts the pipeline source (an *ast.Ident table reference,
// a parenthesised sub-pipeline, or a dotted reference) to an ir.Source.
func (t *translator) translateSource(e ast.Expr) Source {
	switch n := e.(type) {
	case *ast.Ident:
		return &SourceTable{Position: n.Pos(), Table: n.Name}
	case *ast.SelectorExpr:
		// cluster.database.table style — flatten to name for now.
		return &SourceTable{Position: n.Pos(), Table: selectorToName(n)}
	case *ast.ParenExpr:
		return t.translateSource(n.X)
	case *ast.Pipeline:
		// Sub-pipeline as a source: wrap by translating it and using its first
		// stage's source. For MVP, surface as a table ref if simple.
		sub := t.translatePipeline(n)
		if sub.Source != nil && len(sub.Stages) == 0 {
			return sub.Source
		}
		return sub.Source // best-effort; complex sub-pipelines handled at Join
	case *ast.IndexExpr:
		// datatable(Schema)[data] — materialise from the inline data.
		if call, ok := n.X.(*ast.CallExpr); ok {
			if id, ok := call.Fun.(*ast.Ident); ok && strings.EqualFold(id.Name, "datatable") {
				return t.materialiseDatatable(n, call)
			}
			// externaldata — still a placeholder (needs a URL fetcher).
			return &SourceTable{Position: n.Pos(), Table: "externaldata"}
		}
		return &SourceTable{Position: n.Pos()}
	case *ast.CallExpr:
		// A bare call as a source (e.g. `union isfuzzy=true (...)` as a
		// function-form source, or print/range). Surface as a placeholder.
		if id, ok := n.Fun.(*ast.Ident); ok {
			return &SourceTable{Position: n.Pos(), Table: id.Name}
		}
		return &SourceTable{Position: n.Pos()}
	}
	t.errorf(e.Pos(), "KQL010: unsupported pipeline source %T", e)
	return &SourceTable{Position: e.Pos()}
}

// materialiseDatatable builds a SourceDatatableLit from a datatable(Schema)[data]
// AST expression. The schema names come from the call args (each is an Ident,
// possibly with a `:type` already stripped by the parser). The data rows come
// from the IndexExpr's List (flat values, nCols per row).
func (t *translator) materialiseDatatable(idx *ast.IndexExpr, call *ast.CallExpr) Source {
	// Extract column names from the schema args.
	var colNames []string
	for _, arg := range call.Args {
		if id, ok := arg.(*ast.Ident); ok {
			colNames = append(colNames, id.Name)
		}
	}
	nCols := len(colNames)

	// Extract data rows from the index list.
	var rows [][]Expr
	if list, ok := idx.Index.(*ast.ListExpr); ok {
		// Flat list: chunk into rows of nCols.
		allExprs := make([]Expr, 0, len(list.Elems))
		for _, el := range list.Elems {
			allExprs = append(allExprs, t.translateExpr(el))
		}
		for i := 0; i+nCols <= len(allExprs); i += nCols {
			row := make([]Expr, nCols)
			copy(row, allExprs[i:i+nCols])
			rows = append(rows, row)
		}
	}

	return &SourceDatatableLit{
		Position: idx.Pos(),
		ColNames: colNames,
		Rows:     rows,
	}
}
func selectorToName(s *ast.SelectorExpr) string {
	switch x := s.X.(type) {
	case *ast.Ident:
		return x.Name + "." + s.Sel.Name
	case *ast.SelectorExpr:
		return selectorToName(x) + "." + s.Sel.Name
	}
	return s.Sel.Name
}
