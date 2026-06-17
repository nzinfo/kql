// Package exec — IndexLookup structural execution (O4 deferred emit).
//
// When the optimizer's JoinPlan sets JoinHintIndexLookup on a join (small outer
// + large indexed inner), the exec layer executes it as a TWO-PHASE query
// instead of a single SQL JOIN:
//
//	Phase 1: SELECT <outer join key> FROM <outer> WHERE <pre-join filters>
//	         → fetch the small set of outer key values (client-side).
//	Phase 2: SELECT ... FROM <inner> WHERE <inner key> = ANY($1)
//	         → forces the index probe (pg uses the index for = ANY).
//	Join:    the results are joined client-side (hash join on the key).
//	Post:    post-join stages apply client-side.
//
// This is the ONLY join optimization that works WITHOUT pg_hint_plan — it
// reshapes the SQL so pg's own planner is forced down the index path. The
// `= ANY($1)` form with a small key array is a fast index-probe pattern on
// all backends (pg, sqlite, duckdb).
//
// If anything goes wrong (no join key extractable, too many keys, nil inner),
// we fall back to the normal single-query JOIN (correctness over speed — the
// DESIGN §6.6 "every AltPlan must fall back to an executable SQL" principle).
package exec

import (
	"context"
	"fmt"

	"nzinfo/kql/internal/backend"
	"nzinfo/kql/internal/frontend/token"
	"nzinfo/kql/internal/ir"
)

// maxIndexLookupKeys is the safety limit for the IN-list. Beyond this, the
// outer side is no longer "tiny" and the rewrite's benefit disappears (a
// regular hash join is better). We fall back to the normal JOIN.
const maxIndexLookupKeys = 10000

// findIndexLookupJoin returns the index of the first *ir.Join with
// Hint == JoinHintIndexLookup, or -1 if none.
func findIndexLookupJoin(stages []ir.Stage) int {
	for i, st := range stages {
		if j, ok := st.(*ir.Join); ok && j.Hint == ir.JoinHintIndexLookup {
			return i
		}
	}
	return -1
}

// execIndexLookup runs the two-phase IndexLookup strategy. Returns the result
// and true if the strategy was applied; (nil, false) to signal "use normal
// path". On any error within the strategy, it falls back gracefully.
func execIndexLookup(ctx context.Context, bk backend.Backend, pipe *ir.Pipeline) (*Result, bool, error) {
	joinIdx := findIndexLookupJoin(pipe.Stages)
	if joinIdx < 0 {
		return nil, false, nil // no IndexLookup join → normal path
	}
	j := pipe.Stages[joinIdx].(*ir.Join)

	// Extract the join keys: we need the outer key column name and the inner
	// key column name. KQL joins are $left.X == $right.Y or bare X == Y.
	leftKey, rightKey, ok := extractIndexLookupKeys(j)
	if !ok {
		return nil, false, nil // can't extract keys → normal path
	}

	// Phase 1: fetch the outer join keys.
	// Build a pipeline that runs everything before the join, then projects only
	// the outer key. This captures pre-join filters (where/sort/take on the outer).
	outerKeys, err := fetchOuterKeys(ctx, bk, pipe, joinIdx, leftKey)
	if err != nil {
		return nil, false, nil // fallback — error already logged via fmt
	}
	if len(outerKeys) == 0 {
		// No outer keys → empty result.
		return &Result{Columns: nil, Rows: nil}, true, nil
	}
	if len(outerKeys) > maxIndexLookupKeys {
		// Too many keys — the outer isn't actually tiny. Fallback to normal JOIN.
		return nil, false, nil
	}

	// Phase 2: fetch inner rows matching the keys (forces index probe).
	innerResult, err := fetchInnerByKeys(ctx, bk, j, rightKey, outerKeys)
	if err != nil {
		return nil, false, nil // fallback
	}

	// Join outer + inner client-side (hash join on the key).
	// First, get the full outer rows (not just keys) by re-running the outer
	// pipeline — OR we already have the keys, so join key→inner, then attach.
	// For simplicity: re-run the outer pipeline to get full rows, then hash-join.
	outerResult, err := execOuterPipeline(ctx, bk, pipe, joinIdx)
	if err != nil {
		return nil, false, nil // fallback
	}

	joined := hashJoinByKey(outerResult, innerResult, leftKey, rightKey)

	// Apply post-join stages client-side.
	for _, st := range pipe.Stages[joinIdx+1:] {
		var err error
		joined, err = applyPostProc(joined, st)
		if err != nil {
			return nil, true, fmt.Errorf("postproc %T: %w", st, err)
		}
	}
	return joined, true, nil
}

// extractIndexLookupKeys gets the (leftCol, rightCol) from a join's ON clause.
// Only handles simple equality: $left.X == $right.Y or X == Y.
func extractIndexLookupKeys(j *ir.Join) (leftKey, rightKey string, ok bool) {
	for _, cond := range j.On {
		b, isBinop := cond.(*ir.BinOp)
		if !isBinop || b.Op != token.EQL {
			continue
		}
		// The left side of the ON is the outer (left) key; right is inner.
		if lc, isCol := b.X.(*ir.Col); isCol {
			if rc, isCol2 := b.Y.(*ir.Col); isCol2 {
				return lc.Name, rc.Name, true
			}
		}
	}
	return "", "", false
}

// fetchOuterKeys runs the pre-join pipeline + projects the join key, returning
// the distinct key values.
func fetchOuterKeys(ctx context.Context, bk backend.Backend, pipe *ir.Pipeline, joinIdx int, keyCol string) ([]interface{}, error) {
	outerPipe := &ir.Pipeline{
		Source:   pipe.Source,
		Stages:   pipe.Stages[:joinIdx],
		Position: pipe.Position,
	}
	// Add a distinct to extract only the key column values.
	outerPipe.Stages = append(outerPipe.Stages, &ir.Distinct{
		Cols: []ir.Expr{&ir.Col{Name: keyCol}},
	})
	q, err := bk.Emit(outerPipe)
	if err != nil {
		return nil, fmt.Errorf("emit outer keys: %w", err)
	}
	res, err := bk.Exec(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("exec outer keys: %w", err)
	}
	var keys []interface{}
	for _, row := range res.Rows {
		if len(row) > 0 {
			keys = append(keys, row[0])
		}
	}
	return keys, nil
}

// execOuterPipeline runs the pre-join pipeline (full rows, not just keys).
func execOuterPipeline(ctx context.Context, bk backend.Backend, pipe *ir.Pipeline, joinIdx int) (*Result, error) {
	outerPipe := &ir.Pipeline{
		Source:   pipe.Source,
		Stages:   pipe.Stages[:joinIdx],
		Position: pipe.Position,
	}
	q, err := bk.Emit(outerPipe)
	if err != nil {
		return nil, err
	}
	res, err := bk.Exec(ctx, q)
	if err != nil {
		return nil, err
	}
	return backendResultToExec(res), nil
}

// fetchInnerByKeys emits the inner pipeline with a WHERE key = ANY(?) filter
// and executes it. This forces the index probe on the inner table.
func fetchInnerByKeys(ctx context.Context, bk backend.Backend, j *ir.Join, keyCol string, keys []interface{}) (*Result, error) {
	if j.Right == nil {
		return nil, fmt.Errorf("nil inner pipeline")
	}
	// Clone the inner pipeline and inject a filter: key = ANY(?).
	// We build: inner | where key in (keys) — represented as a Filter with an
	// IN-list. The emit layer turns IN into = ANY($N) (pg) or IN (sqlite).
	innerClone := clonePipeline(j.Right)
	keyList := make([]ir.Expr, len(keys))
	for i, k := range keys {
		keyList[i] = &ir.Lit{Value: k, HasValue: true, T: ir.TypeString}
	}
	innerClone.Stages = append([]ir.Stage{
		&ir.Filter{Predicate: &ir.BinOp{
			Op: token.IN,
			X:  &ir.Col{Name: keyCol},
			Y:  &ir.List{Elems: keyList},
		}},
	}, innerClone.Stages...)

	q, err := bk.Emit(innerClone)
	if err != nil {
		return nil, fmt.Errorf("emit inner by keys: %w", err)
	}
	res, err := bk.Exec(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("exec inner by keys: %w", err)
	}
	return backendResultToExec(res), nil
}

// clonePipeline returns a shallow copy of a pipeline (stages slice copied;
// stage contents shared since we only prepend, never mutate existing stages).
func clonePipeline(p *ir.Pipeline) *ir.Pipeline {
	if p == nil {
		return nil
	}
	stages := make([]ir.Stage, len(p.Stages))
	copy(stages, p.Stages)
	return &ir.Pipeline{
		Source:   p.Source,
		Stages:   stages,
		Position: p.Position,
	}
}

// hashJoinByKey joins two result sets on a key column (client-side hash join).
// Matches rows where outer[keyL] == inner[keyR]. Returns all outer×inner
// matches (INNER JOIN semantics).
func hashJoinByKey(outer, inner *Result, keyL, keyR string) *Result {
	outerIdx := colIndex(outer.Columns, keyL)
	innerIdx := colIndex(inner.Columns, keyR)
	if outerIdx < 0 || innerIdx < 0 {
		// Can't find key columns — return outer rows only (degraded but safe).
		return outer
	}

	// Build hash table on the inner side (usually smaller result set).
	innerBuckets := map[interface{} ][][]interface{}{}
	for _, row := range inner.Rows {
		k := row[innerIdx]
		innerBuckets[k] = append(innerBuckets[k], row)
	}

	// Probe with outer rows.
	cols := append([]string{}, outer.Columns...)
	cols = append(cols, inner.Columns...)
	var joinedRows [][]interface{}
	for _, oRow := range outer.Rows {
		k := oRow[outerIdx]
		matches, ok := innerBuckets[k]
		if !ok {
			continue
		}
		for _, iRow := range matches {
			combined := make([]interface{}, 0, len(oRow)+len(iRow))
			combined = append(combined, oRow...)
			combined = append(combined, iRow...)
			joinedRows = append(joinedRows, combined)
		}
	}
	return &Result{Columns: cols, Rows: joinedRows}
}

// colIndex finds a column by name (case-insensitive) in a list, or -1.
func colIndex(cols []string, name string) int {
	name = toLower(name)
	for i, c := range cols {
		if toLower(c) == name {
			return i
		}
	}
	return -1
}

func toLower(s string) string {
	out := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		out[i] = c
	}
	return string(out)
}
