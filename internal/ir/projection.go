// Package ir — projection column-set tracking (I3.S3).
//
// Projection computes the output column set for each stage of a pipeline,
// threading the column set forward (like the binder's schema flow, but at the
// IR level for column-pruning and CTE-boundary decisions). Unlike the binder's
// Schema (which carries ColIDs + physical names), Projection works with column
// NAMES — it's a lightweight shape analysis used by the optimizer's
// ColumnPrune rule and future CTE-materialization decisions.
package ir

import "sort"

// ColSet is an ordered set of column names.
type ColSet struct {
	names []string
	seen  map[string]bool
}

// NewColSet creates an empty ColSet.
func NewColSet(names ...string) *ColSet {
	cs := &ColSet{seen: make(map[string]bool)}
	for _, n := range names {
		cs.Add(n)
	}
	return cs
}

// Add adds a column name (idempotent).
func (c *ColSet) Add(name string) {
	if !c.seen[name] {
		c.seen[name] = true
		c.names = append(c.names, name)
	}
}

// Has reports whether name is in the set.
func (c *ColSet) Has(name string) bool { return c.seen[name] }

// Names returns the column names in insertion order.
func (c *ColSet) Names() []string { return c.names }

// Len returns the number of columns.
func (c *ColSet) Len() int { return len(c.names) }

// Sorted returns the names sorted (for canonical comparison).
func (c *ColSet) Sorted() []string {
	out := make([]string, len(c.names))
	copy(out, c.names)
	sort.Strings(out)
	return out
}

// Projection computes the output column set after a pipeline runs. It walks
// stages in order, starting from the source table's columns.
func Projection(pipe *Pipeline, sourceCols []string) *ColSet {
	cols := NewColSet(sourceCols...)
	if pipe == nil {
		return cols
	}
	for _, st := range pipe.Stages {
		applyProjection(cols, st)
	}
	return cols
}

// applyProjection updates the column set to reflect a stage's output shape.
func applyProjection(cols *ColSet, st Stage) {
	switch s := st.(type) {
	case *Filter, *Sort, *Limit, *Distinct:
		// These don't change the column set (filter/sort/limit preserve; distinct
		// may reduce to its projected cols, but Distinct.Cols can reference all).
		// For Distinct: if it has explicit Cols, project to those.
		if d, ok := st.(*Distinct); ok {
			if len(d.Cols) > 0 {
				newCols := NewColSet()
				for _, e := range d.Cols {
					if name := exprColName(e); name != "" {
						newCols.Add(name)
					}
				}
				if newCols.Len() > 0 {
					*cols = *newCols
				}
			}
		}
	case *Project:
		// Project replaces the column set entirely.
		newCols := NewColSet()
		for _, c := range s.Cols {
			if _, ok := c.Expr.(*Star); ok {
				// project * = keep all input columns + any named new cols.
				for _, n := range cols.Names() {
					newCols.Add(n)
				}
				continue
			}
			if c.Name != "" {
				newCols.Add(c.Name)
			} else {
				if name := exprColName(c.Expr); name != "" {
					newCols.Add(name)
				}
			}
		}
		*cols = *newCols
	case *Extend:
		// Extend adds columns to the existing set.
		for _, c := range s.Cols {
			if c.Name != "" {
				cols.Add(c.Name)
			}
		}
	case *Aggregate:
		// Aggregate resets: output = by-keys + aggregate columns.
		newCols := NewColSet()
		for _, k := range s.Keys {
			if k.Name != "" {
				newCols.Add(k.Name)
			}
		}
		for _, a := range s.Aggregates {
			if a.Name != "" {
				newCols.Add(a.Name)
			}
		}
		*cols = *newCols
	case *Join:
		// Join: output = left columns ∪ right columns.
		// Right columns come from the sub-pipeline's source — we add them by
		// walking the right pipeline's projection (best-effort: uses source name).
		if s.Right != nil {
			if st2, ok := s.Right.Source.(*SourceTable); ok {
				// Can't know right columns without schema; add a marker.
				cols.Add("join:" + st2.Table)
			}
		}
	case *Union:
		// Union: output = union of all inputs (we only know the left columns;
		// additional inputs may add columns, but we don't track their schemas
		// at this level).
	}
}

// exprColName extracts a column name from a simple Col expression.
func exprColName(e Expr) string {
	if c, ok := e.(*Col); ok {
		return c.Name
	}
	return ""
}
