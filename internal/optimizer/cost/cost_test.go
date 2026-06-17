package cost

import (
	"math"
	"testing"

	"nzinfo/kql/internal/frontend/token"
	"nzinfo/kql/internal/ir"
	"nzinfo/kql/internal/optimizer/stats"
)

// catalog with a StormEvents-like State column (62 distinct, MCV known).
func testCatalog() *stats.Catalog {
	return &stats.Catalog{
		Source: stats.SourcePgAnalyze,
		Tables: map[string]*stats.Table{
			"events": {
				RowCount: 1000000,
				Columns: map[string]*stats.ColumnStats{
					"State": {
						Card: 62, Nulls: 0, Type: "string",
						MCV: &stats.MCV{
							Values:     []string{"TEXAS", "KANSAS"},
							Frequencies: []float64{0.08, 0.06},
						},
						Hist: &stats.Hist{Kind: stats.HistEquiFreq, Bounds: []string{"A", "M", "Z"}},
					},
					"Damage": {Card: 95000, Nulls: 5000, Type: "real"},
				},
			},
		},
	}
}

func eq(col string, val string) *ir.BinOp {
	return &ir.BinOp{Op: token.EQL, X: &ir.Col{Name: col}, Y: &ir.Lit{T: ir.TypeString, Value: val, HasValue: true}}
}
func lt(col string, val int64) *ir.BinOp {
	return &ir.BinOp{Op: token.LSS, X: &ir.Col{Name: col}, Y: &ir.Lit{T: ir.TypeLong, Value: val, HasValue: true}}
}
func inList(col string, vals ...string) *ir.BinOp {
	elems := make([]ir.Expr, len(vals))
	for i, v := range vals {
		elems[i] = &ir.Lit{T: ir.TypeString, Value: v, HasValue: true}
	}
	return &ir.BinOp{Op: token.IN, X: &ir.Col{Name: col}, Y: &ir.List{Elems: elems}}
}
func andOf(a, b ir.Expr) *ir.BinOp  { return &ir.BinOp{Op: token.AND, X: a, Y: b} }
func orOf(a, b ir.Expr) *ir.BinOp   { return &ir.BinOp{Op: token.OR, X: a, Y: b} }

func approxEqual(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// TestSel_EqualityMCVHit: State = "TEXAS" → 0.08 (MCV frequency).
func TestSel_EqualityMCVHit(t *testing.T) {
	e := NewEstimator(testCatalog())
	if got := e.Selectivity("events", eq("State", "TEXAS")); !approxEqual(got, 0.08) {
		t.Errorf("State=TEXAS sel = %v, want 0.08 (MCV freq)", got)
	}
}

// TestSel_EqualityNonMCV: State = "ALASKA" → 1/62.
func TestSel_EqualityNonMCV(t *testing.T) {
	e := NewEstimator(testCatalog())
	want := 1.0 / 62.0
	if got := e.Selectivity("events", eq("State", "ALASKA")); !approxEqual(got, want) {
		t.Errorf("State=ALASKA sel = %v, want %v (1/card)", got, want)
	}
}

// TestSel_EqualityNoStats: unknown table → default 0.1.
func TestSel_EqualityNoStats(t *testing.T) {
	e := NewEstimator(testCatalog())
	if got := e.Selectivity("ghost", eq("State", "X")); got != DefaultSelectivity {
		t.Errorf("unknown table sel = %v, want %v", got, DefaultSelectivity)
	}
}

// TestSel_InList: State in (TEXAS, KANSAS) → 0.08 + 0.06 = 0.14.
func TestSel_InList(t *testing.T) {
	e := NewEstimator(testCatalog())
	if got := e.Selectivity("events", inList("State", "TEXAS", "KANSAS")); !approxEqual(got, 0.14) {
		t.Errorf("in (TEXAS,KANSAS) sel = %v, want 0.14", got)
	}
}

// TestSel_InListMixedMCV: State in (TEXAS, ALASKA) → 0.08 + 1/62.
func TestSel_InListMixedMCV(t *testing.T) {
	e := NewEstimator(testCatalog())
	want := 0.08 + 1.0/62.0
	if got := e.Selectivity("events", inList("State", "TEXAS", "ALASKA")); !approxEqual(got, want) {
		t.Errorf("in (TEXAS,ALASKA) sel = %v, want %v", got, want)
	}
}

// TestSel_RangeWithHistogram: State < X with a histogram → 1/(2*bucket_count).
func TestSel_RangeWithHistogram(t *testing.T) {
	e := NewEstimator(testCatalog())
	// State has 3 bounds → 1/(2*3) = 0.1666...
	got := e.Selectivity("events", lt("State", 5))
	want := 1.0 / 6.0
	if !approxEqual(got, want) {
		t.Errorf("State<5 sel = %v, want %v (1/2*buckets)", got, want)
	}
}

// TestSel_RangeNoHistogram: Damage < X without histogram → 0.33 (pg default).
func TestSel_RangeNoHistogram(t *testing.T) {
	e := NewEstimator(testCatalog())
	if got := e.Selectivity("events", lt("Damage", 5)); !approxEqual(got, 0.33) {
		t.Errorf("Damage<5 sel = %v, want 0.33 (no hist default)", got)
	}
}

// TestSel_AND: independent assumption s1*s2.
func TestSel_AND(t *testing.T) {
	e := NewEstimator(testCatalog())
	// State=TEXAS (0.08) AND State=KANSAS (0.06) → 0.08*0.06 = 0.0048
	got := e.Selectivity("events", andOf(eq("State", "TEXAS"), eq("State", "KANSAS")))
	if !approxEqual(got, 0.08*0.06) {
		t.Errorf("AND sel = %v, want %v", got, 0.08*0.06)
	}
}

// TestSel_OR: P(a)+P(b)-P(a)P(b).
func TestSel_OR(t *testing.T) {
	e := NewEstimator(testCatalog())
	got := e.Selectivity("events", orOf(eq("State", "TEXAS"), eq("State", "KANSAS")))
	want := 0.08 + 0.06 - 0.08*0.06
	if !approxEqual(got, want) {
		t.Errorf("OR sel = %v, want %v", got, want)
	}
}

// TestSel_NilCatalog: nil catalog → all default 0.1, no panic.
func TestSel_NilCatalog(t *testing.T) {
	e := NewEstimator(nil)
	if got := e.Selectivity("events", eq("State", "X")); got != DefaultSelectivity {
		t.Errorf("nil catalog sel = %v, want %v", got, DefaultSelectivity)
	}
	if got := e.Selectivity("events", lt("State", 5)); got != DefaultSelectivity {
		t.Errorf("nil catalog range sel = %v, want %v", got, DefaultSelectivity)
	}
}

// TestSel_NilPredicate: nil predicate → 1.0 (all rows).
func TestSel_NilPredicate(t *testing.T) {
	e := NewEstimator(testCatalog())
	if got := e.Selectivity("events", nil); got != 1.0 {
		t.Errorf("nil pred sel = %v, want 1.0", got)
	}
}

// TestSel_ClampsToUnit: large IN-list selectivity caps at 1.0.
func TestSel_ClampsToUnit(t *testing.T) {
	e := NewEstimator(testCatalog())
	// Damage has card 95000; 50 values → 50/95000, small. Force a low-card col:
	cat := &stats.Catalog{Tables: map[string]*stats.Table{
		"T": {Columns: map[string]*stats.ColumnStats{
			"c": {Card: 2}, // 1/2 each → 4 values = 2.0 → clamp to 1.0
		}},
	}}
	e = NewEstimator(cat)
	got := e.Selectivity("T", inList("c", "a", "b", "c", "d"))
	if got != 1.0 {
		t.Errorf("over-1 sel = %v, want 1.0 (clamped)", got)
	}
}

// --- Cost / weights tests ---

func TestCost_Add(t *testing.T) {
	a := Cost{IO: 1, CPU: 2, Net: 3, Mem: 4}
	b := Cost{IO: 10, CPU: 20, Net: 30, Mem: 40}
	s := a.Add(b)
	if s.IO != 11 || s.CPU != 22 || s.Net != 33 || s.Mem != 44 {
		t.Errorf("Add = %+v", s)
	}
}

func TestCost_Scale(t *testing.T) {
	c := Cost{IO: 2, CPU: 3}.Scale(4)
	if c.IO != 8 || c.CPU != 12 {
		t.Errorf("Scale = %+v", c)
	}
}

func TestCost_Total(t *testing.T) {
	c := Cost{IO: 10, CPU: 100, Net: 5, Mem: 50}
	w := CostWeights{IO: 1.0, CPU: 0.01, Net: 2.0, Mem: 0.1}
	want := 10*1.0 + 100*0.01 + 5*2.0 + 50*0.1 // 10+1+10+5 = 26
	if got := c.Total(w); got != want {
		t.Errorf("Total = %v, want %v", got, want)
	}
}

func TestDefaultWeights(t *testing.T) {
	pg := DefaultWeights("pg")
	sqlite := DefaultWeights("sqlite")
	duck := DefaultWeights("duckdb")
	// pg has Net>0 (remote); sqlite has Net==0 (local); duckdb CPU-heavy
	if pg.Net <= 0 {
		t.Error("pg weights should have Net > 0")
	}
	if sqlite.Net != 0 {
		t.Error("sqlite weights should have Net == 0")
	}
	if duck.CPU < duck.IO {
		t.Error("duckdb should be CPU-heavy")
	}
}

func TestEstimateConfidence(t *testing.T) {
	// testCatalog has full stats on State → High.
	c := testCatalog()
	if EstimateConfidence(c, "events") != HighConfidence {
		t.Error("events with full stats should be HighConfidence")
	}
	if EstimateConfidence(nil, "x") != LowConfidence {
		t.Error("nil catalog should be LowConfidence")
	}
	if EstimateConfidence(c, "ghost") != LowConfidence {
		t.Error("unknown table should be LowConfidence")
	}
}
