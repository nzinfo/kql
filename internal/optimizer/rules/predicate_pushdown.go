package rules

import (
	"nzinfo/kql/internal/ir"
)

// PredicatePushdown (O2.S2) moves WHERE filters as close to the source as
// possible, past Extend/Project stages, so the database filters rows early
// instead of materialising the full input then filtering.
//
// Safety rules (a predicate MUST NOT cross a stage if):
//   - the stage is an Aggregate/Join/Union/Distinct (semantics change — the
//     predicate's columns may not exist post-aggregation, or the row count
//     semantics differ). Pushed filters stop at these barriers.
//   - the stage is an Extend/Project and the predicate references a column the
//     stage INTRODUCES (an extend-added column or a project-renamed column that
//     shadows the original). The filter stays AFTER such a stage.
//
// A predicate that only references columns present in the SOURCE (or a stage's
// passthrough columns) can be pushed past Extend/Project freely.
//
// Implementation: walk stages right-to-left. For each Filter, try to move it
// leftward past consecutive Extend/Project barriers it doesn't depend on,
// stopping at the source or an impassable stage.
type PredicatePushdown struct{}

// Name returns the rule identifier.
func (PredicatePushdown) Name() string { return "PredicatePushdown" }

// Apply attempts to push filters down. Returns changed=true if any filter moved.
func (PredicatePushdown) Apply(pipe *ir.Pipeline, _ StatsReader) (*ir.Pipeline, bool) {
	if pipe == nil || len(pipe.Stages) < 2 {
		return pipe, false
	}
	changed := false
	// Repeat passes until stable (each pass may enable further pushes).
	for {
		passChanged := false
		// Walk left-to-right; when we find a Filter preceded by a pushable-
		// past stage, try to swap them.
		for i := 1; i < len(pipe.Stages); i++ {
			f, ok := pipe.Stages[i].(*ir.Filter)
			if !ok {
				continue
			}
			prev := pipe.Stages[i-1]
			if !canPushPast(prev, f.Predicate) {
				continue
			}
			// Swap: filter moves before prev.
			pipe.Stages[i-1], pipe.Stages[i] = f, prev
			passChanged = true
		}
		if !passChanged {
			break
		}
		changed = true
	}
	return pipe, changed
}

// canPushPast reports whether a filter with predicate `pred` may be moved
// BEFORE stage `st` (i.e. the filter doesn't depend on anything `st` produces).
// Returns false for impassable stages (Aggregate/Join/Union/Distinct/Limit/Sort).
func canPushPast(st ir.Stage, pred ir.Expr) bool {
	switch s := st.(type) {
	case *ir.Extend:
		// The filter can pass if it doesn't reference any column Extend adds.
		// (Extend keeps all input columns, so pre-existing cols are fine.)
		return !referencesAny(pred, extendAddedColumns(s))
	case *ir.Project:
		// Project REPLACES the column set. The filter can pass only if every
		// column it references is a passthrough (projected from a bare Col of
		// the same name). For the minimal rule, only allow push past a Project
		// whose projected columns are ALL bare column refs (no expressions),
		// and the predicate references only those names.
		names := projectPassthroughNames(s)
		if names == nil {
			return false // has computed/renamed cols — conservatively block
		}
		return referencesOnly(pred, names)
	}
	// Aggregate/Join/Union/Distinct/Limit/Sort: impassable.
	return false
}

// extendAddedColumns returns the names of columns an Extend stage introduces.
func extendAddedColumns(e *ir.Extend) []string {
	var out []string
	for _, c := range e.Cols {
		if c != nil && c.Name != "" {
			out = append(out, c.Name)
		}
	}
	return out
}

// projectPassthroughNames returns the projected column names if ALL of a
// Project's columns are bare column references (passthrough), else nil.
func projectPassthroughNames(p *ir.Project) []string {
	if len(p.Cols) == 0 {
		return nil
	}
	names := make([]string, 0, len(p.Cols))
	for _, c := range p.Cols {
		if c == nil {
			return nil
		}
		// Must be unnamed AND a bare Col reference.
		if c.Name != "" {
			return nil
		}
		col, ok := c.Expr.(*ir.Col)
		if !ok {
			return nil
		}
		names = append(names, col.Name)
	}
	return names
}

// referencesAny reports whether expr references any of the named columns.
func referencesAny(expr ir.Expr, names []string) bool {
	if len(names) == 0 {
		return false
	}
	set := toSet(names)
	hit := false
	walkColumns(expr, func(c *ir.Col) bool {
		if set[c.Name] {
			hit = true
			return false // stop
		}
		return true
	})
	return hit
}

// referencesOnly reports whether expr references ONLY columns in `names`.
func referencesOnly(expr ir.Expr, names []string) bool {
	set := toSet(names)
	ok := true
	walkColumns(expr, func(c *ir.Col) bool {
		if !set[c.Name] {
			ok = false
			return false
		}
		return true
	})
	return ok
}

func toSet(names []string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

// walkColumns calls visit on every *ir.Col in the expression tree. visit
// returns true to continue, false to stop.
func walkColumns(e ir.Expr, visit func(*ir.Col) bool) {
	if e == nil {
		return
	}
	switch n := e.(type) {
	case *ir.Col:
		visit(n)
	case *ir.BinOp:
		walkColumns(n.X, visit)
		walkColumns(n.Y, visit)
	case *ir.UnaryOp:
		walkColumns(n.X, visit)
	case *ir.FuncCall:
		for _, a := range n.Args {
			walkColumns(a, visit)
		}
	case *ir.Member:
		walkColumns(n.X, visit)
	case *ir.Index:
		walkColumns(n.X, visit)
		walkColumns(n.Index, visit)
	case *ir.Case:
		walkColumns(n.Cond, visit)
		walkColumns(n.Then, visit)
		walkColumns(n.Else, visit)
	case *ir.List:
		for _, el := range n.Elems {
			walkColumns(el, visit)
		}
	}
}
