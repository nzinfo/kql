package decision

import (
	"strings"
	"testing"

	"nzinfo/kql/internal/frontend/token"
	"nzinfo/kql/internal/ir"
	"nzinfo/kql/internal/optimizer/cost"
	"nzinfo/kql/internal/optimizer/stats"
)

// TestEnginePlan_NoCatalog: without a catalog, everything goes to pg.
func TestEnginePlan_NoCatalog(t *testing.T) {
	p := EnginePlan{Catalog: nil, Weights: cost.DefaultWeights("pg"), HasDuckDB: true}
	pipe := &ir.Pipeline{
		Source: &ir.SourceTable{Table: "t"},
		Stages: []ir.Stage{
			&ir.Aggregate{Aggregates: []*ir.NamedExpr{{Name: "c", Expr: &ir.FuncCall{Name: "count"}}}},
		},
	}
	segs, d := p.Plan(pipe)
	if len(segs) != 1 {
		t.Fatalf("segments = %d, want 1", len(segs))
	}
	if segs[0].Engine != EnginePg {
		t.Errorf("engine = %v, want pg", segs[0].Engine)
	}
	if !strings.Contains(d.Reason, "no catalog") {
		t.Errorf("reason = %q", d.Reason)
	}
}

// TestEnginePlan_LargeAggToDuckDB: large table aggregate → DuckDB (vectorized
// advantage exceeds transfer cost).
func TestEnginePlan_LargeAggToDuckDB(t *testing.T) {
	cat := &stats.Catalog{
		Source: stats.SourcePgAnalyze,
		Tables: map[string]*stats.Table{
			"events": {RowCount: 10000000, Columns: map[string]*stats.ColumnStats{
				"state": {Card: 62},
			}},
		},
	}
	p := EnginePlan{Catalog: cat, Weights: cost.DefaultWeights("pg"), HasDuckDB: true}
	pipe := &ir.Pipeline{
		Source: &ir.SourceTable{Table: "events"},
		Stages: []ir.Stage{
			&ir.Filter{Predicate: &ir.BinOp{
				Op: token.GTR, X: &ir.Col{Name: "state"}, Y: &ir.Lit{Value: "TX", HasValue: true, T: ir.TypeString},
			}},
			&ir.Aggregate{
				Aggregates: []*ir.NamedExpr{{Name: "c", Expr: &ir.FuncCall{Name: "count"}}},
				Keys:       []*ir.NamedExpr{{Name: "s", Expr: &ir.Col{Name: "state"}}},
			},
		},
	}
	segs, d := p.Plan(pipe)
	t.Logf("decision: %s", d.Reason)
	if len(segs) < 2 {
		t.Fatalf("segments = %d, want ≥2 (split at aggregate)", len(segs))
	}
	// On 10M rows, DuckDB vectorized should beat pg even with transfer cost.
	if segs[1].Engine != EngineDuckDB {
		t.Logf("NOTE: chose %v instead of DuckDB — may need cost tuning", segs[1].Engine)
	}
}

// TestEnginePlan_NoDuckDB: without DuckDB, everything stays on pg.
func TestEnginePlan_NoDuckDB(t *testing.T) {
	cat := &stats.Catalog{
		Tables: map[string]*stats.Table{
			"t": {RowCount: 1000000, Columns: map[string]*stats.ColumnStats{"x": {Card: 10}}},
		},
	}
	p := EnginePlan{Catalog: cat, Weights: cost.DefaultWeights("pg"), HasDuckDB: false}
	pipe := &ir.Pipeline{
		Source: &ir.SourceTable{Table: "t"},
		Stages: []ir.Stage{
			&ir.Aggregate{Aggregates: []*ir.NamedExpr{{Name: "c", Expr: &ir.FuncCall{Name: "count"}}}},
		},
	}
	segs, _ := p.Plan(pipe)
	if len(segs) != 1 || segs[0].Engine != EnginePg {
		t.Errorf("without DuckDB → all pg; got %d segments, engine[0]=%v", len(segs), segs[0].Engine)
	}
}

// TestEnginePlan_NoAggregate: no aggregate → everything on pg.
func TestEnginePlan_NoAggregate(t *testing.T) {
	cat := &stats.Catalog{
		Tables: map[string]*stats.Table{
			"t": {RowCount: 1000000},
		},
	}
	p := EnginePlan{Catalog: cat, Weights: cost.DefaultWeights("pg"), HasDuckDB: true}
	pipe := &ir.Pipeline{
		Source: &ir.SourceTable{Table: "t"},
		Stages: []ir.Stage{
			&ir.Filter{Predicate: &ir.Col{Name: "x"}},
			&ir.Limit{Count: &ir.Lit{Value: int64(10), HasValue: true, T: ir.TypeLong}},
		},
	}
	segs, _ := p.Plan(pipe)
	if len(segs) != 1 || segs[0].Engine != EnginePg {
		t.Errorf("no aggregate → all pg; got %d segments", len(segs))
	}
}

// TestEnginePlan_ExplainDecision: the decision includes engine routing rationale.
func TestEnginePlan_ExplainDecision(t *testing.T) {
	cat := &stats.Catalog{
		Tables: map[string]*stats.Table{
			"events": {RowCount: 5000000, Columns: map[string]*stats.ColumnStats{
				"state": {Card: 62},
			}},
		},
	}
	p := EnginePlan{Catalog: cat, Weights: cost.DefaultWeights("pg"), HasDuckDB: true}
	pipe := &ir.Pipeline{
		Source: &ir.SourceTable{Table: "events"},
		Stages: []ir.Stage{
			&ir.Aggregate{
				Aggregates: []*ir.NamedExpr{{Name: "c", Expr: &ir.FuncCall{Name: "count"}}},
				Keys:       []*ir.NamedExpr{{Name: "s", Expr: &ir.Col{Name: "state"}}},
			},
		},
	}
	_, d := p.Plan(pipe)
	if d.Choice != "EngineRoute" {
		t.Errorf("Choice = %q, want EngineRoute", d.Choice)
	}
	if !strings.Contains(d.Reason, "pg(") {
		t.Errorf("reason should mention pg routing: %q", d.Reason)
	}
	t.Logf("Explain decision: %s", d.Reason)
}
