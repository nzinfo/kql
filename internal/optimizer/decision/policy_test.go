package decision

import (
	"testing"

	"nzinfo/kql/internal/frontend/token"
	"nzinfo/kql/internal/ir"
	"nzinfo/kql/internal/optimizer/stats"
)

// fakeEstimator returns a fixed selectivity per column name, for deterministic
// policy tests.
type fakeEstimator struct{ per map[string]float64 }

func (f fakeEstimator) Selectivity(table string, pred ir.Expr) float64 {
	if b, ok := pred.(*ir.BinOp); ok && b.X != nil {
		if c, ok := b.X.(*ir.Col); ok {
			if s, ok := f.per[c.Name]; ok {
				return s
			}
		}
	}
	return 0.1
}

// A pipeline with one Filter: `where b = 1 AND a = 1` — b is loose (0.5)
// listed FIRST, a is very selective (0.01) second. The "good" order is [a, b],
// so a reorder is needed (this is the suboptimal-source-order case the rule fixes).
func orderTestPipeline() *ir.Pipeline {
	a := &ir.BinOp{Op: token.EQL, X: &ir.Col{Name: "a"}, Y: &ir.Lit{Value: int64(1), HasValue: true}}
	b := &ir.BinOp{Op: token.EQL, X: &ir.Col{Name: "b"}, Y: &ir.Lit{Value: int64(1), HasValue: true}}
	return &ir.Pipeline{
		Source: &ir.SourceTable{Table: "T"},
		Stages: []ir.Stage{
			// b first (loose), a second (selective) — suboptimal order
			&ir.Filter{Predicate: &ir.BinOp{Op: token.AND, X: b, Y: a}},
		},
	}
}

// estRealStats: a=0.01, b=0.5 (both real, distinct from default 0.1).
func estRealStats() fakeEstimator {
	return fakeEstimator{per: map[string]float64{"a": 0.01, "b": 0.5}}
}

// estAllDefault: every column → 0.1 (no real stats).
func estAllDefault() fakeEstimator {
	return fakeEstimator{per: map[string]float64{}}
}

// firstConjunctColumn returns the column name of the first conjunct of an AND
// (or the single predicate if not AND), to assert ordering.
func firstConjunctColumn(e ir.Expr) string {
	if b, ok := e.(*ir.BinOp); ok && b.Op == token.AND {
		return firstConjunctColumn(b.X)
	}
	if b, ok := e.(*ir.BinOp); ok && b.X != nil {
		if c, ok := b.X.(*ir.Col); ok {
			return c.Name
		}
	}
	return ""
}

// TestPolicy_ConservativeReordersWithStats: real stats → Conservative reorders
// so the selective predicate (a) is first.
func TestPolicy_ConservativeReordersWithStats(t *testing.T) {
	pipe := orderTestPipeline()
	po := PredicateOrder{Policy: Conservative{}, Estimator: estRealStats(), Table: "T"}
	_, changed, d := po.Apply(pipe)
	if !changed {
		t.Fatal("expected reorder with real stats")
	}
	if got := firstConjunctColumn(pipe.Stages[0].(*ir.Filter).Predicate); got != "a" {
		t.Errorf("first conjunct = %q, want 'a' (most selective); decision: %+v", got, d)
	}
	if d.PolicyName != "Conservative" {
		t.Errorf("policy name = %q", d.PolicyName)
	}
}

// TestPolicy_ConservativeKeepsOrderWithWeakStats: all-default selectivities →
// Conservative keeps source order (a was first, stays first).
func TestPolicy_ConservativeKeepsOrderWithWeakStats(t *testing.T) {
	pipe := orderTestPipeline()
	po := PredicateOrder{Policy: Conservative{}, Estimator: estAllDefault(), Table: "T"}
	_, changed, d := po.Apply(pipe)
	if changed {
		t.Error("Conservative should NOT reorder with weak stats")
	}
	if !contains(d.Reason, "source order") {
		t.Errorf("reason should mention source order: %q", d.Reason)
	}
}

// TestPolicy_AggressiveAlwaysReorders: even with weak stats, Aggressive reorders
// (here it treats both as uniform 0.1 → keeps order, but the POINT is it ran).
// Use real stats to show it picks a-first like Conservative would.
func TestPolicy_AggressiveReordersWithStats(t *testing.T) {
	pipe := orderTestPipeline()
	po := PredicateOrder{Policy: Aggressive{}, Estimator: estRealStats(), Table: "T"}
	_, changed, d := po.Apply(pipe)
	if !changed {
		t.Fatal("Aggressive should reorder")
	}
	if got := firstConjunctColumn(pipe.Stages[0].(*ir.Filter).Predicate); got != "a" {
		t.Errorf("first conjunct = %q, want 'a'", got)
	}
	if d.PolicyName != "Aggressive" {
		t.Errorf("policy = %q", d.PolicyName)
	}
}

// TestPolicy_AggressiveReordersEvenWithWeakStats: with all-default stats,
// Aggressive still produces a decision (and a Reason noting no real stats),
// whereas Conservative would keep source order. Both reorders here give the
// same order (uniform), but Aggressive's reason differs.
func TestPolicy_AggressiveReasonWithWeakStats(t *testing.T) {
	pipe := orderTestPipeline()
	po := PredicateOrder{Policy: Aggressive{}, Estimator: estAllDefault(), Table: "T"}
	_, _, d := po.Apply(pipe)
	if !contains(d.Reason, "no real stats") {
		t.Errorf("Aggressive weak-stats reason should note 'no real stats': %q", d.Reason)
	}
}

// TestPolicy_ConfidenceGated_HighConf: with a confident catalog, Gated behaves
// like Aggressive (reorders to put selective a first).
func TestPolicy_ConfidenceGated_HighConf(t *testing.T) {
	cat := &stats.Catalog{
		Source: stats.SourcePgAnalyze,
		Tables: map[string]*stats.Table{
			"T": {Columns: map[string]*stats.ColumnStats{
				"a": {Card: 100, Nulls: 0, MCV: &stats.MCV{}, Hist: &stats.Hist{}},
				"b": {Card: 2, Nulls: 0, MCV: &stats.MCV{}, Hist: &stats.Hist{}},
			}},
		},
	}
	pipe := orderTestPipeline()
	po := PredicateOrder{Policy: ConfidenceGated{Catalog: cat}, Estimator: estRealStats(), Table: "T"}
	_, changed, d := po.Apply(pipe)
	if !changed {
		t.Fatal("ConfidenceGated (high conf) should reorder like Aggressive")
	}
	if got := firstConjunctColumn(pipe.Stages[0].(*ir.Filter).Predicate); got != "a" {
		t.Errorf("first conjunct = %q, want 'a'", got)
	}
	if !contains(d.Reason, "high confidence") {
		t.Errorf("reason should mention high confidence: %q", d.Reason)
	}
}

// TestPolicy_ConfidenceGated_LowConf: with a nil catalog, Gated falls back to
// Conservative behavior (keeps source order with weak stats).
func TestPolicy_ConfidenceGated_LowConf(t *testing.T) {
	pipe := orderTestPipeline()
	po := PredicateOrder{Policy: ConfidenceGated{Catalog: nil}, Estimator: estAllDefault(), Table: "T"}
	_, changed, d := po.Apply(pipe)
	if changed {
		t.Error("ConfidenceGated (low conf, weak stats) should keep source order")
	}
	if !contains(d.Reason, "low confidence") {
		t.Errorf("reason should mention low confidence: %q", d.Reason)
	}
}

// TestPolicy_ThreePoliciesDiffer: the key O3.S5 acceptance — three strategies
// on the same weak-stats IR produce different Reasons (Conservative keeps,
// Aggressive notes uniform, Gated defers to Conservative).
func TestPolicy_ThreePoliciesDiffer(t *testing.T) {
	est := estAllDefault()
	_, _, dc := PredicateOrder{Policy: Conservative{}, Estimator: est, Table: "T"}.Apply(orderTestPipeline())
	_, _, da := PredicateOrder{Policy: Aggressive{}, Estimator: est, Table: "T"}.Apply(orderTestPipeline())
	_, _, dg := PredicateOrder{Policy: ConfidenceGated{Catalog: nil}, Estimator: est, Table: "T"}.Apply(orderTestPipeline())
	if dc.Reason == da.Reason {
		t.Errorf("Conservative and Aggressive reasons identical on weak stats: %q", dc.Reason)
	}
	// Gated (low conf) delegates to Conservative → same reason shape, but
	// prefixed with "low confidence →".
	if !contains(dg.Reason, "low confidence") {
		t.Errorf("Gated reason should reflect low-confidence delegation: %q", dg.Reason)
	}
}

// TestPredicateOrder_NoOpOnSinglePredicate: a non-AND filter is left alone.
func TestPredicateOrder_NoOpOnSinglePredicate(t *testing.T) {
	pipe := &ir.Pipeline{
		Source: &ir.SourceTable{Table: "T"},
		Stages: []ir.Stage{
			&ir.Filter{Predicate: &ir.BinOp{Op: token.EQL, X: &ir.Col{Name: "a"}, Y: &ir.Lit{Value: int64(1), HasValue: true}}},
		},
	}
	_, changed, _ := PredicateOrder{Policy: Aggressive{}, Estimator: estRealStats(), Table: "T"}.Apply(pipe)
	if changed {
		t.Error("single-predicate filter should not be reordered")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
