package decision

import (
	"testing"

	"nzinfo/kql/internal/frontend/token"
	"nzinfo/kql/internal/ir"
	"nzinfo/kql/internal/optimizer/stats"
)

// CTE materialization cost-based decision tests (O6).

// matCatalog: a large table (1M rows × 200 bytes = 200MB → exceeds 4MB
// threshold → MATERIALIZED) and a small table (100 × 50 = 5KB → INLINE).
func matCatalog() *stats.Catalog {
	return &stats.Catalog{
		Tables: map[string]*stats.Table{
			"big":   {RowCount: 1000000, AvgRowBytes: 200, Columns: map[string]*stats.ColumnStats{"id": {Card: 1000000}}},
			"small": {RowCount: 100, AvgRowBytes: 50, Columns: map[string]*stats.ColumnStats{"id": {Card: 100}}},
		},
	}
}

// TestShouldMaterialize_LargeTable: 1M rows × 200 bytes = 200MB > 4MB → FORCE.
func TestShouldMaterialize_LargeTable(t *testing.T) {
	c := matCatalog()
	// A bare source-only segment (no reducing stages) → output = full table.
	seg := []ir.Stage{}
	got := ShouldMaterialize(c, "big", seg)
	if got != MatForceMaterialize {
		t.Errorf("big table: got %v, want MatForceMaterialize (200MB > 4MB)", got)
	}
}

// TestShouldMaterialize_SmallTable: 100 × 50 = 5KB < 4MB → INLINE.
func TestShouldMaterialize_SmallTable(t *testing.T) {
	c := matCatalog()
	seg := []ir.Stage{}
	got := ShouldMaterialize(c, "small", seg)
	if got != MatForceInline {
		t.Errorf("small table: got %v, want MatForceInline (5KB < 4MB)", got)
	}
}

// TestShouldMaterialize_LargeTableWithLimit: big table + LIMIT 10 → output
// shrinks to 10 × 200 = 2KB < 4MB → INLINE (limit makes it cheap to recompute).
func TestShouldMaterialize_LargeTableWithLimit(t *testing.T) {
	c := matCatalog()
	seg := []ir.Stage{
		&ir.Limit{Count: &ir.Lit{T: ir.TypeLong, Value: 10}},
	}
	got := ShouldMaterialize(c, "big", seg)
	if got != MatForceInline {
		t.Errorf("big + limit 10: got %v, want MatForceInline (2KB after limit)", got)
	}
}

// TestShouldMaterialize_LargeTableWithFilter: big table + filter (×0.1) →
// 100K × 200 = 20MB > 4MB → still MATERIALIZED.
func TestShouldMaterialize_LargeTableWithFilter(t *testing.T) {
	c := matCatalog()
	seg := []ir.Stage{
		&ir.Filter{Predicate: &ir.BinOp{Op: token.EQL, X: &ir.Col{Name: "id"}, Y: &ir.Lit{}}},
	}
	got := ShouldMaterialize(c, "big", seg)
	if got != MatForceMaterialize {
		t.Errorf("big + filter: got %v, want MatForceMaterialize (20MB > 4MB after 0.1 selectivity)", got)
	}
}

// TestShouldMaterialize_AggregateReduces: big table + aggregate → output ≈
// sqrt(1M)=1000 rows × 200 = 200KB < 4MB → INLINE.
func TestShouldMaterialize_AggregateReduces(t *testing.T) {
	c := matCatalog()
	seg := []ir.Stage{
		&ir.Aggregate{Keys: []*ir.NamedExpr{{Name: "k"}}, Aggregates: []*ir.NamedExpr{{Name: "c", Expr: &ir.FuncCall{Name: "count"}}}},
	}
	got := ShouldMaterialize(c, "big", seg)
	if got != MatForceInline {
		t.Errorf("big + aggregate: got %v, want MatForceInline (sqrt reduction → 200KB)", got)
	}
}

// TestShouldMaterialize_NilCatalog: no catalog → MatDefault (static fallback).
func TestShouldMaterialize_NilCatalog(t *testing.T) {
	got := ShouldMaterialize(nil, "big", []ir.Stage{})
	if got != MatDefault {
		t.Errorf("nil catalog: got %v, want MatDefault", got)
	}
}

// TestShouldMaterialize_UnknownTable: catalog present but table not in it →
// MatDefault (can't estimate).
func TestShouldMaterialize_UnknownTable(t *testing.T) {
	c := matCatalog()
	got := ShouldMaterialize(c, "nonexistent", []ir.Stage{})
	if got != MatDefault {
		t.Errorf("unknown table: got %v, want MatDefault", got)
	}
}

// TestShouldMaterialize_EmptySource: empty source table → MatDefault.
func TestShouldMaterialize_EmptySource(t *testing.T) {
	c := matCatalog()
	got := ShouldMaterialize(c, "", []ir.Stage{})
	if got != MatDefault {
		t.Errorf("empty source: got %v, want MatDefault", got)
	}
}
