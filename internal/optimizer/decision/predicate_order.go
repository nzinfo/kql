package decision

import (
	"nzinfo/kql/internal/frontend/token"
	"nzinfo/kql/internal/ir"
)

// nilEstimator is a SelectivityEstimator that returns a uniform 0.1, used when
// PredicateOrder has no real estimator (so the policy still gets input, just
// default values — Conservative will then keep source order).
type nilEstimator struct{}

func (nilEstimator) Selectivity(table string, pred ir.Expr) float64 { return 0.1 }

// PredicateOrder is the first cost-based decision wired into the optimizer:
// when a single Filter stage has an AND-of-predicates, reorder the conjuncts
// so the MOST SELECTIVE one evaluates first (short-circuits the rest).
//
// This is a real cost-based choice (unlike O2's always-safe rewrites): it
// consults a DecisionPolicy + a SelectivityEstimator. Conservative keeps source
// order when stats are weak; Aggressive always reorders; ConfidenceGated gates
// on catalog confidence. The decision (with reason) is recorded for Explain.
//
// NOTE: short-circuit reordering is semantically safe for pure predicates
// (AND is commutative) — it only changes performance, never results. So this
// rule, like O2 rules, preserves semantics; the "cost-based" part is which
// ORDER it picks, governed by the policy.
type PredicateOrder struct {
	Policy    DecisionPolicy
	Estimator SelectivityEstimator
	Table     string // the source table for selectivity lookup
}

// SelectivityEstimator is the minimal interface PredicateOrder needs (so it can
// accept a *cost.Estimator or a test fake without importing cost directly).
type SelectivityEstimator interface {
	Selectivity(table string, pred ir.Expr) float64
}

// Apply reorders a Filter's AND-conjuncts per the policy. Returns changed=true
// if the order changed. Only filters with an AND top-level predicate are
// considered; other predicate shapes pass through.
func (r PredicateOrder) Apply(pipe *ir.Pipeline) (*ir.Pipeline, bool, Decision) {
	if pipe == nil || r.Policy == nil {
		return pipe, false, Decision{}
	}
	changed := false
	var lastDecision Decision
	for _, st := range pipe.Stages {
		f, ok := st.(*ir.Filter)
		if !ok {
			continue
		}
		conjuncts := flattenAnd(f.Predicate)
		if len(conjuncts) < 2 {
			continue // nothing to reorder
		}
		// Estimate each conjunct's selectivity.
		sels := make([]float64, len(conjuncts))
		est := r.Estimator
		if est == nil {
			est = nilEstimator{}
		}
		for i, c := range conjuncts {
			sels[i] = est.Selectivity(r.Table, c)
		}
		order, d := r.Policy.OrderPredicates(r.Table, sels)
		lastDecision = d
		// Rebuild the AND in the chosen order (if it differs).
		if !sameOrder(order, len(conjuncts)) {
			reordered := make([]ir.Expr, len(conjuncts))
			for i, idx := range order {
				reordered[i] = conjuncts[idx]
			}
			f.Predicate = chainAnd(reordered)
			changed = true
		}
	}
	return pipe, changed, lastDecision
}

// flattenAnd extracts the conjuncts of a (possibly nested) AND tree into a
// flat slice. A non-AND predicate returns a single-element slice.
func flattenAnd(e ir.Expr) []ir.Expr {
	if b, ok := e.(*ir.BinOp); ok && b.Op == token.AND {
		return append(flattenAnd(b.X), flattenAnd(b.Y)...)
	}
	if e == nil {
		return nil
	}
	return []ir.Expr{e}
}

// chainAnd rebuilds a left-leaning AND tree from a slice of conjuncts.
func chainAnd(conjuncts []ir.Expr) ir.Expr {
	if len(conjuncts) == 0 {
		return nil
	}
	out := conjuncts[0]
	for _, c := range conjuncts[1:] {
		out = &ir.BinOp{Op: token.AND, X: out, Y: c}
	}
	return out
}

// sameOrder reports whether order is [0,1,...,n-1] (i.e. unchanged from source).
func sameOrder(order []int, n int) bool {
	if len(order) != n {
		return false
	}
	for i := range order {
		if order[i] != i {
			return false
		}
	}
	return true
}
