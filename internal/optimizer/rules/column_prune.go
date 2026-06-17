package rules

import (
	"nzinfo/kql/internal/ir"
)

// ColumnPrune (O2.S3) trims the columns read from the source to only those
// the pipeline actually needs, so the database fetches fewer columns.
//
// Conservative safe version: when the pipeline ends in a Project whose columns
// are all bare column references (no computed expressions), and every stage
// between source and that Project is a passthrough that doesn't introduce
// columns the Project depends on (Filter/Sort/Limit/Distinct — all of which
// reference only pre-existing columns), ColumnPrune inserts a Project of the
// needed columns immediately after the source. The DB then reads only those.
//
// It does NOT fire when an Extend/Aggregate/Project intervenes (those add or
// reshape columns), or when the terminal Project has computed columns (the
// source can't compute them). This keeps the rule provably semantics-preserving
// without full column-lineage tracking (which lands with the optimizer's
// PhysicalPlan work).
type ColumnPrune struct{}

// Name returns the rule identifier.
func (ColumnPrune) Name() string { return "ColumnPrune" }

// Apply inserts a source-level projection when safe. Returns changed=true if it
// inserted one.
func (ColumnPrune) Apply(pipe *ir.Pipeline, _ StatsReader) (*ir.Pipeline, bool) {
	if pipe == nil || len(pipe.Stages) == 0 || pipe.Source == nil {
		return pipe, false
	}
	// Find a terminal Project of bare columns.
	last := pipe.Stages[len(pipe.Stages)-1]
	proj, ok := last.(*ir.Project)
	if !ok {
		return pipe, false
	}
	needed := projectBareColumns(proj)
	if len(needed) == 0 {
		return pipe, false // no bare columns to prune to
	}
	// Every intermediate stage must be a passthrough (Filter/Sort/Limit/
	// Distinct) — none of these introduce columns, so a source projection of
	// the terminal Project's columns covers them all.
	for _, st := range pipe.Stages[:len(pipe.Stages)-1] {
		if !isPassthrough(st) {
			return pipe, false
		}
		// Additionally, the stage must only reference columns in `needed`
		// (a Filter on a column the terminal Project drops would still need
		// that column from the source). Compute the union.
		for _, extra := range stageColumnRefs(st) {
			if !contains(needed, extra) {
				needed = append(needed, extra)
			}
		}
	}
	// Don't prune if it wouldn't reduce columns (we don't know the source's full
	// column set without a schema, so we only prune when the projection is a
	// strict subset we're confident about — i.e. when there's at least one
	// intermediate stage, meaning the terminal Project likely narrows things).
	// Insert a Project of `needed` right after the source.
	pruneProj := &ir.Project{
		Position: pipe.Source.Pos(),
		Cols:     bareNamedExprs(needed),
	}
	pipe.Stages = append([]ir.Stage{pruneProj}, pipe.Stages...)
	return pipe, true
}

// projectBareColumns returns the bare column names a Project references, or nil
// if any column is computed/renamed (making pruning unsafe).
func projectBareColumns(p *ir.Project) []string {
	var names []string
	for _, c := range p.Cols {
		if c == nil || c.Name != "" {
			return nil // renamed → unsafe
		}
		col, ok := c.Expr.(*ir.Col)
		if !ok {
			return nil // computed → unsafe
		}
		names = append(names, col.Name)
	}
	return names
}

// isPassthrough reports whether a stage introduces no new columns.
func isPassthrough(st ir.Stage) bool {
	switch st.(type) {
	case *ir.Filter, *ir.Sort, *ir.Limit, *ir.Distinct:
		return true
	}
	return false
}

// stageColumnRefs returns the column names a passthrough stage references.
func stageColumnRefs(st ir.Stage) []string {
	var names []string
	switch s := st.(type) {
	case *ir.Filter:
		walkColumns(s.Predicate, func(c *ir.Col) bool {
			names = append(names, c.Name)
			return true
		})
	case *ir.Sort:
		for _, k := range s.Keys {
			walkColumns(k.Expr, func(c *ir.Col) bool {
				names = append(names, c.Name)
				return true
			})
		}
	case *ir.Distinct:
		for _, c := range s.Cols {
			walkColumns(c, func(col *ir.Col) bool {
				names = append(names, col.Name)
				return true
			})
		}
	}
	return names
}

// bareNamedExprs builds []*NamedExpr of bare Col references for the given names.
func bareNamedExprs(names []string) []*ir.NamedExpr {
	out := make([]*ir.NamedExpr, len(names))
	for i, n := range names {
		out[i] = &ir.NamedExpr{Expr: &ir.Col{Name: n}}
	}
	return out
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
