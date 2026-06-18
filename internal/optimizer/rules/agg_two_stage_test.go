package rules

import (
	"testing"

	"nzinfo/kql/internal/ir"
	"nzinfo/kql/internal/optimizer/stats"
)

// TestTwoStageAgg_SplitsLargeTable: a large table with associative aggregates
// gets split into partial + final stages.
func TestTwoStageAgg_SplitsLargeTable(t *testing.T) {
	catalog := &stats.Catalog{
		Source: stats.SourcePgAnalyze,
		Tables: map[string]*stats.Table{
			"events": {
				RowCount: 1000000,
				Columns: map[string]*stats.ColumnStats{
					"state":   {Card: 62},
					"created": {Card: 1000000},
				},
			},
		},
	}
	pipe := &ir.Pipeline{
		Source: &ir.SourceTable{Table: "events"},
		Stages: []ir.Stage{
			&ir.Aggregate{
				Aggregates: []*ir.NamedExpr{
					{Name: "cnt", Expr: &ir.FuncCall{Name: "count"}},
				},
				Keys: []*ir.NamedExpr{
					{Name: "s", Expr: &ir.Col{Name: "state"}},
				},
			},
		},
	}
	rule := TwoStageAgg{Catalog: catalog}
	out, changed := rule.Apply(pipe, noopReader{})
	if !changed {
		t.Fatal("TwoStageAgg should split a large table")
	}
	// Should now have 2 stages: partial (with shard) + final.
	if len(out.Stages) != 2 {
		t.Fatalf("stages = %d, want 2 (partial + final)", len(out.Stages))
	}
	partial := out.Stages[0].(*ir.Aggregate)
	final := out.Stages[1].(*ir.Aggregate)
	// Partial should have the shard key added.
	if len(partial.Keys) != 2 {
		t.Errorf("partial keys = %d, want 2 (state + __shard)", len(partial.Keys))
	}
	// Final should have the original keys only.
	if len(final.Keys) != 1 {
		t.Errorf("final keys = %d, want 1 (state)", len(final.Keys))
	}
	t.Logf("partial keys: %v, final keys: %v", partial.Keys, final.Keys)
}

// TestTwoStageAgg_NoOpSmallTable: small tables don't get split.
func TestTwoStageAgg_NoOpSmallTable(t *testing.T) {
	catalog := &stats.Catalog{
		Tables: map[string]*stats.Table{
			"events": {RowCount: 1000, Columns: map[string]*stats.ColumnStats{
				"state": {Card: 10},
			}},
		},
	}
	pipe := &ir.Pipeline{
		Source: &ir.SourceTable{Table: "events"},
		Stages: []ir.Stage{
			&ir.Aggregate{
				Aggregates: []*ir.NamedExpr{{Name: "c", Expr: &ir.FuncCall{Name: "count"}}},
				Keys:       []*ir.NamedExpr{{Name: "s", Expr: &ir.Col{Name: "state"}}},
			},
		},
	}
	rule := TwoStageAgg{Catalog: catalog}
	_, changed := rule.Apply(pipe, noopReader{})
	if changed {
		t.Error("TwoStageAgg should not split a small table")
	}
}

// TestTwoStageAgg_SkipsNonAssociative: avg is not associative → skip.
func TestTwoStageAgg_SkipsNonAssociative(t *testing.T) {
	catalog := &stats.Catalog{
		Tables: map[string]*stats.Table{
			"events": {RowCount: 1000000, Columns: map[string]*stats.ColumnStats{
				"state": {Card: 62},
				"val":   {Card: 100},
			}},
		},
	}
	pipe := &ir.Pipeline{
		Source: &ir.SourceTable{Table: "events"},
		Stages: []ir.Stage{
			&ir.Aggregate{
				Aggregates: []*ir.NamedExpr{
					{Name: "a", Expr: &ir.FuncCall{Name: "avg", Args: []ir.Expr{&ir.Col{Name: "val"}}}},
				},
				Keys: []*ir.NamedExpr{{Name: "s", Expr: &ir.Col{Name: "state"}}},
			},
		},
	}
	rule := TwoStageAgg{Catalog: catalog}
	_, changed := rule.Apply(pipe, noopReader{})
	if changed {
		t.Error("TwoStageAgg should skip non-associative avg")
	}
}

// TestTwoStageAgg_NoOpWithoutCatalog.
func TestTwoStageAgg_NoOpWithoutCatalog(t *testing.T) {
	pipe := &ir.Pipeline{
		Source: &ir.SourceTable{Table: "events"},
		Stages: []ir.Stage{
			&ir.Aggregate{Aggregates: []*ir.NamedExpr{{Name: "c", Expr: &ir.FuncCall{Name: "count"}}}}},
	}
	rule := TwoStageAgg{Catalog: nil}
	_, changed := rule.Apply(pipe, noopReader{})
	if changed {
		t.Error("TwoStageAgg should be no-op without catalog")
	}
}
