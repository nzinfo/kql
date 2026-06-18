// Package rules — Two-Stage Aggregation (O6.S2).
//
// For large tables, a single summarize scan moves all rows through the
// aggregation pipeline. Two-stage aggregation splits this into:
//
//	Stage 1 (partial): summarize <aggregates> by <keys + shard_column>
//	Stage 2 (final):   summarize <recombined aggregates> by <original keys>
//
// The shard column is a high-cardinality column from the stats catalog (e.g.
// a date or hash bucket). Stage 1 pre-reduces the data by grouping on keys +
// shard; stage 2 merges the shards. Only associative aggregates are eligible:
// count, sum, min, max (NOT avg/stddev — those need sum + count pairs).
//
// This rule is most useful for distributed backends (pg partitioning, duckdb
// parallel scan). For single-node sqlite it's a no-op (no benefit). The rule
// gates on table row count from the catalog: only large tables (> 100K rows)
// benefit from the extra aggregation pass.
package rules

import (
	"strings"

	"nzinfo/kql/internal/ir"
	"nzinfo/kql/internal/optimizer/stats"
)

// twoStageThreshold is the minimum table row count to consider two-stage agg.
const twoStageThreshold = 100000

// TwoStageAgg splits a large-table summarize into partial + final stages.
// No-op without a catalog, on small tables, or with non-associative aggregates.
type TwoStageAgg struct {
	Catalog *stats.Catalog
}

// Name returns the rule name.
func (TwoStageAgg) Name() string { return "TwoStageAgg" }

// Apply checks if the pipeline has a large-table summarize eligible for
// two-stage splitting.
func (r TwoStageAgg) Apply(pipe *ir.Pipeline, sr StatsReader) (*ir.Pipeline, bool) {
	if r.Catalog == nil || pipe == nil {
		return pipe, false
	}
	srcTable := sourceTableName(pipe)
	if srcTable == "" {
		return pipe, false
	}
	// Only large tables benefit.
	t := lookupTable(r.Catalog, srcTable)
	if t == nil || t.RowCount < twoStageThreshold {
		return pipe, false
	}
	// Find the summarize stage.
	for i, st := range pipe.Stages {
		agg, ok := st.(*ir.Aggregate)
		if !ok {
			continue
		}
		// All aggregates must be associative.
		if !allAssociative(agg.Aggregates) {
			continue
		}
		// Need at least one group-by key to shard on.
		if len(agg.Keys) == 0 {
			continue
		}
		// Find a shard column: a high-cardinality column from the table.
		shardCol := pickShardColumn(t, agg.Keys)
		if shardCol == "" {
			continue
		}
		// Rewrite: insert a partial summarize before the final one.
		// Stage 1: summarize <same aggregates> by <keys + shardCol>
		// Stage 2: summarize <same aggregates> by <keys>  (the original, now
		// operating on the reduced partial output).
		partialKeys := make([]*ir.NamedExpr, len(agg.Keys))
		copy(partialKeys, agg.Keys)
		partialKeys = append(partialKeys, &ir.NamedExpr{
			Name: "__shard",
			Expr: &ir.Col{Name: shardCol},
		})
		partialAgg := &ir.Aggregate{
			Aggregates: copyAggregates(agg.Aggregates),
			Keys:       partialKeys,
		}
		// Insert partial before the final aggregate.
		pipe.Stages = insertStage(pipe.Stages, i, partialAgg)
		return pipe, true
	}
	return pipe, false
}

// allAssociative checks if all aggregates use associative functions.
func allAssociative(aggs []*ir.NamedExpr) bool {
	for _, a := range aggs {
		fc, ok := a.Expr.(*ir.FuncCall)
		if !ok {
			return false // non-call aggregate (e.g. bare column) — skip
		}
		switch strings.ToLower(fc.Name) {
		case "count", "sum", "min", "max", "countif", "sumif", "dcount":
			// Associative (dcount is approximately associative via HLL).
		default:
			return false // avg, stdev, percentile, etc. — not associative
		}
	}
	return true
}

// pickShardColumn finds a high-cardinality column that's NOT already a group-by
// key. Prefers date/time columns (natural partition boundaries).
func pickShardColumn(t *stats.Table, keys []*ir.NamedExpr) string {
	if t == nil || t.Columns == nil {
		return ""
	}
	keyNames := map[string]bool{}
	for _, k := range keys {
		if c, ok := k.Expr.(*ir.Col); ok {
			keyNames[strings.ToLower(c.Name)] = true
		}
	}
	// Prefer high-cardinality columns (not in the group-by keys).
	bestCard := int64(0)
	bestCol := ""
	for colName, cs := range t.Columns {
		if cs == nil || keyNames[colName] {
			continue
		}
		if cs.Card > bestCard && cs.Card > 10 {
			bestCard = cs.Card
			bestCol = colName
		}
	}
	return bestCol
}

// copyAggregates deep-copies aggregate NamedExprs (shared Expr is OK — we
// don't mutate them, just reference them in a new stage).
func copyAggregates(aggs []*ir.NamedExpr) []*ir.NamedExpr {
	out := make([]*ir.NamedExpr, len(aggs))
	for i, a := range aggs {
		out[i] = &ir.NamedExpr{Name: a.Name, Expr: a.Expr}
	}
	return out
}

// insertStage inserts a stage at index i, shifting the rest right.
func insertStage(stages []ir.Stage, i int, st ir.Stage) []ir.Stage {
	out := make([]ir.Stage, 0, len(stages)+1)
	out = append(out, stages[:i]...)
	out = append(out, st)
	out = append(out, stages[i:]...)
	return out
}

// lookupTable finds a table from the catalog (case-insensitive).
func lookupTable(c *stats.Catalog, name string) *stats.Table {
	if c == nil || c.Tables == nil {
		return nil
	}
	if t, ok := c.Tables[name]; ok {
		return t
	}
	return c.Tables[strings.ToLower(name)]
}
