// Package decision — join cost combinators (O4.S1–S4).
//
// These pure functions build a cost.Cost for each physical join method, using
// the existing cost.Estimator (selectivity + output cardinality) and the
// catalog's table sizes. They are the per-plan costing formulas from the O4
// phase doc:
//
//   - HashJoin:    build hash table on the smaller side, probe the larger.
//                  IO = seq scan both; CPU = build+probe rows; Mem = inner size.
//   - NestLoop:    outer × inner × cpu_tuple_cost (CPU-dominated).
//   - MergeJoin:   one sorted pass each when both sides are key-sorted; Mem≈0.
//                  (feasibility proxied by corr_vs presence — see join_plan.go)
//   - IndexLookup: outer × random_page_cost (random IO per outer row).
//                  (only feasible when inner has an index on a join key)
//   - Default:     baseline — let the backend planner decide. Cost is the
//                  estimated output work (a neutral floor) so Conservative can
//                  pick it when no candidate is clearly dominant.
//
// Constants reflect PostgreSQL defaults (seq_page_cost=1.0, random_page_cost=
// 4.0, cpu_tuple_cost=0.01); the catalog's CostModel overrides when present.
package decision

import (
	"fmt"

	"nzinfo/kql/internal/ir"
	"nzinfo/kql/internal/optimizer/cost"
	"nzinfo/kql/internal/optimizer/stats"
)

// pageSize is PostgreSQL's default block size (8KB). Used to convert row counts
// + avg row bytes into page counts for IO cost.
const pageSize = 8192

// defaultSeqPageCost / defaultRandPageCost / defaultCPUTupleCost mirror
// PostgreSQL's stock planner defaults; the catalog's CostModel overrides these.
const (
	defaultSeqPageCost  = 1.0
	defaultRandPageCost = 4.0
	defaultCPUTupleCost = 0.01
)

// joinCostInput carries the shared context the per-method cost functions need.
// Built once per join node by join_plan.go, then passed to each combinator so
// they share the same cardinality/selectivity estimates.
type joinCostInput struct {
	est          *cost.Estimator
	cm           *stats.CostModel // nil → use defaults
	leftTable    string
	rightTable   string
	on           []ir.Expr
	leftCard     int64
	rightCard    int64
	leftBytes    int // avg row bytes (0 if unknown)
	rightBytes   int
	sel          float64 // join selectivity (fraction of cross-product)
	outCard      int64  // output cardinality = leftCard × rightCard × sel
	hasCorr      bool   // corr_vs present on a join key (MergeJoin feasibility)
	innerIndexed bool   // inner side has an index on a join key (IndexLookup feasibility)
}

// costModel resolves a cost constant: catalog value if non-zero, else default.
func (in *joinCostInput) seqPageCost() float64 {
	if in.cm != nil && in.cm.SeqPageCost > 0 {
		return in.cm.SeqPageCost
	}
	return defaultSeqPageCost
}
func (in *joinCostInput) randPageCost() float64 {
	if in.cm != nil && in.cm.RandomPageCost > 0 {
		return in.cm.RandomPageCost
	}
	return defaultRandPageCost
}
func (in *joinCostInput) cpuTupleCost() float64 {
	if in.cm != nil && in.cm.CPUTupleCost > 0 {
		return in.cm.CPUTupleCost
	}
	return defaultCPUTupleCost
}

// pages converts a row count + avg row bytes into an 8KB page count.
func pages(rows int64, avgBytes int) float64 {
	if avgBytes <= 0 {
		avgBytes = 64 // assume a modest row when unknown
	}
	return float64(rows) * float64(avgBytes) / float64(pageSize)
}

// hashJoinCost: build a hash table on the smaller (inner) side, probe with the
// larger (outer) side. Mem tracks the hash-table footprint — high when the
// inner side doesn't fit work_mem (the O4.S1 acceptance criterion).
//
// The CPU cost has TWO parts: a BUILD cost (hashing each inner row, proportional
// to inner rows — this is the startup overhead that makes Hash lose to NestLoop
// on tiny tables) and a PROBE cost (hashing each outer row). The build cost
// uses a higher multiplier than probe because hash insertion is more work than
// a simple tuple comparison.
func hashJoinCost(in *joinCostInput) cost.Cost {
	inner, outer := in.rightCard, in.leftCard
	innerBytes, outerBytes := in.rightBytes, in.leftBytes
	// Put the smaller side inner (hash table) — standard hash-join optimisation.
	if inner > outer && outer > 0 {
		inner, outer = outer, inner
		innerBytes, outerBytes = outerBytes, innerBytes
	}
	innerPages := pages(inner, innerBytes)
	outerPages := pages(outer, outerBytes)
	// Build = inner rows × cpuTupleCost × buildMultiplier (hash insert ≈ 2-3× a
	// compare); Probe = outer rows × cpuTupleCost. The buildMultiplier is what
	// makes Hash's startup cost exceed NestLoop's per-tuple cost on tiny tables.
	const buildMultiplier = 3.0
	buildCPU := float64(inner) * in.cpuTupleCost() * buildMultiplier
	probeCPU := float64(outer) * in.cpuTupleCost()
	return cost.Cost{
		IO:  (innerPages + outerPages) * in.seqPageCost(),
		CPU: buildCPU + probeCPU,
		// Mem is the hash table: inner row count × row bytes (in pages, as a
		// proxy — the weight turns it into a comparable scalar).
		Mem: innerPages,
	}
}

// nestLoopCost: for each outer row, scan the inner side. CPU-dominated; only
// wins when both sides are small (the O4.S2 acceptance criterion).
//
// Key insight: when the inner side fits in cache (small), repeated scans are
// effectively free (the pages stay resident). So IO is NOT outer×inner_pages
// — it's a single scan of each side, plus a small cache-miss factor proportional
// to how far the inner exceeds a nominal cache window. This is what makes
// NestLoop competitive for small×small joins: the CPU work is the real cost,
// and IO is nearly free when cached.
func nestLoopCost(in *joinCostInput) cost.Cost {
	outer, inner := in.leftCard, in.rightCard
	if outer <= 0 {
		outer = 1
	}
	if inner <= 0 {
		inner = 1
	}
	innerPages := pages(inner, in.rightBytes)
	outerPages := pages(outer, in.leftBytes)
	// Cache-residency heuristic: if the inner side fits in ~1000 pages (≈8MB,
	// a conservative shared_buffers slice), the repeated scans hit cache and
	// IO is just the initial load of both sides. Beyond that, each outer
	// iteration re-incurs a fraction of the inner scan.
	const cachePages = 1000
	io := (innerPages + outerPages) * in.seqPageCost()
	if innerPages > cachePages {
		// Inner spills cache: add (innerPages - cachePages) × outer × seq cost
		// as the re-scan penalty (the fraction not cached).
		io += (innerPages - cachePages) * float64(outer) * in.seqPageCost()
	}
	return cost.Cost{
		CPU: float64(outer) * float64(inner) * in.cpuTupleCost(),
		IO:  io,
	}
}

// mergeJoinCost: one sorted pass over each side when both are key-sorted. Cheaper
// than hash when a sort isn't needed (Mem≈0, single pass). We can't detect sort
// order from IR yet, so this is costed optimistically (no sort penalty) and the
// planner gates it on corr_vs as a proxy for "naturally ordered key" (O4.S4).
func mergeJoinCost(in *joinCostInput) cost.Cost {
	leftPages := pages(in.leftCard, in.leftBytes)
	rightPages := pages(in.rightCard, in.rightBytes)
	return cost.Cost{
		IO:  (leftPages + rightPages) * in.seqPageCost(),
		CPU: float64(in.leftCard+in.rightCard) * in.cpuTupleCost(),
		// Mem=0 — merge join uses no hash table (its advantage over hash).
	}
}

// indexLookupCost: for each outer row, do an index probe into the inner side
// (random IO). Wins when the outer side is small and the inner has a usable
// index (O4.S3). The structural IN-list rewrite (WHERE id = ANY(...)) is a
// deferred emit path; this cost lets the planner know when it would be best.
func indexLookupCost(in *joinCostInput) cost.Cost {
	outer := in.leftCard
	if outer <= 0 {
		outer = 1
	}
	return cost.Cost{
		// Each outer row → one random-page index probe (random_page_cost).
		IO:  float64(outer) * in.randPageCost(),
		CPU: float64(outer) * in.cpuTupleCost(),
	}
}

// defaultJoinCost: the "let the backend planner decide" baseline. Costed as the
// estimated output work (a neutral floor) so Conservative can pick it when no
// candidate is clearly dominant. It is NOT a specific method — it defers the
// choice to pg/sqlite/duckdb's own planner.
func defaultJoinCost(in *joinCostInput) cost.Cost {
	out := in.outCard
	if out <= 0 {
		out = in.leftCard + in.rightCard
	}
	return cost.Cost{
		IO:  (pages(in.leftCard, in.leftBytes) + pages(in.rightCard, in.rightBytes)) * in.seqPageCost(),
		CPU: float64(out) * in.cpuTupleCost(),
	}
}

// describeJoin builds a one-line summary for Explain, shared by all plans.
func describeJoin(method string, in *joinCostInput) string {
	return fmt.Sprintf("%s sel=%.4f out=%d L=%d(R%dB) R=%d(R%dB)",
		method, in.sel, in.outCard, in.leftCard, in.leftBytes, in.rightCard, in.rightBytes)
}
