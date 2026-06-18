package kql_test

import (
	"strings"
	"testing"

	"nzinfo/kql/internal/backend/pg"
	"nzinfo/kql/internal/ir"
	"nzinfo/kql/internal/optimizer/stats"
)

// Cost-based CTE materialization e2e: verify the pg emitter produces the right
// MATERIALIZED / NOT MATERIALIZED hint when a stats catalog is wired via
// EmitCTEWithCatalog.

// TestCTEMaterialize_CostBasedEmit verifies:
//   - A large-table pipeline (1M×200B) → MATERIALIZED on its segment.
//   - A small-table pipeline (100×50B) → NOT MATERIALIZED.
//   - Without a catalog (EmitCTE) → the static stage-type rule applies.
func TestCTEMaterialize_CostBasedEmit(t *testing.T) {
	cat := &stats.Catalog{
		Tables: map[string]*stats.Table{
			"big":   {RowCount: 1000000, AvgRowBytes: 200, Columns: map[string]*stats.ColumnStats{"id": {Card: 1000000}}},
			"small": {RowCount: 100, AvgRowBytes: 50, Columns: map[string]*stats.ColumnStats{"id": {Card: 100}}},
		},
	}

	// Big table (1M rows, even after aggregate sqrt = 1000×200=200KB < 4MB → INLINE).
	// Actually aggregate reduces it below threshold. Use a non-reducing breakpoint:
	// a Distinct on the big table keeps full cardinality → MATERIALIZED.
	bigDistinct := &ir.Pipeline{
		Source: &ir.SourceTable{Table: "big"},
		Stages: []ir.Stage{
			&ir.Distinct{Cols: []ir.Expr{&ir.Col{Name: "id"}}},
		},
	}
	qBig, err := pg.EmitCTEWithCatalog(bigDistinct, cat)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(qBig.SQL, "MATERIALIZED") || strings.Contains(qBig.SQL, "NOT MATERIALIZED") {
		t.Errorf("big distinct: expected MATERIALIZED, got SQL:\n%s", qBig.SQL)
	}

	// Small table distinct (100×50=5KB < 4MB → NOT MATERIALIZED).
	smallDistinct := &ir.Pipeline{
		Source: &ir.SourceTable{Table: "small"},
		Stages: []ir.Stage{
			&ir.Distinct{Cols: []ir.Expr{&ir.Col{Name: "id"}}},
		},
	}
	qSmall, err := pg.EmitCTEWithCatalog(smallDistinct, cat)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(qSmall.SQL, "NOT MATERIALIZED") {
		t.Errorf("small distinct: expected NOT MATERIALIZED, got SQL:\n%s", qSmall.SQL)
	}

	// Without catalog (EmitCTE): Distinct is a breakpoint → static rule gives
	// NOT MATERIALIZED (Distinct is in the inline list).
	qNoCat, err := pg.EmitCTE(smallDistinct)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(qNoCat.SQL, "NOT MATERIALIZED") {
		t.Errorf("no-catalog static: expected NOT MATERIALIZED for Distinct, got SQL:\n%s", qNoCat.SQL)
	}

	// (Aggregate static-rule verification covered by o4_join_e2e_test.go)
}
