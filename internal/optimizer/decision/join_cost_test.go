package decision

import (
	"math"
	"testing"

	"nzinfo/kql/internal/frontend/token"
	"nzinfo/kql/internal/ir"
	"nzinfo/kql/internal/optimizer/cost"
	"nzinfo/kql/internal/optimizer/stats"
)

// --- Test fixtures (local to the decision package) ---

// joinCatalog: T1 (1000 rows, a card 10, b card 5) × T2 (500 rows, a card 10).
// Mirrors cost/corr_test.go's twoTableCatalog. T2 has an index on (a) for
// IndexLookup testing.
func joinCatalog() *stats.Catalog {
	return &stats.Catalog{
		Source: stats.SourcePgAnalyze,
		Tables: map[string]*stats.Table{
			"T1": {RowCount: 1000, AvgRowBytes: 100, Columns: map[string]*stats.ColumnStats{
				"a": {Card: 10},
				"b": {Card: 5},
			}},
			"T2": {RowCount: 500, AvgRowBytes: 80, Columns: map[string]*stats.ColumnStats{
				"a": {Card: 10},
			}, Indexes: []stats.IndexDef{{Name: "t2_a", Columns: []string{"a"}}}},
		},
	}
}

// joinCatalogCorr adds a corr_vs on T1.b→T2.a so MergeJoin is feasible.
func joinCatalogCorr() *stats.Catalog {
	c := joinCatalog()
	c.Tables["T1"].Columns["b"].CorrVs = &stats.CorrVs{OtherColumn: "a", Rho: 0.8}
	return c
}

func joinOn(l, r string) *ir.BinOp {
	return &ir.BinOp{Op: token.EQL, X: &ir.Col{Name: l}, Y: &ir.Col{Name: r}}
}

func approxLE(a, b float64) bool { return a <= b+1e-9 }
func approxEqual(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// buildInput constructs a joinCostInput for T1.a = T2.a from a catalog.
func buildInput(t *testing.T, c *stats.Catalog) *joinCostInput {
	t.Helper()
	est := cost.NewEstimator(c)
	on := []ir.Expr{joinOn("a", "a")}
	leftCard := c.Tables["T1"].RowCount
	rightCard := c.Tables["T2"].RowCount
	sel := est.JoinSelectivity("T1", "T2", on, leftCard, rightCard)
	out := est.OutputCardinality("T1", "T2", on, leftCard, rightCard)
	return &joinCostInput{
		est:        est,
		leftTable:  "T1",
		rightTable: "T2",
		on:         on,
		leftCard:   leftCard,
		rightCard:  rightCard,
		leftBytes:  c.Tables["T1"].AvgRowBytes,
		rightBytes: c.Tables["T2"].AvgRowBytes,
		sel:        sel,
		outCard:    out,
		innerIndexed: true, // T2 has index on (a)
	}
}

// --- Cost combinator tests ---

// TestHashJoinCost_Structure: the hash cost has positive IO/CPU/Mem components.
func TestHashJoinCost_Structure(t *testing.T) {
	in := buildInput(t, joinCatalog())
	c := hashJoinCost(in)
	if c.IO <= 0 || c.CPU <= 0 || c.Mem <= 0 {
		t.Errorf("hashJoinCost missing components: %+v", c)
	}
	// Inner (T2, 500 rows) is the hash table → Mem should reflect it.
	// pages(500, 80) = 500*80/8192 ≈ 4.88 pages.
	if !approxEqual(c.Mem, 4.8828125) {
		t.Errorf("hash Mem = %v, want ~4.88 (500 rows × 80B / 8192)", c.Mem)
	}
	// CPU = build(inner=500 × 0.01 × 3) + probe(outer=1000 × 0.01) = 15 + 10 = 25.
	if !approxEqual(c.CPU, 25.0) {
		t.Errorf("hash CPU = %v, want 25 (build 500×0.01×3 + probe 1000×0.01)", c.CPU)
	}
}

// TestNestLoopCost_Structure: NestLoop cost is CPU-dominated and quadratic.
func TestNestLoopCost_Structure(t *testing.T) {
	in := buildInput(t, joinCatalog())
	c := nestLoopCost(in)
	// CPU = 1000 × 500 × 0.01 = 5000.
	if !approxEqual(c.CPU, 5000.0) {
		t.Errorf("nestLoop CPU = %v, want 5000 (1000×500×0.01)", c.CPU)
	}
	if c.IO <= 0 {
		t.Errorf("nestLoop IO should be positive (re-scan): %+v", c)
	}
}

// TestMergeJoinCost_NoMem: merge join uses no hash table → Mem = 0.
func TestMergeJoinCost_NoMem(t *testing.T) {
	in := buildInput(t, joinCatalog())
	c := mergeJoinCost(in)
	if c.Mem != 0 {
		t.Errorf("merge Mem = %v, want 0 (no hash table)", c.Mem)
	}
	if c.IO <= 0 || c.CPU <= 0 {
		t.Errorf("merge cost missing IO/CPU: %+v", c)
	}
}

// TestIndexLookupCost_Structure: index lookup cost = outer × random_page_cost.
func TestIndexLookupCost_Structure(t *testing.T) {
	in := buildInput(t, joinCatalog())
	c := indexLookupCost(in)
	// IO = 1000 (outer) × 4.0 (random_page_cost) = 4000.
	if !approxEqual(c.IO, 4000.0) {
		t.Errorf("indexLookup IO = %v, want 4000 (1000 × 4.0)", c.IO)
	}
}

// TestDefaultJoinCost_Structure: default cost is a neutral floor (output work).
func TestDefaultJoinCost_Structure(t *testing.T) {
	in := buildInput(t, joinCatalog())
	c := defaultJoinCost(in)
	if c.IO <= 0 || c.CPU <= 0 {
		t.Errorf("default cost missing components: %+v", c)
	}
}

// --- Cost comparison tests (the O4 acceptance criteria) ---

// TestNestLoopCheaper_BothSmall: NestLoop beats Hash when BOTH sides are tiny
// AND the hash-table build cost exceeds the cross-product comparison cost. This
// happens at very small cardinalities (a handful of rows) where the build
// multiplier (3×) on the inner side makes Hash's startup cost exceed NestLoop's
// full cross-product scan. This is the O4.S2 acceptance criterion.
//
// At 5×5: NestLoop CPU = 25×0.01 = 0.25; Hash CPU = build(5×0.01×3=0.15) +
// probe(5×0.01=0.05) = 0.20. Here NestLoop ≈ Hash on CPU, but Hash adds the Mem
// component (the hash table), tipping it above NestLoop.
func TestNestLoopCheaper_BothSmall(t *testing.T) {
	c := &stats.Catalog{
		Source: stats.SourcePgAnalyze,
		Tables: map[string]*stats.Table{
			"T1": {RowCount: 5, AvgRowBytes: 50, Columns: map[string]*stats.ColumnStats{"a": {Card: 5}}},
			"T2": {RowCount: 5, AvgRowBytes: 50, Columns: map[string]*stats.ColumnStats{"a": {Card: 5}}},
		},
	}
	in := buildInputTiny(c)
	w := cost.DefaultWeights("pg")
	nl := nestLoopCost(in).Total(w)
	hj := hashJoinCost(in).Total(w)
	if nl >= hj {
		t.Errorf("NestLoop (%.4f) should be cheaper than HashJoin (%.4f) for tiny tables", nl, hj)
	}
}

// TestHashCheaper_LargeInner: when the inner side is large, Hash beats NestLoop
// (NestLoop's quadratic CPU dominates). The standard hash-join win.
func TestHashCheaper_LargeInner(t *testing.T) {
	c := &stats.Catalog{
		Source: stats.SourcePgAnalyze,
		Tables: map[string]*stats.Table{
			"T1": {RowCount: 100000, AvgRowBytes: 100, Columns: map[string]*stats.ColumnStats{"a": {Card: 1000}}},
			"T2": {RowCount: 50000, AvgRowBytes: 100, Columns: map[string]*stats.ColumnStats{"a": {Card: 1000}}},
		},
	}
	in := buildInputTiny(c)
	w := cost.DefaultWeights("pg")
	nl := nestLoopCost(in).Total(w)
	hj := hashJoinCost(in).Total(w)
	if hj >= nl {
		t.Errorf("HashJoin (%.2f) should be cheaper than NestLoop (%.2f) on large tables", hj, nl)
	}
}

// TestIndexLookupCheaper_SmallOuter: when outer is small + inner indexed,
// IndexLookup beats both Hash and NestLoop. The O4.S3 acceptance criterion.
func TestIndexLookupCheaper_SmallOuter(t *testing.T) {
	c := &stats.Catalog{
		Source: stats.SourcePgAnalyze,
		Tables: map[string]*stats.Table{
			"T1": {RowCount: 50, AvgRowBytes: 50, Columns: map[string]*stats.ColumnStats{"a": {Card: 50}}},
			"T2": {RowCount: 1000000, AvgRowBytes: 200, Columns: map[string]*stats.ColumnStats{"a": {Card: 1000000}},
				Indexes: []stats.IndexDef{{Name: "t2_pk", Columns: []string{"a"}, Unique: true}}},
		},
	}
	in := buildInputTiny(c)
	in.innerIndexed = true
	w := cost.DefaultWeights("pg")
	il := indexLookupCost(in).Total(w)
	hj := hashJoinCost(in).Total(w)
	nl := nestLoopCost(in).Total(w)
	if il >= hj {
		t.Errorf("IndexLookup (%.2f) should beat HashJoin (%.2f) for small outer + indexed large inner", il, hj)
	}
	if il >= nl {
		t.Errorf("IndexLookup (%.2f) should beat NestLoop (%.2f) for small outer", il, nl)
	}
}

// TestDescribeJoin: the Explain summary includes method + selectivity + cards.
func TestDescribeJoin(t *testing.T) {
	in := buildInput(t, joinCatalog())
	s := describeJoin("HashJoin", in)
	if s == "" {
		t.Error("describeJoin returned empty string")
	}
	// Should mention the method and the cardinalities.
	for _, want := range []string{"HashJoin", "L=1000", "R=500"} {
		if !contains(s, want) {
			t.Errorf("describeJoin %q missing %q", s, want)
		}
	}
}

// contains is defined in policy_test.go (shared across this package's _test.go
// files — Go unifies the package's test files into one scope).

// buildInputTiny is like buildInput but doesn't assume specific table names,
// for catalogs where the tables are still T1/T2 but with different sizes.
func buildInputTiny(c *stats.Catalog) *joinCostInput {
	est := cost.NewEstimator(c)
	on := []ir.Expr{joinOn("a", "a")}
	leftCard := c.Tables["T1"].RowCount
	rightCard := c.Tables["T2"].RowCount
	sel := est.JoinSelectivity("T1", "T2", on, leftCard, rightCard)
	out := est.OutputCardinality("T1", "T2", on, leftCard, rightCard)
	return &joinCostInput{
		est:          est,
		leftTable:    "T1",
		rightTable:   "T2",
		on:           on,
		leftCard:     leftCard,
		rightCard:    rightCard,
		leftBytes:    c.Tables["T1"].AvgRowBytes,
		rightBytes:   c.Tables["T2"].AvgRowBytes,
		sel:          sel,
		outCard:      out,
		innerIndexed: false,
	}
}

// keep helpers used
var _ = approxLE
