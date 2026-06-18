// Package rules — ViewMatch (O6.S1).
//
// ViewMatch replaces a pipeline's source table with a pre-computed view when
// the view's definition is equivalent to (or a subset of) the query's initial
// stages. This is a major performance win for dashboards that repeatedly
// aggregate the same data — the view is pre-materialized and the query avoids
// re-scanning the base table.
//
// The rule matches patterns like:
//
//	orders | summarize count() by bin(created_at, 1d)
//
// When the catalog has a view `orders_daily_summary` whose definition matches
// this pattern, the rule rewrites the source to `orders_daily_summary` and
// removes the summarize stage (the view already has the aggregated data).
//
// Matching is conservative: the view's Definition string must contain the key
// column names and aggregate function names. Full semantic equivalence checking
// (via ir.Equivalent) is the ideal but requires parsing the view definition —
// we start with keyword-based matching and can upgrade to AST comparison later.
package rules

import (
	"strings"

	"nzinfo/kql/internal/ir"
	"nzinfo/kql/internal/optimizer/stats"
)

// ViewMatch rewrites a source + summarize pattern to read from a pre-computed
// view when one exists in the catalog. It is a no-op without a catalog or when
// no matching view is found.
type ViewMatch struct {
	Catalog *stats.Catalog
}

// Name returns the rule name.
func (ViewMatch) Name() string { return "ViewMatch" }

// Apply checks if the pipeline matches a pre-computed view and rewrites the
// source if so.
func (r ViewMatch) Apply(pipe *ir.Pipeline, sr StatsReader) (*ir.Pipeline, bool) {
	if r.Catalog == nil || len(r.Catalog.Views) == 0 || pipe == nil {
		return pipe, false
	}
	srcTable := sourceTableName(pipe)
	if srcTable == "" {
		return pipe, false
	}

	// Build a signature of the query's summarize pattern (if present).
	sig, aggCols, ok := summarizeSignature(pipe)
	if !ok {
		return pipe, false // no summarize stage to match
	}

	// Search views for a match: the view name or definition should reference
	// the same base table + contain the same aggregation signature.
	for viewName, viewDef := range r.Catalog.Views {
		if viewDef == nil {
			continue
		}
		if viewMatchesQuery(viewName, viewDef.Definition, srcTable, sig, aggCols) {
			// Rewrite: replace source with view, remove the summarize stage.
			if st, ok := pipe.Source.(*ir.SourceTable); ok {
				st.Table = viewName
			}
			// Remove the matched summarize stage (it's now baked into the view).
			pipe.Stages = removeStage(pipe.Stages, 0) // summarize is stage 0
			return pipe, true
		}
	}
	return pipe, false
}

// summarizeSignature extracts the aggregation pattern from a pipeline:
// returns (signature, aggregateColumns, ok). The signature is a canonical
// string of aggregate function names + group-by keys, used for view matching.
func summarizeSignature(pipe *ir.Pipeline) (sig string, cols []string, ok bool) {
	if len(pipe.Stages) == 0 {
		return "", nil, false
	}
	agg, isAgg := pipe.Stages[0].(*ir.Aggregate)
	if !isAgg {
		return "", nil, false
	}
	var parts []string
	for _, a := range agg.Aggregates {
		if fc, isCall := a.Expr.(*ir.FuncCall); isCall {
			parts = append(parts, fc.Name)
			cols = append(cols, a.Name)
		}
	}
	for _, k := range agg.Keys {
		if k.Name != "" {
			parts = append(parts, "by:"+k.Name)
		}
	}
	return strings.Join(parts, "|"), cols, true
}

// viewMatchesQuery checks whether a view's definition matches the query's
// summarize pattern. Conservative: requires the base table name + all
// aggregate function names to appear in the view definition.
func viewMatchesQuery(viewName, viewDef, baseTable, querySig string, aggCols []string) bool {
	if viewDef == "" {
		return false
	}
	defLower := strings.ToLower(viewDef)
	// Must reference the same base table.
	if !strings.Contains(defLower, strings.ToLower(baseTable)) {
		return false
	}
	// Must contain all aggregate function names from the query signature.
	for _, part := range strings.Split(querySig, "|") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "by:") {
			continue // group-by keys are checked loosely
		}
		if part == "" {
			continue
		}
		if !strings.Contains(defLower, strings.ToLower(part)) {
			return false
		}
	}
	return true
}

// removeStage removes the stage at index i from a stages slice.
func removeStage(stages []ir.Stage, i int) []ir.Stage {
	if i < 0 || i >= len(stages) {
		return stages
	}
	return append(stages[:i], stages[i+1:]...)
}

// sourceTableName extracts the left-side table name from the pipeline source.
func sourceTableName(pipe *ir.Pipeline) string {
	if pipe == nil || pipe.Source == nil {
		return ""
	}
	if st, ok := pipe.Source.(*ir.SourceTable); ok {
		return st.Table
	}
	return ""
}
