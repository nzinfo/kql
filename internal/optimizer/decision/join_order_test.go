package decision

import (
	"testing"

	"nzinfo/kql/internal/frontend/token"
	"nzinfo/kql/internal/ir"
	"nzinfo/kql/internal/optimizer/cost"
	"nzinfo/kql/internal/optimizer/stats"
)

// joinOrderCatalog builds a 3-table catalog for join-order tests:
//   base (A): 100 rows
//   B: 1,000,000 rows (large — should be joined LAST, not first)
//   C: 50 rows (small — cheap to join early)
// All joined on column "k" with cardinality matching row count (no correlation).
// Optimal order: A ⋈ C (50→100 = small), then ⋈ B last.
// Text order A ⋈ B ⋈ C builds the huge A⋈B first — strictly worse.
func joinOrderCatalog() *stats.Catalog {
	return &stats.Catalog{
		Source: stats.SourcePgAnalyze,
		Tables: map[string]*stats.Table{
			"A": {RowCount: 100, AvgRowBytes: 100, Columns: map[string]*stats.ColumnStats{
				"k": {Card: 100},
			}},
			"B": {RowCount: 1000000, AvgRowBytes: 200, Columns: map[string]*stats.ColumnStats{
				"k": {Card: 1000000},
			}},
			"C": {RowCount: 50, AvgRowBytes: 50, Columns: map[string]*stats.ColumnStats{
				"k": {Card: 50},
			}},
		},
	}
}

// joinOnA is a simple equality ON condition: col "k" == col "k".
func joinOnK() []ir.Expr {
	return []ir.Expr{&ir.BinOp{Op: token.EQL, X: &ir.Col{Name: "k"}, Y: &ir.Col{Name: "k"}}}
}

// TestEnumerateJoinOrder_PrefersSmallFirst: with base A (100), B (1M), C (50),
// the optimal left-deep order joins C before B (small intermediate result).
func TestEnumerateJoinOrder_PrefersSmallFirst(t *testing.T) {
	c := joinOrderCatalog()
	w := cost.DefaultWeights("pg")
	rights := []string{"B", "C"} // text order: A ⋈ B ⋈ C
	joinOns := map[string][]ir.Expr{"B": joinOnK(), "C": joinOnK()}

	got := EnumerateJoinOrder(c, "A", 100, rights, joinOns, w)
	// Optimal: join C (50) before B (1M) → ["C", "B"]
	if len(got) != 2 || got[0] != "C" || got[1] != "B" {
		t.Errorf("EnumerateJoinOrder = %v, want [C B] (small table first)", got)
	}
}

// TestEnumerateJoinOrder_NoStatsReturnsInput: without stats, returns input order.
func TestEnumerateJoinOrder_NoStatsReturnsInput(t *testing.T) {
	w := cost.DefaultWeights("pg")
	rights := []string{"B", "C"}
	joinOns := map[string][]ir.Expr{"B": joinOnK(), "C": joinOnK()}
	got := EnumerateJoinOrder(nil, "A", 100, rights, joinOns, w)
	if len(got) != 2 || got[0] != "B" || got[1] != "C" {
		t.Errorf("nil catalog: got %v, want input [B C]", got)
	}
}

// TestEnumerateJoinOrder_SingleJoinNoReorder: one join → no reorder possible.
func TestEnumerateJoinOrder_SingleJoinNoReorder(t *testing.T) {
	c := joinOrderCatalog()
	w := cost.DefaultWeights("pg")
	got := EnumerateJoinOrder(c, "A", 100, []string{"B"}, map[string][]ir.Expr{"B": joinOnK()}, w)
	if len(got) != 1 || got[0] != "B" {
		t.Errorf("single join: got %v, want [B]", got)
	}
}

// TestEnumerateJoinOrder_MissingJoinOnReturnsInput: if a join's ON is missing,
// can't cost → return input (safe).
func TestEnumerateJoinOrder_MissingJoinOnReturnsInput(t *testing.T) {
	c := joinOrderCatalog()
	w := cost.DefaultWeights("pg")
	rights := []string{"B", "C"}
	joinOns := map[string][]ir.Expr{"B": joinOnK()} // C missing
	got := EnumerateJoinOrder(c, "A", 100, rights, joinOns, w)
	if len(got) != 2 || got[0] != "B" || got[1] != "C" {
		t.Errorf("missing ON: got %v, want input [B C]", got)
	}
}

// TestApplyJoinOrder_ReordersStages: A ⋈ B ⋈ C (text) → A ⋈ C ⋈ B (optimal).
func TestApplyJoinOrder_ReordersStages(t *testing.T) {
	c := joinOrderCatalog()
	w := cost.DefaultWeights("pg")
	pipe := &ir.Pipeline{
		Source: &ir.SourceTable{Table: "A"},
		Stages: []ir.Stage{
			&ir.Join{Kind: ir.JoinInner, Right: &ir.Pipeline{Source: &ir.SourceTable{Table: "B"}}, On: joinOnK()},
			&ir.Join{Kind: ir.JoinInner, Right: &ir.Pipeline{Source: &ir.SourceTable{Table: "C"}}, On: joinOnK()},
		},
	}
	out, changed := ApplyJoinOrder(pipe, c, w)
	if !changed {
		t.Fatal("ApplyJoinOrder: changed=false, want true (reorder expected)")
	}
	j0 := out.Stages[0].(*ir.Join)
	j1 := out.Stages[1].(*ir.Join)
	gotRight0 := joinRightTableName(j0)
	gotRight1 := joinRightTableName(j1)
	// Optimal: C first (small), B last (large).
	if gotRight0 != "C" || gotRight1 != "B" {
		t.Errorf("after reorder: right tables = [%s, %s], want [C, B]", gotRight0, gotRight1)
	}
}

// TestApplyJoinOrder_NonCommutableStaysPut: a LEFT join chain is NOT reordered
// (left/right/full change row survival semantics).
func TestApplyJoinOrder_NonCommutableStaysPut(t *testing.T) {
	c := joinOrderCatalog()
	w := cost.DefaultWeights("pg")
	pipe := &ir.Pipeline{
		Source: &ir.SourceTable{Table: "A"},
		Stages: []ir.Stage{
			&ir.Join{Kind: ir.JoinLeftOuter, Right: &ir.Pipeline{Source: &ir.SourceTable{Table: "B"}}, On: joinOnK()},
			&ir.Join{Kind: ir.JoinLeftOuter, Right: &ir.Pipeline{Source: &ir.SourceTable{Table: "C"}}, On: joinOnK()},
		},
	}
	out, changed := ApplyJoinOrder(pipe, c, w)
	if changed {
		j0 := out.Stages[0].(*ir.Join)
		j1 := out.Stages[1].(*ir.Join)
		t.Errorf("LEFT join reordered (should stay): [%s, %s]",
			joinRightTableName(j0), joinRightTableName(j1))
	}
}

// TestApplyJoinOrder_NilCatalogNoOp: nil catalog → no-op.
func TestApplyJoinOrder_NilCatalogNoOp(t *testing.T) {
	w := cost.DefaultWeights("pg")
	pipe := &ir.Pipeline{
		Source: &ir.SourceTable{Table: "A"},
		Stages: []ir.Stage{
			&ir.Join{Kind: ir.JoinInner, Right: &ir.Pipeline{Source: &ir.SourceTable{Table: "B"}}, On: joinOnK()},
			&ir.Join{Kind: ir.JoinInner, Right: &ir.Pipeline{Source: &ir.SourceTable{Table: "C"}}, On: joinOnK()},
		},
	}
	_, changed := ApplyJoinOrder(pipe, nil, w)
	if changed {
		t.Error("nil catalog: changed=true, want false (no-op)")
	}
}

// TestApplyJoinOrder_MixedChainReordersCommutableOnly: a chain with inner,
// inner, then LEFT — the first two (inner) are reorderable; the LEFT is not.
func TestApplyJoinOrder_MixedChainReordersCommutableOnly(t *testing.T) {
	c := joinOrderCatalog()
	w := cost.DefaultWeights("pg")
	pipe := &ir.Pipeline{
		Source: &ir.SourceTable{Table: "A"},
		Stages: []ir.Stage{
			&ir.Join{Kind: ir.JoinInner, Right: &ir.Pipeline{Source: &ir.SourceTable{Table: "B"}}, On: joinOnK()},
			&ir.Join{Kind: ir.JoinInner, Right: &ir.Pipeline{Source: &ir.SourceTable{Table: "C"}}, On: joinOnK()},
			&ir.Join{Kind: ir.JoinLeftOuter, Right: &ir.Pipeline{Source: &ir.SourceTable{Table: "D"}}, On: joinOnK()},
		},
	}
	// Add D to catalog.
	c.Tables["D"] = &stats.Table{RowCount: 10, AvgRowBytes: 10, Columns: map[string]*stats.ColumnStats{"k": {Card: 10}}}
	out, changed := ApplyJoinOrder(pipe, c, w)
	if !changed {
		t.Fatal("mixed chain: changed=false, want true (B,C reorderable)")
	}
	// The LEFT join to D must remain last (position 2).
	j2 := out.Stages[2].(*ir.Join)
	if joinRightTableName(j2) != "D" {
		t.Errorf("LEFT join to D moved to %s; must stay last", joinRightTableName(j2))
	}
	// The first two should be reordered to [C, B] (small first).
	j0 := out.Stages[0].(*ir.Join)
	j1 := out.Stages[1].(*ir.Join)
	r0, r1 := joinRightTableName(j0), joinRightTableName(j1)
	if !(r0 == "C" && r1 == "B") {
		t.Errorf("commutable pair = [%s, %s], want [C, B]", r0, r1)
	}
}
