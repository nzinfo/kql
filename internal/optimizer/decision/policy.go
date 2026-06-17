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
	"fmt"

	"nzinfo/kql/internal/ir"
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
	// ChooseJoinPlan selects a physical join method from the enumerated
	// candidates (O4). Returns the winning AltPlan and a Decision explaining
	// the rationale for Explain. candidates is never empty (the planner always
	// includes a Default/let-backend-decide option).
	ChooseJoinPlan(candidates []AltPlan, weights cost.CostWeights) (AltPlan, Decision)
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

// ChooseJoinPlan (O4): Conservative defaults to "let the backend planner
// decide" (the Default candidate) unless ONE non-default candidate is clearly
// dominant — defined as ≥10× cheaper than the default AND cheaper than every
// other candidate. This avoids overriding pg's planner on marginal calls while
// still picking up unambiguous wins (e.g. a tiny indexed lookup).
func (Conservative) ChooseJoinPlan(cands []AltPlan, w cost.CostWeights) (AltPlan, Decision) {
	def := findDefault(cands)
	if def == nil || len(cands) <= 1 {
		// No default option or no alternatives — return the first candidate.
		if len(cands) == 0 {
			return nil, Decision{PolicyName: "Conservative", Choice: "JoinPlan", Reason: "no candidates"}
		}
		return cands[0], Decision{PolicyName: "Conservative", Choice: "JoinPlan", Reason: cands[0].Describe()}
	}
	defCost := def.Cost().Total(w)
	// Find the cheapest non-default candidate.
	best := def
	bestCost := defCost
	clearlyDominant := false
	for _, c := range cands {
		if c.Kind() == ir.JoinHintNone {
			continue
		}
		cc := c.Cost().Total(w)
		if cc < bestCost {
			best = c
			bestCost = cc
			// "Clearly dominant": ≥10× cheaper than the default baseline.
			clearlyDominant = defCost > 0 && bestCost < defCost/10
		}
	}
	if !clearlyDominant {
		return def, Decision{
			PolicyName: "Conservative",
			Choice:     "JoinPlan",
			Reason:     "let backend planner decide (no candidate ≥10× cheaper than default)",
		}
	}
	return best, Decision{
		PolicyName: "Conservative",
		Choice:     "JoinPlan",
		Reason:     fmt.Sprintf("clear winner: %s (%.2f vs default %.2f)", best.Describe(), bestCost, defCost),
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

// ChooseJoinPlan (O4): Aggressive always picks the lowest-cost candidate
// (argmin Cost.Total(weights)), even with weak stats. It trusts the estimator's
// uniform-fallback estimates rather than deferring to the backend planner.
// Ties break in favor of a CONCRETE method (non-Default) — when costs are equal,
// an explicit hint documents the decision better than "let pg decide".
func (Aggressive) ChooseJoinPlan(cands []AltPlan, w cost.CostWeights) (AltPlan, Decision) {
	if len(cands) == 0 {
		return nil, Decision{PolicyName: "Aggressive", Choice: "JoinPlan", Reason: "no candidates"}
	}
	best := cands[0]
	bestCost := best.Cost().Total(w)
	for _, c := range cands[1:] {
		cc := c.Cost().Total(w)
		// Strictly cheaper → pick. Equal cost → prefer concrete (non-Default).
		if cc < bestCost || (cc == bestCost && best.Kind() == ir.JoinHintNone && c.Kind() != ir.JoinHintNone) {
			best = c
			bestCost = cc
		}
	}
	return best, Decision{
		PolicyName: "Aggressive",
		Choice:     "JoinPlan",
		Reason:     fmt.Sprintf("lowest cost: %s (%.4f)", best.Describe(), bestCost),
	}
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

// ChooseJoinPlan (O4): delegate to Aggressive or Conservative based on the
// join's left-table confidence. High confidence → trust the cost estimates
// (Aggressive); low confidence → defer to the backend planner (Conservative).
// The table name isn't available here (ChooseJoinPlan is join-agnostic in the
// interface), so we check confidence on the catalog as a whole — if ANY table
// in the catalog is high-confidence, we trust the estimates. This is a
// pragmatic approximation; a future refinement can thread the table name.
func (g ConfidenceGated) ChooseJoinPlan(cands []AltPlan, w cost.CostWeights) (AltPlan, Decision) {
	// Without a catalog we can't assess confidence → Conservative.
	if g.Catalog == nil {
		plan, d := Conservative{}.ChooseJoinPlan(cands, w)
		d.PolicyName = "ConfidenceGated"
		d.Reason = "no catalog → " + d.Reason
		return plan, d
	}
	// Check if the catalog has reasonable confidence overall (≥1 high-conf table).
	anyHigh := false
	for name := range g.Catalog.Tables {
		if cost.EstimateConfidence(g.Catalog, name) == cost.HighConfidence {
			anyHigh = true
			break
		}
	}
	if anyHigh {
		plan, d := Aggressive{}.ChooseJoinPlan(cands, w)
		d.PolicyName = "ConfidenceGated"
		d.Reason = "high confidence → " + d.Reason
		return plan, d
	}
	plan, d := Conservative{}.ChooseJoinPlan(cands, w)
	d.PolicyName = "ConfidenceGated"
	d.Reason = "low confidence → " + d.Reason
	return plan, d
}

// --- helpers ---

// findDefault returns the Default (JoinHintNone) candidate from a list, or nil.
func findDefault(cands []AltPlan) AltPlan {
	for _, c := range cands {
		if c.Kind() == ir.JoinHintNone {
			return c
		}
	}
	return nil
}

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
