// Package decision — AltPlan interface (O3.S1 / O4 foundation).
//
// An AltPlan is one physical execution strategy for an IR node. The
// PhysicalPlanner (join_plan.go) enumerates ≥2 AltPlans per JOIN node; the
// DecisionPolicy chooses the winner by cost. DESIGN §6.4 specifies
// `AltPlan { Cost(stats, cm) Cost; Emit(Dialect) PhysicalStep }`, but
// backend.PhysicalStep doesn't exist yet (B1.S3 is deferred). Per the
// user-approved O4 design, the chosen plan is instead stamped onto ir.Join.Hint
// (a JoinHint enum) and read by the existing emitter — so AltPlan here is the
// minimal decision-layer shape: the cost is precomputed at construction (the
// planner has the estimator context) and Kind() returns the hint to set.
package decision

import (
	"nzinfo/kql/internal/ir"
	"nzinfo/kql/internal/optimizer/cost"
)

// AltPlan is one candidate physical strategy for an IR node. Implementations
// carry their precomputed Cost (so the policy can compare without re-estimating)
// and the JoinHint they advocate (so the planner can stamp it onto the IR).
type AltPlan interface {
	// Kind returns the JoinHint this plan advocates (JoinHintNone = "let the
	// backend planner decide", the conservative baseline).
	Kind() ir.JoinHint
	// Cost returns the precomputed cost vector. The policy compares plans via
	// Cost.Total(weights).
	Cost() cost.Cost
	// Describe returns a one-line human-readable summary for Explain, e.g.
	// "HashJoin sel=0.10 out=50000 inner=500rows".
	Describe() string
}

// joinAltPlan is the concrete AltPlan for join-method choices. It is built by
// join_plan.go's enumeration once the cost is known; the fields are immutable.
type joinAltPlan struct {
	kind ir.JoinHint
	c    cost.Cost
	desc string
}

func (p *joinAltPlan) Kind() ir.JoinHint  { return p.kind }
func (p *joinAltPlan) Cost() cost.Cost    { return p.c }
func (p *joinAltPlan) Describe() string   { return p.desc }

// newPlan constructs a join AltPlan. Used by the planner in join_plan.go.
func newPlan(kind ir.JoinHint, c cost.Cost, desc string) AltPlan {
	return &joinAltPlan{kind: kind, c: c, desc: desc}
}
