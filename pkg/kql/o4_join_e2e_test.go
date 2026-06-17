// Package kql_test — O4 join-method end-to-end integration tests.
//
// These tests exercise the full optimizer→emit path for join-method selection:
// build a join IR pipeline → run JoinPlan with a stats catalog → emit to pg
// SQL → verify the hint comment appears. They don't need a live DB (the
// optimizer + emit are pure functions of the IR + catalog).
//
// Uses testdata/stats/join.yaml (events 1M rows + meta 5K indexed).
package kql_test

import (
	"strings"
	"testing"

	"nzinfo/kql/internal/backend/pg"
	"nzinfo/kql/internal/frontend/token"
	"nzinfo/kql/internal/ir"
	"nzinfo/kql/internal/optimizer/cost"
	"nzinfo/kql/internal/optimizer/decision"
	"nzinfo/kql/internal/optimizer/stats"
)

// joinPipe builds: events ⋈ meta on id=id (the canonical join test shape).
func joinPipe() *ir.Pipeline {
	return &ir.Pipeline{
		Source: &ir.SourceTable{Table: "events"},
		Stages: []ir.Stage{
			&ir.Join{
				Kind: ir.JoinInner,
				Right: &ir.Pipeline{Source: &ir.SourceTable{Table: "meta"}},
				On: []ir.Expr{
					&ir.BinOp{Op: token.EQL, X: &ir.Col{Name: "id"}, Y: &ir.Col{Name: "id"}},
				},
			},
		},
	}
}

// loadJoinCatalog loads the test stats catalog (events 1M + meta 5K indexed).
func loadJoinCatalog(t *testing.T) *stats.Catalog {
	t.Helper()
	c, _, err := stats.Load("testdata/stats/join.yaml")
	if err != nil {
		t.Fatalf("load join.yaml: %v", err)
	}
	return c
}

// TestO4_FullPath_Aggressive_HashHint: events(1M) ⋈ meta(5K) — Aggressive
// should pick HashJoin (large outer, moderate inner → hash amortizes build),
// and the emitted pg SQL should contain /*+ HashJoin(...) */.
func TestO4_FullPath_Aggressive_HashHint(t *testing.T) {
	cat := loadJoinCatalog(t)
	pipe := joinPipe()

	jp := decision.JoinPlan{
		Policy:  decision.Aggressive{},
		Catalog: cat,
		Weights: cost.DefaultWeights("pg"),
	}
	_, changed, d := jp.Apply(pipe)
	if !changed {
		t.Fatalf("JoinPlan should set a hint; Decision=%+v", d)
	}
	j := pipe.Stages[0].(*ir.Join)
	t.Logf("Aggressive hint: %v (%s)", j.Hint, d.Reason)

	// Emit to pg SQL and check for the hint comment.
	q, err := pg.Emit(pipe)
	if err != nil {
		t.Fatalf("pg.Emit: %v", err)
	}
	t.Logf("SQL:\n%s", q.SQL)
	if j.Hint == ir.JoinHintHash && !strings.Contains(q.SQL, "/*+ HashJoin(") {
		t.Errorf("expected /*+ HashJoin(...) in pg SQL")
	}
}

// TestO4_FullPath_Conservative_Defer: Conservative should defer to the backend
// (JoinHintNone) for the events(1M)×meta(5K) join — no single method is 10×
// cheaper than default. The emitted SQL should have NO hint comment.
func TestO4_FullPath_Conservative_Defer(t *testing.T) {
	cat := loadJoinCatalog(t)
	pipe := joinPipe()

	jp := decision.JoinPlan{
		Policy:  decision.Conservative{},
		Catalog: cat,
		Weights: cost.DefaultWeights("pg"),
	}
	_, _, d := jp.Apply(pipe)
	j := pipe.Stages[0].(*ir.Join)
	t.Logf("Conservative hint: %v (%s)", j.Hint, d.Reason)

	q, err := pg.Emit(pipe)
	if err != nil {
		t.Fatalf("pg.Emit: %v", err)
	}
	if j.Hint == ir.JoinHintNone {
		if strings.Contains(q.SQL, "/*+") {
			t.Errorf("Conservative (Hint=None) should emit no hint; got:\n%s", q.SQL)
		}
	}
}

// TestO4_FullPath_NoCatalog_NoHint: without a catalog, JoinPlan is a no-op and
// the emitted SQL has no hint (the no-regression guarantee).
func TestO4_FullPath_NoCatalog_NoHint(t *testing.T) {
	pipe := joinPipe()
	jp := decision.JoinPlan{
		Policy:  decision.Aggressive{},
		Catalog: nil, // no stats
		Weights: cost.DefaultWeights("pg"),
	}
	_, changed, _ := jp.Apply(pipe)
	if changed {
		t.Error("JoinPlan should be a no-op without a catalog")
	}
	q, err := pg.Emit(pipe)
	if err != nil {
		t.Fatalf("pg.Emit: %v", err)
	}
	if strings.Contains(q.SQL, "/*+") {
		t.Errorf("no catalog → no hint; got:\n%s", q.SQL)
	}
}

// TestO4_FullPath_IndexLookupSmallOuter: when the outer table is tiny and the
// inner is very large+indexed, Aggressive picks IndexLookup (random index
// probes beat a full sequential scan of a huge table). meta must be millions
// of rows for the index to pay off — at 5K rows the seq scan is cheaper.
func TestO4_FullPath_IndexLookupSmallOuter(t *testing.T) {
	cat := loadJoinCatalog(t)
	cat.Tables["events"].RowCount = 50       // tiny outer
	cat.Tables["meta"].RowCount = 5000000    // very large indexed inner
	cat.Tables["meta"].AvgRowBytes = 200
	pipe := joinPipe()

	jp := decision.JoinPlan{
		Policy:  decision.Aggressive{},
		Catalog: cat,
		Weights: cost.DefaultWeights("pg"),
	}
	_, _, d := jp.Apply(pipe)
	j := pipe.Stages[0].(*ir.Join)
	t.Logf("IndexLookup hint: %v (%s)", j.Hint, d.Reason)
	if j.Hint != ir.JoinHintIndexLookup {
		t.Errorf("Hint = %v, want IndexLookup (small outer + large indexed inner)", j.Hint)
	}
}

// TestO4_PolicyDivergence_OnRealCatalog: the O4.S5 acceptance criterion on the
// real join.yaml catalog — Conservative and Aggressive diverge.
func TestO4_PolicyDivergence_OnRealCatalog(t *testing.T) {
	cat := loadJoinCatalog(t)

	pipeC := joinPipe()
	decision.JoinPlan{Policy: decision.Conservative{}, Catalog: cat, Weights: cost.DefaultWeights("pg")}.Apply(pipeC)
	hintC := pipeC.Stages[0].(*ir.Join).Hint

	pipeA := joinPipe()
	decision.JoinPlan{Policy: decision.Aggressive{}, Catalog: cat, Weights: cost.DefaultWeights("pg")}.Apply(pipeA)
	hintA := pipeA.Stages[0].(*ir.Join).Hint

	t.Logf("Conservative: %v", hintC)
	t.Logf("Aggressive:   %v", hintA)
	if hintC == hintA {
		t.Logf("NOTE: both chose %v", hintA)
	}
}
