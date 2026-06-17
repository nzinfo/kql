// Package decision — JoinPlan: cost-based join-method selection (O4).
//
// JoinPlan is the join-method analogue of PredicateOrder: it walks a pipeline's
// stages, locates each *ir.Join, enumerates the feasible physical plans (AltPlans),
// costs them, and asks the DecisionPolicy to choose. The winner is stamped onto
// ir.Join.Hint, which the backend emitter reads to produce a join-method
// directive (pg_hint_plan comment for pg; no-op for sqlite/duckdb).
//
// Enumeration rules (O4 phase doc, risk "AltPlan 数量多 → 限制每 join 节点 ≤4 候选"):
//   - Default (let backend decide) is ALWAYS a candidate (the Conservative fallback).
//   - HashJoin and NestLoop are always enumerated (the two universal methods).
//   - MergeJoin is added only when a join key has corr_vs (proxy for sorted input).
//   - IndexLookup is added only when the inner side has an index on a join key
//     AND the inner is large (small inner → Hash is better; IndexLookup wins on
//     large indexed inner with small outer).
//
// Apply is a NO-OP when Policy or Catalog is nil (the no-regression guarantee:
// without stats there's nothing to cost, so the join keeps JoinHintNone).
package decision

import (
	"strings"

	"nzinfo/kql/internal/frontend/token"
	"nzinfo/kql/internal/ir"
	"nzinfo/kql/internal/optimizer/cost"
	"nzinfo/kql/internal/optimizer/stats"
)

// JoinPlan is the cost-based join-method decision pass (O4). Apply it to a
// pipeline after the rewrite rules + PredicateOrder have run.
type JoinPlan struct {
	Policy  DecisionPolicy
	Catalog *stats.Catalog
	Weights cost.CostWeights // per-backend (cost.DefaultWeights(dialect))
}

// Apply walks the pipeline, enumerates + costs join plans for each *ir.Join,
// and stamps the winner's JoinHint onto the IR. Returns changed=true if any
// join's hint was set. Mirrors PredicateOrder.Apply's shape.
func (r JoinPlan) Apply(pipe *ir.Pipeline) (*ir.Pipeline, bool, Decision) {
	if pipe == nil || r.Policy == nil || r.Catalog == nil {
		return pipe, false, Decision{}
	}
	leftTable := sourceTableName(pipe)
	changed := false
	var lastDecision Decision
	for _, st := range pipe.Stages {
		j, ok := st.(*ir.Join)
		if !ok {
			continue
		}
		rightTable := joinRightTableName(j)
		cands := r.enumerate(leftTable, rightTable, j)
		if len(cands) == 0 {
			continue
		}
		winner, d := r.Policy.ChooseJoinPlan(cands, r.Weights)
		lastDecision = d
		if winner != nil && winner.Kind() != ir.JoinHintNone {
			j.Hint = winner.Kind()
			changed = true
		}
	}
	return pipe, changed, lastDecision
}

// enumerate builds the candidate AltPlans for one join node. See package doc
// for the feasibility rules (Default always; Hash/NestLoop always; Merge if
// corr; IndexLookup if inner indexed + large).
func (r JoinPlan) enumerate(leftTable, rightTable string, j *ir.Join) []AltPlan {
	est := cost.NewEstimator(r.Catalog)
	leftCard, leftBytes := tableCard(r.Catalog, leftTable)
	rightCard, rightBytes := tableCard(r.Catalog, rightTable)
	sel := est.JoinSelectivity(leftTable, rightTable, j.On, leftCard, rightCard)
	out := est.OutputCardinality(leftTable, rightTable, j.On, leftCard, rightCard)
	cm := r.Catalog.CostModel
	hasCorr := joinHasCorr(r.Catalog, leftTable, rightTable, j.On)
	innerIndexed := innerHasIndex(r.Catalog, rightTable, j.On)
	in := &joinCostInput{
		est:          est,
		cm:           cm,
		leftTable:    leftTable,
		rightTable:   rightTable,
		on:           j.On,
		leftCard:     leftCard,
		rightCard:    rightCard,
		leftBytes:    leftBytes,
		rightBytes:   rightBytes,
		sel:          sel,
		outCard:      out,
		hasCorr:      hasCorr,
		innerIndexed: innerIndexed,
	}
	cands := []AltPlan{
		newPlan(ir.JoinHintNone, defaultJoinCost(in), describeJoin("Default", in)),
		newPlan(ir.JoinHintHash, hashJoinCost(in), describeJoin("HashJoin", in)),
		newPlan(ir.JoinHintNestLoop, nestLoopCost(in), describeJoin("NestLoop", in)),
	}
	if hasCorr {
		cands = append(cands, newPlan(ir.JoinHintMerge, mergeJoinCost(in), describeJoin("MergeJoin", in)))
	}
	// IndexLookup: only when inner has an index on a join key AND inner is large
	// (small inner → Hash wins; the IN-list rewrite pays off on large indexed tables).
	if innerIndexed && rightCard > 1000 {
		cands = append(cands, newPlan(ir.JoinHintIndexLookup, indexLookupCost(in), describeJoin("IndexLookup", in)))
	}
	return cands
}

// --- table/field resolution helpers ---

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

// joinRightTableName extracts the right-side table name from a Join's sub-pipeline.
func joinRightTableName(j *ir.Join) string {
	if j == nil || j.Right == nil || j.Right.Source == nil {
		return ""
	}
	if st, ok := j.Right.Source.(*ir.SourceTable); ok {
		return st.Table
	}
	return ""
}

// tableCard returns (rowCount, avgRowBytes) for a table from the catalog.
func tableCard(c *stats.Catalog, table string) (int64, int) {
	if c == nil || table == "" {
		return 0, 0
	}
	t, ok := c.Tables[strings.ToLower(table)]
	if !ok {
		t, ok = c.Tables[table]
	}
	if !ok || t == nil {
		return 0, 0
	}
	return t.RowCount, t.AvgRowBytes
}

// joinKeys extracts the (leftCol, rightCol) pairs from a join's ON conditions.
func joinKeys(on []ir.Expr) [] [2]string {
	var out [][2]string
	for _, cond := range on {
		b, ok := cond.(*ir.BinOp)
		if !ok || b.Op != token.EQL {
			continue
		}
		l, r := colName(b.X), colName(b.Y)
		if l == "" || r == "" {
			continue
		}
		out = append(out, [2]string{l, r})
	}
	return out
}

func colName(e ir.Expr) string {
	if c, ok := e.(*ir.Col); ok {
		return c.Name
	}
	return ""
}

// joinHasCorr reports whether any join key column has a corr_vs entry (the
// MergeJoin feasibility proxy — correlated keys suggest naturally-ordered data).
func joinHasCorr(c *stats.Catalog, leftTable, rightTable string, on []ir.Expr) bool {
	if c == nil {
		return false
	}
	for _, pair := range joinKeys(on) {
		if colHasCorr(c, leftTable, pair[0]) || colHasCorr(c, rightTable, pair[1]) {
			return true
		}
	}
	return false
}

func colHasCorr(c *stats.Catalog, table, col string) bool {
	if c == nil || table == "" || col == "" {
		return false
	}
	t, ok := c.Tables[strings.ToLower(table)]
	if !ok {
		t, ok = c.Tables[table]
	}
	if !ok || t == nil {
		return false
	}
	cs, ok := t.Columns[strings.ToLower(col)]
	if !ok {
		cs, ok = t.Columns[col]
	}
	if !ok || cs == nil {
		return false
	}
	return cs.CorrVs != nil && cs.CorrVs.Rho != 0
}

// innerHasIndex reports whether the inner (right) table has an index whose
// leading column is a join key (the IndexLookup feasibility check).
func innerHasIndex(c *stats.Catalog, rightTable string, on []ir.Expr) bool {
	if c == nil || rightTable == "" {
		return false
	}
	t, ok := c.Tables[strings.ToLower(rightTable)]
	if !ok {
		t, ok = c.Tables[rightTable]
	}
	if !ok || t == nil || len(t.Indexes) == 0 {
		return false
	}
	keys := joinKeys(on)
	for _, idx := range t.Indexes {
		if len(idx.Columns) == 0 {
			continue
		}
		lead := strings.ToLower(idx.Columns[0])
		for _, k := range keys {
			if strings.ToLower(k[1]) == lead { // k[1] = right col
				return true
			}
		}
	}
	return false
}
