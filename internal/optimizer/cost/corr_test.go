package cost

import (
	"math"
	"testing"

	"nzinfo/kql/internal/frontend/token"
	"nzinfo/kql/internal/ir"
	"nzinfo/kql/internal/optimizer/stats"
)

// twoTableCatalog: T1 (card a=10, b=5), T2 (card a=10). For join tests.
func twoTableCatalog() *stats.Catalog {
	return &stats.Catalog{
		Source: stats.SourcePgAnalyze,
		Tables: map[string]*stats.Table{
			"T1": {RowCount: 1000, Columns: map[string]*stats.ColumnStats{
				"a": {Card: 10},
				"b": {Card: 5},
			}},
			"T2": {RowCount: 500, Columns: map[string]*stats.ColumnStats{
				"a": {Card: 10},
			}},
		},
	}
}

func joinOn(leftCol, rightCol string) *ir.BinOp {
	return &ir.BinOp{Op: token.EQL, X: &ir.Col{Name: leftCol}, Y: &ir.Col{Name: rightCol}}
}

// TestJoin_SingleKey: T1.a = T2.a, card_a=10 both → 1/10.
func TestJoin_SingleKey(t *testing.T) {
	e := NewEstimator(twoTableCatalog())
	got := e.JoinSelectivity("T1", "T2", []ir.Expr{joinOn("a", "a")}, 1000, 500)
	if !approxEqual(got, 0.1) {
		t.Errorf("join sel = %v, want 0.1 (1/max(10,10))", got)
	}
}

// TestJoin_DifferentCards: T1.b (5) = T2.a (10) → 1/max(5,10) = 0.1.
func TestJoin_DifferentCards(t *testing.T) {
	e := NewEstimator(twoTableCatalog())
	got := e.JoinSelectivity("T1", "T2", []ir.Expr{joinOn("b", "a")}, 1000, 500)
	if !approxEqual(got, 0.1) {
		t.Errorf("join sel = %v, want 0.1 (1/max(5,10))", got)
	}
}

// TestJoin_MultiKeyIndependence: a=a AND b=a → 0.1 * 0.1 = 0.01 (no corr).
func TestJoin_MultiKeyIndependence(t *testing.T) {
	e := NewEstimator(twoTableCatalog())
	got := e.JoinSelectivity("T1", "T2",
		[]ir.Expr{joinOn("a", "a"), joinOn("b", "a")}, 1000, 500)
	want := 0.1 * 0.1
	if !approxEqual(got, want) {
		t.Errorf("multi-key join sel = %v, want %v (independence)", got, want)
	}
}

// TestJoin_NoConditions: cross join → 1.0.
func TestJoin_NoConditions(t *testing.T) {
	e := NewEstimator(twoTableCatalog())
	if got := e.JoinSelectivity("T1", "T2", nil, 1000, 500); got != 1.0 {
		t.Errorf("cross join sel = %v, want 1.0", got)
	}
}

// TestJoin_UnknownCards: missing stats → default 0.1 per key.
func TestJoin_UnknownCards(t *testing.T) {
	e := NewEstimator(twoTableCatalog())
	got := e.JoinSelectivity("T1", "T2", []ir.Expr{joinOn("ghost", "x")}, 1000, 500)
	if got != DefaultSelectivity {
		t.Errorf("unknown-card join sel = %v, want %v", got, DefaultSelectivity)
	}
}

// TestJoin_OutputCardinality: 1000 × 500 × 0.1 = 50000.
func TestJoin_OutputCardinality(t *testing.T) {
	e := NewEstimator(twoTableCatalog())
	got := e.OutputCardinality("T1", "T2", []ir.Expr{joinOn("a", "a")}, 1000, 500)
	if got != 50000 {
		t.Errorf("output cardinality = %d, want 50000", got)
	}
}

// --- Corr correction tests ---

// correlatedCatalog: T1.a has corr_vs {b, rho=0.9} — strongly correlated keys.
func correlatedCatalog(rho float64) *stats.Catalog {
	return &stats.Catalog{
		Source: stats.SourcePgAnalyze,
		Tables: map[string]*stats.Table{
			"T1": {RowCount: 1000, Columns: map[string]*stats.ColumnStats{
				"a": {Card: 10, CorrVs: &stats.CorrVs{OtherColumn: "b", Rho: rho}},
				"b": {Card: 10},
			}},
			"T2": {RowCount: 1000, Columns: map[string]*stats.ColumnStats{
				"a": {Card: 10},
			}},
		},
	}
}

// TestCorr_PositiveRaisesEstimate: rho=0.9 makes the multi-key join estimate
// HIGHER than the independence product (correlated keys overlap more).
func TestCorr_PositiveRaisesEstimate(t *testing.T) {
	indep := NewEstimator(correlatedCatalog(0)) // no rho
	corr := NewEstimator(correlatedCatalog(0.9))
	on := []ir.Expr{joinOn("a", "a"), joinOn("b", "a")}
	sIndep := indep.JoinSelectivity("T1", "T2", on, 1000, 1000)
	sCorr := corr.JoinSelectivity("T1", "T2", on, 1000, 1000)
	if sCorr <= sIndep {
		t.Errorf("positive corr should raise estimate: corr=%v indep=%v", sCorr, sIndep)
	}
}

// TestCorr_NegativeLowersEstimate: rho=-0.9 lowers the estimate.
func TestCorr_NegativeLowersEstimate(t *testing.T) {
	indep := NewEstimator(correlatedCatalog(0))
	corr := NewEstimator(correlatedCatalog(-0.9))
	on := []ir.Expr{joinOn("a", "a"), joinOn("b", "a")}
	sIndep := indep.JoinSelectivity("T1", "T2", on, 1000, 1000)
	sCorr := corr.JoinSelectivity("T1", "T2", on, 1000, 1000)
	if sCorr >= sIndep {
		t.Errorf("negative corr should lower estimate: corr=%v indep=%v", sCorr, sIndep)
	}
}

// TestCorr_NoEffectSingleKey: corr only applies to multi-key; single key unchanged.
func TestCorr_NoEffectSingleKey(t *testing.T) {
	corr := NewEstimator(correlatedCatalog(0.9))
	got := corr.JoinSelectivity("T1", "T2", []ir.Expr{joinOn("a", "a")}, 1000, 1000)
	if !approxEqual(got, 0.1) {
		t.Errorf("single-key with corr = %v, want 0.1 (corr irrelevant)", got)
	}
}

// TestCorr_UnrelatedColumnIgnored: corr_vs pointing at a non-key column has no effect.
func TestCorr_UnrelatedColumnIgnored(t *testing.T) {
	cat := correlatedCatalog(0.9)
	// Point a's corr at column "z" which isn't a join key.
	cat.Tables["T1"].Columns["a"].CorrVs.OtherColumn = "z"
	corr := NewEstimator(cat)
	got := corr.JoinSelectivity("T1", "T2",
		[]ir.Expr{joinOn("a", "a"), joinOn("b", "a")}, 1000, 1000)
	want := 0.1 * 0.1 // independence, no correction applies
	if !approxEqual(got, want) {
		t.Errorf("unrelated corr = %v, want %v (no correction)", got, want)
	}
}

// TestCorr_ClampsToUnit: heavy positive rho doesn't push sel above 1.
func TestCorr_ClampsToUnit(t *testing.T) {
	cat := correlatedCatalog(1.0)
	corr := NewEstimator(cat)
	got := corr.JoinSelectivity("T1", "T2",
		[]ir.Expr{joinOn("a", "a"), joinOn("b", "a")}, 1000, 1000)
	if got > 1.0 {
		t.Errorf("corr-corrected sel = %v, should clamp to ≤1.0", got)
	}
}

// TestSqrt_Helper: the local sqrt is accurate enough.
func TestSqrt_Helper(t *testing.T) {
	cases := map[float64]float64{0: 0, 1: 1, 4: 2, 9: 3, 0.25: 0.5, 100: 10}
	for in, want := range cases {
		if got := sqrt(in); math.Abs(got-want) > 1e-9 {
			t.Errorf("sqrt(%v) = %v, want %v", in, got, want)
		}
	}
}
