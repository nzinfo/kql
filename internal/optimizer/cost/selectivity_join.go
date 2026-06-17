package cost

import (
	"nzinfo/kql/internal/frontend/token"
	"nzinfo/kql/internal/ir"
	"nzinfo/kql/internal/optimizer/stats"
)

// joinKey is a pair of joined column names (left, right), shared with corr.go.
type joinKey struct{ lCol, rCol string }

// JoinSelectivity estimates the row-count multiplier of a join's equality
// conditions (DESIGN.md §6.3 table row 6):
//
//	t1.a = t2.a  →  1 / max(card_a, card_b)
//
// For multi-key joins (k1 AND k2 AND ...), the per-key selectivities combine
// under the independence assumption, then O1.S3's corr correction applies if
// the catalog records correlation between the key columns (rho).
//
// leftCard/rightCard are the input row counts of the two sides (the join
// output cardinality ≈ leftCard × rightCard × joinSelectivity). Either may be
// 0 (unknown) → the estimator falls back to DefaultSelectivity per condition.
//
// Returns a multiplier in [0,1] (fraction of the cross-product that survives).
func (e *Estimator) JoinSelectivity(leftTable, rightTable string, on []ir.Expr, leftCard, rightCard int64) float64 {
	if len(on) == 0 {
		return 1.0 // cross join: no filter
	}
	var keys []joinKey
	sel := 1.0
	for _, cond := range on {
		b, ok := cond.(*ir.BinOp)
		if !ok || b.Op != token.EQL {
			sel *= DefaultSelectivity
			continue
		}
		lCol, rCol := joinOperands(b)
		if lCol == "" || rCol == "" {
			sel *= DefaultSelectivity
			continue
		}
		keys = append(keys, joinKey{lCol: lCol, rCol: rCol})
		sel *= e.eqJoinSelectivity(leftTable, rightTable, lCol, rCol)
	}
	sel = e.applyCorrCorrection(keys, leftTable, sel)
	return clamp01(sel)
}

// eqJoinSelectivity: t1.a = t2.a → 1/max(card_a, card_b). Falls back to the
// default if either column's cardinality is unknown.
func (e *Estimator) eqJoinSelectivity(leftTable, rightTable, lCol, rCol string) float64 {
	lc := e.columnStats(leftTable, lCol)
	rc := e.columnStats(rightTable, rCol)
	maxCard := int64(0)
	if lc != nil && lc.Card > maxCard {
		maxCard = lc.Card
	}
	if rc != nil && rc.Card > maxCard {
		maxCard = rc.Card
	}
	if maxCard <= 0 {
		return DefaultSelectivity
	}
	return 1.0 / float64(maxCard)
}

// joinOperands extracts the two column names from a `lCol = rCol` join
// condition. Returns ("","") if the shape doesn't match (e.g. one side is a
// literal — that's a filter, not a join key).
func joinOperands(b *ir.BinOp) (string, string) {
	lCol := columnName(b.X)
	rCol := columnName(b.Y)
	if lCol != "" && rCol != "" {
		return lCol, rCol
	}
	return "", ""
}

// OutputCardinality estimates the row count a join produces, given the two
// input cardinalities and the join-selectivity multiplier. Convenience for
// the cost model.
func (e *Estimator) OutputCardinality(leftTable, rightTable string, on []ir.Expr, leftCard, rightCard int64) int64 {
	if leftCard <= 0 || rightCard <= 0 {
		return 0
	}
	sel := e.JoinSelectivity(leftTable, rightTable, on, leftCard, rightCard)
	n := float64(leftCard) * float64(rightCard) * sel
	if n < 1 {
		return 1 // a join producing fewer than 1 row is rounded up
	}
	return int64(n)
}

// columnStats is shared with selectivity.go (same package). The O1.S3 corr
// correction reuses it to look up CorrVs.
var _ = (*stats.ColumnStats)(nil) // keep the import meaningful for the godoc
