package rules

import (
	"nzinfo/kql/internal/ir"
)

// PredicatePushdownUnion (O6) pushes a Filter that appears AFTER a Union
// into BOTH union inputs, so each side filters early (before the UNION
// concatenation). This is semantically safe because UNION's output contains
// all rows from all inputs — a post-union predicate applies to every input
// row equally, so pushing it into each input preserves the result set.
//
// Example:
//
//	(T | ...) | union (U | ...) | where state == "TX"
//	→ (T | ... | where state == "TX") | union (U | ... | where state == "TX")
//
// This lets the database filter in each sub-scan rather than materialising
// the full union then filtering.
type PredicatePushdownUnion struct{}

// Name returns the rule identifier.
func (PredicatePushdownUnion) Name() string { return "PredicatePushdownUnion" }

// Apply pushes a post-Union Filter into the union's inputs.
func (PredicatePushdownUnion) Apply(pipe *ir.Pipeline, _ StatsReader) (*ir.Pipeline, bool) {
	if pipe == nil || len(pipe.Stages) < 2 {
		return pipe, false
	}
	changed := false
	for i := 1; i < len(pipe.Stages); i++ {
		f, ok := pipe.Stages[i].(*ir.Filter)
		if !ok {
			continue
		}
		u, ok := pipe.Stages[i-1].(*ir.Union)
		if !ok || len(u.Inputs) == 0 {
			continue
		}
		// Push the predicate into each union input (append as a trailing Filter).
		for _, in := range u.Inputs {
			if in == nil {
				continue
			}
			// Clone the predicate for each input (each gets its own copy; the
			// IR nodes are not shared after translate, so a shallow append is safe).
			in.Stages = append(in.Stages, &ir.Filter{
				Position:  f.Position,
				Predicate: f.Predicate,
			})
		}
		// Remove the post-union filter (it's now inside each input).
		pipe.Stages = append(pipe.Stages[:i], pipe.Stages[i+1:]...)
		changed = true
		i-- // re-check this position (another filter may follow)
	}
	return pipe, changed
}
