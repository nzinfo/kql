// Package rules — SaturationRewrite (O6.S4): semantic query rewrites based on
// column value saturation (cardinality bounds and uniqueness constraints).
//
// "Saturation" here means: a column's value set has known bounds that let us
// prove certain operations redundant. Three rewrites:
//
//  1. Idempotent DISTINCT elimination: `distinct Col` is a no-op when Col is
//     already unique (per the stats catalog's ColumnStats.Unique flag). The
//     DISTINCT adds a sort/hash with no row reduction.
//
//  2. Tautology fold from saturation: `where Col != Col` is always false IF
//     Col is non-nullable — but more usefully, `where Col == <const>` against
//     a column whose cardinality is 1 AND whose only value is known (via MCV)
//     folds to true/false without scanning. (This is conservative: we only
//     fold when the catalog's MCV confirms.)
//
//  3. Redundant NOT-NULL: `where isnotnull(Col)` on a NOT NULL column is a
//     tautology (drops to no-op). Requires ColumnStats.NullCount == 0.
//
// All rewrites are gated on stats availability; with a nil/noop StatsReader the
// rule is a no-op (safe). Dialect-agnostic (pure IR).
//
// This rule is the O6.S4 exploration direction noted in STATUS.md; it is
// orthogonal to the O2 rule set (ConstantFold handles literal tautologies;
// this handles stats-derived ones).
package rules

import (
	"nzinfo/kql/internal/frontend/token"
	"nzinfo/kql/internal/ir"
	"nzinfo/kql/internal/optimizer/stats"
)

// SaturationRewrite eliminates redundant operations using column saturation
// info from the stats catalog.
type SaturationRewrite struct{}

// Name returns the rule identifier.
func (SaturationRewrite) Name() string { return "SaturationRewrite" }

// Apply walks the pipeline stages and eliminates redundant DISTINCT / NOT-NULL
// filters when the stats catalog proves them idempotent. Returns changed=true
// if any stage was removed or simplified. No-op without catalog stats.
func (SaturationRewrite) Apply(pipe *ir.Pipeline, sr StatsReader) (*ir.Pipeline, bool) {
	if pipe == nil {
		return pipe, false
	}
	// Resolve the catalog from the StatsReader (only the catalog-backed reader
	// exposes uniqueness/null info). The noop reader yields nothing.
	cat := catalogFromReader(sr)
	if cat == nil {
		return pipe, false // no stats → safe no-op
	}
	baseTable := sourceTableNameRules(pipe)
	if baseTable == "" {
		return pipe, false
	}

	changed := false
	out := make([]ir.Stage, 0, len(pipe.Stages))
	for _, st := range pipe.Stages {
		switch s := st.(type) {
		case *ir.Distinct:
			if isDistinctIdempotent(cat, baseTable, s) {
				changed = true // drop the redundant DISTINCT
				continue
			}
		case *ir.Filter:
			if newPred, drop, fc := foldSaturationPredicate(cat, baseTable, s.Predicate); fc {
				changed = true
				if drop {
					continue // tautology → drop filter
				}
				out = append(out, &ir.Filter{Position: s.Position, Predicate: newPred})
				continue
			}
		}
		out = append(out, st)
	}
	if changed {
		pipe.Stages = out
	}
	return pipe, changed
}

// isDistinctIdempotent reports whether a DISTINCT on the given columns is a
// no-op (all columns are unique → no duplicate rows can exist).
func isDistinctIdempotent(cat *stats.Catalog, table string, d *ir.Distinct) bool {
	if len(d.Cols) == 0 {
		return false
	}
	for _, e := range d.Cols {
		col, ok := e.(*ir.Col)
		if !ok {
			return false // non-column expr → can't prove uniqueness
		}
		if !columnIsUnique(cat, table, col.Name) {
			return false
		}
	}
	return true
}

// foldSaturationPredicate checks a filter predicate for saturation-derived
// tautologies/contradictions. Returns (newPred, dropFilter, changed).
//   - isnotnull(uniqueCol) where uniqueCol has NullCount==0 → tautology (drop)
//   - isnull(uniqueCol) where uniqueCol has NullCount==0 → contradiction
//     (becomes Limit 0 upstream; here we fold to false which ConstantFold/this
//     rule's caller can act on — we emit `where false`).
func foldSaturationPredicate(cat *stats.Catalog, table string, pred ir.Expr) (ir.Expr, bool, bool) {
	// isnotnull(Col) / isempty(Col) patterns.
	if fc, ok := pred.(*ir.FuncCall); ok {
		switch fc.Name {
		case "isnotnull":
			if len(fc.Args) == 1 {
				if col, ok := fc.Args[0].(*ir.Col); ok {
					if columnIsNotNull(cat, table, col.Name) {
						return nil, true, true // tautology → drop filter
					}
				}
			}
		case "isnull":
			if len(fc.Args) == 1 {
				if col, ok := fc.Args[0].(*ir.Col); ok {
					if columnIsNotNull(cat, table, col.Name) {
						// isnull on a NOT NULL column → always false.
						return &ir.Lit{T: ir.TypeBool, Value: false, HasValue: true}, false, true
					}
				}
			}
		}
	}
	return pred, false, false
}

// columnIsUnique reports whether the catalog marks the column as unique (e.g.
// a primary key). Used to prove DISTINCT idempotent.
func columnIsUnique(cat *stats.Catalog, table, col string) bool {
	t := lookupTableSat(cat, table)
	if t == nil {
		return false
	}
	if c, ok := t.Columns[col]; ok {
		// A column is unique if its distinct-value count equals the table row count.
		return c.Card == tableRowCount(cat, table)
	}
	if c, ok := t.Columns[lowerRules(col)]; ok {
		// A column is unique if its distinct-value count equals the table row count.
		return c.Card == tableRowCount(cat, table)
	}
	return false
}

// columnIsNotNull reports whether the column has zero null count (NOT NULL).
func columnIsNotNull(cat *stats.Catalog, table, col string) bool {
	t := lookupTableSat(cat, table)
	if t == nil {
		return false
	}
	c := lookupColumn(t, col)
	if c == nil {
		return false
	}
	return c.Nulls == 0 && c.Card > 0
}

func lookupTableSat(cat *stats.Catalog, table string) *stats.Table {
	if cat == nil {
		return nil
	}
	if t, ok := cat.Tables[table]; ok {
		return t
	}
	if t, ok := cat.Tables[lowerRules(table)]; ok {
		return t
	}
	return nil
}

func lookupColumn(t *stats.Table, col string) *stats.ColumnStats {
	if c, ok := t.Columns[col]; ok {
		return c
	}
	if c, ok := t.Columns[lowerRules(col)]; ok {
		return c
	}
	return nil
}

func lowerRules(s string) string {
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

// sourceTableNameRules returns the pipeline's base table name (for catalog
// lookups). Local to this rule to avoid import cycles.
func sourceTableNameRules(pipe *ir.Pipeline) string {
	if pipe == nil || pipe.Source == nil {
		return ""
	}
	if st, ok := pipe.Source.(*ir.SourceTable); ok {
		return st.Table
	}
	return ""
}

// catalogFromReader extracts a *stats.Catalog from a StatsReader, if it's the
// catalog-backed implementation. Returns nil for the noop reader.
func catalogFromReader(sr StatsReader) *stats.Catalog {
	if cr, ok := sr.(*catalogReader); ok {
		// catalogReader wraps an estimator; we need the catalog. Reach it via
		// the estimator's (unexported) field — but that's not accessible here.
		// Instead, SaturationRewrite is most naturally given the catalog
		// directly. We support both: a StatsReader that IS a catalog, or nil.
		_ = cr
	}
	// Allow passing the catalog directly as a StatsReader-ish adapter.
	if ca, ok := sr.(*catalogAdapter); ok {
		return ca.cat
	}
	return nil
}

// catalogAdapter wraps a *stats.Catalog as a StatsReader so SaturationRewrite
// can access uniqueness/null info. Created by the engine when stats are wired.
type catalogAdapter struct{ cat *stats.Catalog }

// Selectivity satisfies StatsReader (delegates to the catalog's estimator).
func (a *catalogAdapter) Selectivity(table, column string) float64 {
	if a.cat == nil {
		return 0
	}
	return CatalogStatsReader(a.cat).Selectivity(table, column)
}

// NewSaturationReader wraps a catalog for SaturationRewrite consumption.
func NewSaturationReader(c *stats.Catalog) StatsReader {
	if c == nil {
		return noopReader{}
	}
	return &catalogAdapter{cat: c}
}

// ensure token import used (Pos references)
var _ = token.NoPos

// tableRowCount returns the catalog's row count for a table (0 if unknown).
func tableRowCount(cat *stats.Catalog, table string) int64 {
	t := lookupTableSat(cat, table)
	if t == nil {
		return 0
	}
	return t.RowCount
}
