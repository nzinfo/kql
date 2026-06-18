package rules

import (
	"testing"

	"nzinfo/kql/internal/frontend/token"
	"nzinfo/kql/internal/ir"
	"nzinfo/kql/internal/optimizer/stats"
)

// TestViewMatch_ReplacesSourceWithView: when the catalog has a view whose
// definition matches the query's summarize pattern, ViewMatch replaces the
// source table with the view and removes the summarize stage.
func TestViewMatch_ReplacesSourceWithView(t *testing.T) {
	catalog := &stats.Catalog{
		Source: stats.SourcePgAnalyze,
		Tables: map[string]*stats.Table{
			"orders": {RowCount: 1000000},
		},
		Views: map[string]*stats.ViewDef{
			"orders_daily_summary": {
				Definition: `orders | summarize count() by bin(created_at, 1d)`,
			},
		},
	}
	pipe := &ir.Pipeline{
		Source: &ir.SourceTable{Table: "orders"},
		Stages: []ir.Stage{
			&ir.Aggregate{
				Aggregates: []*ir.NamedExpr{
					{Name: "count_", Expr: &ir.FuncCall{Name: "count"}},
				},
				Keys: []*ir.NamedExpr{
					{Name: "day", Expr: &ir.FuncCall{Name: "bin", Args: []ir.Expr{&ir.Col{Name: "created_at"}}}},
				},
			},
		},
	}
	rule := ViewMatch{Catalog: catalog}
	out, changed := rule.Apply(pipe, noopReader{})
	if !changed {
		t.Fatal("ViewMatch should rewrite when a matching view exists")
	}
	// Source should now be the view name.
	st := out.Source.(*ir.SourceTable)
	if st.Table != "orders_daily_summary" {
		t.Errorf("source = %q, want orders_daily_summary", st.Table)
	}
	// The summarize stage should be removed (baked into the view).
	if len(out.Stages) != 0 {
		t.Errorf("stages = %d, want 0 (summarize removed)", len(out.Stages))
	}
}

// TestViewMatch_NoOpWithoutCatalog: without a catalog, ViewMatch does nothing.
func TestViewMatch_NoOpWithoutCatalog(t *testing.T) {
	pipe := &ir.Pipeline{
		Source: &ir.SourceTable{Table: "orders"},
		Stages: []ir.Stage{
			&ir.Aggregate{Aggregates: []*ir.NamedExpr{{Name: "c", Expr: &ir.FuncCall{Name: "count"}}}},
		},
	}
	rule := ViewMatch{Catalog: nil}
	out, changed := rule.Apply(pipe, noopReader{})
	if changed {
		t.Error("ViewMatch should be a no-op without a catalog")
	}
	if out != pipe {
		t.Error("pipeline should be unchanged")
	}
}

// TestViewMatch_NoMatchWhenViewDoesntReferenceBaseTable: the view must
// reference the same base table.
func TestViewMatch_NoMatchWhenViewDoesntReferenceBaseTable(t *testing.T) {
	catalog := &stats.Catalog{
		Views: map[string]*stats.ViewDef{
			"other_summary": {Definition: `other_table | summarize count() by bin(x, 1d)`},
		},
	}
	pipe := &ir.Pipeline{
		Source: &ir.SourceTable{Table: "orders"},
		Stages: []ir.Stage{
			&ir.Aggregate{Aggregates: []*ir.NamedExpr{{Name: "c", Expr: &ir.FuncCall{Name: "count"}}}},
		},
	}
	rule := ViewMatch{Catalog: catalog}
	_, changed := rule.Apply(pipe, noopReader{})
	if changed {
		t.Error("ViewMatch should not match when view references a different base table")
	}
}

// TestViewMatch_NoMatchWhenAggregateMissing: the view must contain all
// aggregate function names from the query.
func TestViewMatch_NoMatchWhenAggregateMissing(t *testing.T) {
	catalog := &stats.Catalog{
		Views: map[string]*stats.ViewDef{
			"orders_summary": {Definition: `orders | summarize sum(total) by bin(x, 1d)`},
		},
	}
	pipe := &ir.Pipeline{
		Source: &ir.SourceTable{Table: "orders"},
		Stages: []ir.Stage{
			&ir.Aggregate{Aggregates: []*ir.NamedExpr{
				{Name: "c", Expr: &ir.FuncCall{Name: "count"}},
				{Name: "s", Expr: &ir.FuncCall{Name: "sum", Args: []ir.Expr{&ir.Col{Name: "total"}}}},
			}},
		},
	}
	rule := ViewMatch{Catalog: catalog}
	_, changed := rule.Apply(pipe, noopReader{})
	if changed {
		t.Error("ViewMatch should not match when view is missing 'count' aggregate")
	}
}

// TestViewMatch_NoOpWithoutSummarize: a pipeline without a summarize stage
// can't match a view.
func TestViewMatch_NoOpWithoutSummarize(t *testing.T) {
	catalog := &stats.Catalog{
		Views: map[string]*stats.ViewDef{
			"orders_summary": {Definition: `orders | summarize count()`},
		},
	}
	pipe := &ir.Pipeline{
		Source: &ir.SourceTable{Table: "orders"},
		Stages: []ir.Stage{
			&ir.Filter{Predicate: &ir.BinOp{Op: token.GTR, X: &ir.Col{Name: "x"}, Y: &ir.Lit{Value: int64(1), HasValue: true, T: ir.TypeLong}}},
		},
	}
	rule := ViewMatch{Catalog: catalog}
	_, changed := rule.Apply(pipe, noopReader{})
	if changed {
		t.Error("ViewMatch should not match without a summarize stage")
	}
}

// TestViewMatch_EmptyCatalog: empty views map → no-op.
func TestViewMatch_EmptyCatalog(t *testing.T) {
	catalog := &stats.Catalog{
		Views: map[string]*stats.ViewDef{},
	}
	pipe := &ir.Pipeline{
		Source: &ir.SourceTable{Table: "orders"},
		Stages: []ir.Stage{
			&ir.Aggregate{Aggregates: []*ir.NamedExpr{{Name: "c", Expr: &ir.FuncCall{Name: "count"}}}},
		},
	}
	rule := ViewMatch{Catalog: catalog}
	_, changed := rule.Apply(pipe, noopReader{})
	if changed {
		t.Error("ViewMatch should be no-op with empty views map")
	}
}
