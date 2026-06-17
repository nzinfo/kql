package decision

import (
	"strings"

	"nzinfo/kql/internal/backend"
	"nzinfo/kql/internal/ir"
)

// ExplainOutput is the structured result of `kql explain` with optimizer
// decisions: the IR tree, the emitted SQL, and the per-decision rationale log.
// (O3.S6.) The CLI renders this for the user; tests assert on the decisions.
type ExplainOutput struct {
	IR         *ir.Pipeline
	SQL        string
	Args       []interface{}
	Dialect    backend.Dialect
	Decisions  []Decision // the optimizer's choices, in order
	RuleChanges int       // how many O2 rule rewrites fired
}

// RenderReasons returns the decision log as CLI-friendly lines.
func (e *ExplainOutput) RenderReasons() []string {
	out := make([]string, 0, len(e.Decisions))
	for _, d := range e.Decisions {
		out = append(out, "["+d.PolicyName+"] "+d.Choice+": "+d.Reason)
	}
	return out
}

// Explain builds an ExplainOutput for a pipeline + backend: runs the rule
// engine, runs the predicate-order decision (if a policy is given), emits the
// SQL, and collects every decision's reason. nil policy → skip cost-based
// decisions (rules-only Explain).
//
// This is the single entry point `kql explain` calls; it centralises the
// "optimise then emit then log" flow so the CLI stays thin.
func Explain(pipe *ir.Pipeline, bk backend.Backend, policy DecisionPolicy, est interface {
	Selectivity(table string, pred ir.Expr) float64
}, applyRules func(*ir.Pipeline) int) (*ExplainOutput, error) {
	out := &ExplainOutput{IR: pipe, Dialect: bk.Dialect()}
	if applyRules != nil {
		out.RuleChanges = applyRules(pipe)
	}
	if policy != nil {
		po := PredicateOrder{Policy: policy}
		if est != nil {
			po.Estimator = &estimatorAdapter{inner: est}
		}
		_, _, d := po.Apply(pipe)
		if d.Choice != "" {
			out.Decisions = append(out.Decisions, d)
		}
	}
	q, err := bk.Emit(pipe)
	if err != nil {
		return nil, err
	}
	out.SQL = q.SQL
	out.Args = q.Args
	return out, nil
}

// asEstimator is no longer needed (PredicateOrder takes the interface directly);
// the estimatorAdapter below bridges Explain's loose est param to it.

type estimatorAdapter struct {
	inner interface {
		Selectivity(table string, pred ir.Expr) float64
	}
}

func (a *estimatorAdapter) Selectivity(table string, pred ir.Expr) float64 {
	if a == nil || a.inner == nil {
		return 0
	}
	return a.inner.Selectivity(table, pred)
}

// Ensure strings is used (RenderReasons may grow to format richer).
var _ = strings.Join
