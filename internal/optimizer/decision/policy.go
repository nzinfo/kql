// Package decision is the cost-based optimizer's decision-policy layer
// (docs/phases/optimizer/O3-decision-policy.md).
//
// Where O2 (rules) does dialect-agnostic IR→IR rewrites that are ALWAYS safe,
// O3 makes COST-BASED choices between alternatives where there's a real
// trade-off — e.g. which of two commutative predicates to evaluate first
// (more selective first), or join order (build the smaller side). These
// decisions read the O0 stats catalog + O1 selectivity/cost via a
// cost.Estimator and a DecisionPolicy strategy object.
//
// The policy is SWAPPABLE (DESIGN §6.4 / O3.S3): Conservative (default — falls
// back to "most pg-like" when stats are weak), Aggressive (always lowest
// estimated cost), ConfidenceGated (aggressive when confidence is high,
// conservative otherwise). The CLI's --policy flag and `kql explain` switch
// between them (O3.S6).
//
// Every decision carries a human-readable Reason (which stat, what
// selectivity, why this choice) so `kql explain` can show the rationale.
package decision

import (
	"nzinfo/kql/internal/optimizer/cost"
	"nzinfo/kql/internal/optimizer/stats"
)

// Decision is one cost-based choice: the chosen option index + why.
type Decision struct {
	// PolicyName is the strategy that made this choice ("Conservative" etc.).
	PolicyName string
	// Choice is a short label for what was decided ("PredicateOrder" etc.).
	Choice string
	// Reason explains the rationale (which stat, what selectivity).
	Reason string
}

// DecisionPolicy picks among alternatives using the cost inputs. Implementations
// must be safe to call with a nil catalog (Conservative falls back to a sane
// default; Aggressive treats missing stats as uniform).
type DecisionPolicy interface {
	// Name returns the strategy name for Explain logging.
	Name() string
	// OrderPredicates decides the evaluation order of a set of predicates on a
	// table. Returns the indices into `selectivities` in the chosen order, plus
	// a Decision explaining why. The first predicate evaluated should filter
	// the most rows (lowest selectivity), so the rest do less work.
	OrderPredicates(table string, selectivities []float64) (order []int, d Decision)
}

// --- Strategy implementations ---

// Conservative is the default policy. When stats are weak (any selectivity is
// the DefaultSelectivity placeholder, meaning "no real stat"), it preserves
// source order (don't reorder what we can't estimate well). This matches the
// O3.S4 spec: "key stats missing → no aggressive reorder, fall back to most
// pg-like (source order)".
type Conservative struct{}

// Name returns "Conservative".
func (Conservative) Name() string { return "Conservative" }

// OrderPredicates: if all selectivities are real (not all default), order most-
// selective-first; otherwise keep source order (don't guess).
func (Conservative) OrderPredicates(table string, sels []float64) ([]int, Decision) {
	if !anyRealStat(sels) {
		// Can't estimate well → keep source order (the conservative choice).
		return identityOrder(len(sels)), Decision{
			PolicyName: "Conservative",
			Choice:     "PredicateOrder",
			Reason:     "stats weak (all default) — keeping source order to avoid a bad guess",
		}
	}
	order := sortBySelectivity(sels)
	return order, Decision{
		PolicyName: "Conservative",
		Choice:     "PredicateOrder",
		Reason:     describeOrder("selectivity (most-selective first)", sels, order),
	}
}

// Aggressive always orders most-selective-first regardless of confidence,
// treating missing stats as uniform (DefaultSelectivity). O3.S5.
type Aggressive struct{}

// Name returns "Aggressive".
func (Aggressive) Name() string { return "Aggressive" }

// OrderPredicates: always sort by selectivity, even with weak stats.
func (Aggressive) OrderPredicates(table string, sels []float64) ([]int, Decision) {
	order := sortBySelectivity(sels)
	reason := describeOrder("selectivity (aggressive, even with weak stats)", sels, order)
	if !anyRealStat(sels) {
		reason = "no real stats — treating predicates as uniform; " + reason
	}
	return order, Decision{PolicyName: "Aggressive", Choice: "PredicateOrder", Reason: reason}
}

// ConfidenceGated is Aggressive when the catalog confidence for the table is
// high (≥0.5), Conservative otherwise. O3.S5. It needs the catalog to evaluate
// confidence, so it carries one.
type ConfidenceGated struct {
	Catalog *stats.Catalog
}

// Name returns "ConfidenceGated".
func (g ConfidenceGated) Name() string { return "ConfidenceGated" }

// OrderPredicates: delegate to Aggressive or Conservative based on confidence.
func (g ConfidenceGated) OrderPredicates(table string, sels []float64) ([]int, Decision) {
	conf := cost.EstimateConfidence(g.Catalog, table)
	if conf == cost.HighConfidence {
		order, d := Aggressive{}.OrderPredicates(table, sels)
		d.PolicyName = "ConfidenceGated"
		d.Reason = "high confidence → " + d.Reason
		return order, d
	}
	order, d := Conservative{}.OrderPredicates(table, sels)
	d.PolicyName = "ConfidenceGated"
	d.Reason = "low confidence → " + d.Reason
	return order, d
}

// --- helpers ---

// sortBySelectivity returns indices ordered by ascending selectivity (most
// selective / smallest value first).
func sortBySelectivity(sels []float64) []int {
	idx := make([]int, len(sels))
	for i := range idx {
		idx[i] = i
	}
	// insertion sort (small N — predicates per stage are few)
	for i := 1; i < len(idx); i++ {
		for j := i; j > 0 && sels[idx[j]] < sels[idx[j-1]]; j-- {
			idx[j], idx[j-1] = idx[j-1], idx[j]
		}
	}
	return idx
}

// anyRealStat reports whether at least one selectivity differs from the
// DefaultSelectivity placeholder (i.e. we have a real estimate for something).
func anyRealStat(sels []float64) bool {
	for _, s := range sels {
		if s != cost.DefaultSelectivity {
			return true
		}
	}
	return false
}

// identityOrder returns [0,1,...,n-1].
func identityOrder(n int) []int {
	o := make([]int, n)
	for i := range o {
		o[i] = i
	}
	return o
}

// describeOrder builds a human-readable reason string for an ordering.
func describeOrder(basis string, sels []float64, order []int) string {
	// Reason is intentionally compact; the full selectivity list is in the
	// Explain output's structured form (added in O3.S6).
	return basis
}
