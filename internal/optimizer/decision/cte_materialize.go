// Package decision — cost-based CTE materialization (O6).
//
// PostgreSQL 14+ lets each WITH-clause CTE be annotated MATERIALIZED (force
// compute+cache) or NOT MATERIALIZED (allow the planner to inline/flatten the
// CTE into the consuming query). The static decision in pg/emit_cte.go
// (cteMaterialization) uses stage TYPE: Aggregate/Join → MATERIALIZED,
// Filter/Sort/Limit → NOT MATERIALIZED.
//
// This module provides a COST-BASED refinement. Given a pipeline segment and a
// stats catalog, CTEMaterializeDecision estimates the segment's output
// cardinality × row bytes (the materialization footprint) and recommends:
//
//   - MATERIALIZED when the segment output is LARGE (re-computing it on each
//     reference is expensive; caching pays off). Threshold: footprint bytes
//     exceeds the work_mem budget proxy (default 4MB).
//   - NOT MATERIALIZED when SMALL (inlining avoids CTE-scan overhead and lets
//     the planner push predicates down).
//   - the static default when stats are unavailable (no catalog, or table
//     cardinalities unknown) — the safe no-regression path.
//
// The decision is a pure function of (segment stages, source table, catalog);
// the pg emitter consults it via ShouldMaterialize.
package decision

import (
	"nzinfo/kql/internal/ir"
	"nzinfo/kql/internal/optimizer/cost"
	"nzinfo/kql/internal/optimizer/stats"
)

// materializeBytesThreshold is the materialization footprint above which
// MATERIALIZED is recommended. Proxies PostgreSQL's default work_mem (4MB): a
// CTE whose result fits comfortably in work_mem is cheap to recompute (inline),
// while one exceeding it forces a spill if recomputed → materialize.
const materializeBytesThreshold = 4 * 1024 * 1024 // 4MB

// MaterializeHint is the cost-based CTE materialization recommendation.
type MaterializeHint int

const (
	// MatDefault leaves the decision to the static stage-type heuristic.
	MatDefault MaterializeHint = iota
	// MatForceMaterialize recommends MATERIALIZED (large output, caching pays).
	MatForceMaterialize
	// MatForceInline recommends NOT MATERIALIZED (small output, inline is cheaper).
	MatForceInline
)

// ShouldMaterialize returns the cost-based materialization hint for a CTE
// segment. It walks the segment's stages, estimates the output cardinality
// (starting from the source table's row count, applying filter selectivity and
// aggregate reduction), and compares the footprint to the threshold.
//
// Returns MatDefault when the catalog is nil, the source table is unknown, or
// estimation is impossible — the caller then falls back to the static rule.
func ShouldMaterialize(catalog *stats.Catalog, sourceTable string, seg []ir.Stage) MaterializeHint {
	if catalog == nil || sourceTable == "" {
		return MatDefault
	}
	card := sourceCardinality(catalog, sourceTable)
	if card == 0 {
		return MatDefault // unknown → static default
	}
	bytes := tableRowBytes(catalog, sourceTable)
	out, _ := estimateSegmentOutput(catalog, sourceTable, card, bytes, seg)
	if out < 0 {
		return MatDefault // estimation failed → static default
	}
	if out > materializeBytesThreshold {
		return MatForceMaterialize
	}
	return MatForceInline
}

// sourceCardinality returns the row count for the pipeline's base table.
func sourceCardinality(c *stats.Catalog, table string) int64 {
	if c == nil || table == "" {
		return 0
	}
	if t, ok := c.Tables[lower(table)]; ok && t != nil {
		return t.RowCount
	}
	if t, ok := c.Tables[table]; ok && t != nil {
		return t.RowCount
	}
	return 0
}

// tableRowBytes returns the average row bytes for the base table (0 if unknown).
func tableRowBytes(c *stats.Catalog, table string) int {
	if t, ok := c.Tables[lower(table)]; ok && t != nil {
		return t.AvgRowBytes
	}
	if t, ok := c.Tables[table]; ok && t != nil {
		return t.AvgRowBytes
	}
	return 0
}

// estimateSegmentOutput walks the segment's stages, updating cardinality and
// returns the estimated output footprint in bytes (card × bytes). Returns -1 if
// a stage's effect can't be estimated.
//
// Heuristics (intentionally conservative — relative comparison, not absolute):
//   - Filter: apply the estimator's predicate selectivity (approximated as a
//     fixed 0.1 factor when the predicate references unknown columns).
//   - Aggregate: output = distinct count of the GROUP BY keys (bounded by input).
//   - Join: output = left × right × join selectivity (uses the estimator).
//   - Sort/Limit/Distinct: cardinality unchanged (or capped by Limit N).
//   - Other stages: assume cardinality unchanged (conservative).
func estimateSegmentOutput(catalog *stats.Catalog, sourceTable string, card int64, bytes int, seg []ir.Stage) (int64, error) {
	est := cost.NewEstimator(catalog)
	cur := card
	for _, st := range seg {
		switch s := st.(type) {
		case *ir.Filter:
			// Approximate filter selectivity: 0.1 when we can't estimate precisely.
			// (A full per-predicate estimate would need the column stats wired here;
			// the 0.1 factor is a conservative midpoint.)
			cur = int64(float64(cur) * 0.1)
			if cur < 1 {
				cur = 1
			}
		case *ir.Aggregate:
			// Output rows ≈ distinct GROUP BY keys. Without per-key stats, bound by
			// min(input, sqrt(input)) — aggregates typically reduce cardinality
			// significantly. Use sqrt as a conservative reduction factor.
			if cur > 100 {
				cur = isqrt(cur)
			}
		case *ir.Limit:
			// Limit.Count is an Expr (typically a literal). If it's a literal int,
			// cap cardinality; otherwise leave unchanged.
			if n, ok := litInt(s.Count); ok && n < cur {
				cur = n
			}
		case *ir.Join:
			rt := joinRightTableName(s)
			if rt == "" {
				return -1, nil
			}
			rc := sourceCardinality(catalog, rt)
			if rc == 0 {
				return -1, nil
			}
			out := est.OutputCardinality(sourceTable, rt, s.On, cur, rc)
			cur = out
		case *ir.Sort, *ir.Distinct, *ir.Project, *ir.Extend:
			// cardinality unchanged
		default:
			// unknown stage: assume unchanged (conservative — don't underestimate)
		}
	}
	if bytes == 0 {
		bytes = 100 // default row width when unknown
	}
	return cur * int64(bytes), nil
}

// litInt extracts an integer value from a literal Expr (ir.Lit holding an int),
// or returns (0, false) if not a literal int.
func litInt(e ir.Expr) (int64, bool) {
	if l, ok := e.(*ir.Lit); ok {
		if n, ok := l.Value.(int); ok {
			return int64(n), true
		}
		if n, ok := l.Value.(int64); ok {
			return n, true
		}
	}
	return 0, false
}

// isqrt returns the integer square root (floor).
func isqrt(n int64) int64 {
	if n <= 0 {
		return 0
	}
	x := n
	y := (x + 1) / 2
	for y < x {
		x = y
		y = (x + n/x) / 2
	}
	return x
}

// lower is a local ASCII lowercase (avoids importing strings just for this).
func lower(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 32
		}
		b[i] = c
	}
	return string(b)
}
