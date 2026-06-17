// Package pg — join hint emission tests (O4.S4).
//
// These tests construct an *ir.Pipeline with a *ir.Join whose Hint is set
// (simulating the optimizer's JoinPlan.Apply) and verify the emitted SQL
// includes the correct pg_hint_plan comment. They complement the golden
// snapshots (which cover the zero-Hint = no-comment path).
package pg

import (
	"strings"
	"testing"

	"nzinfo/kql/internal/frontend/token"
	"nzinfo/kql/internal/ir"
)

// hintedJoinPipe builds a minimal pipeline: events ⋈ meta on id=id, with the
// given join hint stamped onto the Join stage.
func hintedJoinPipe(hint ir.JoinHint) *ir.Pipeline {
	return &ir.Pipeline{
		Source: &ir.SourceTable{Table: "events"},
		Stages: []ir.Stage{
			&ir.Join{
				Kind: ir.JoinInner,
				Hint: hint,
				Right: &ir.Pipeline{Source: &ir.SourceTable{Table: "meta"}},
				On: []ir.Expr{
					&ir.BinOp{Op: token.EQL, X: &ir.Col{Name: "id"}, Y: &ir.Col{Name: "id"}},
				},
			},
		},
	}
}

// TestJoinHint_Hash: a Hash hint emits /*+ HashJoin(...) */.
func TestJoinHint_Hash(t *testing.T) {
	q, err := Emit(hintedJoinPipe(ir.JoinHintHash))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(q.SQL, "/*+ HashJoin(") {
		t.Errorf("expected /*+ HashJoin(...) in SQL, got:\n%s", q.SQL)
	}
}

// TestJoinHint_NestLoop: a NestLoop hint emits /*+ NestLoop(...) */.
func TestJoinHint_NestLoop(t *testing.T) {
	q, err := Emit(hintedJoinPipe(ir.JoinHintNestLoop))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(q.SQL, "/*+ NestLoop(") {
		t.Errorf("expected /*+ NestLoop(...) in SQL, got:\n%s", q.SQL)
	}
}

// TestJoinHint_Merge: a Merge hint emits /*+ MergeJoin(...) */.
func TestJoinHint_Merge(t *testing.T) {
	q, err := Emit(hintedJoinPipe(ir.JoinHintMerge))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(q.SQL, "/*+ MergeJoin(") {
		t.Errorf("expected /*+ MergeJoin(...) in SQL, got:\n%s", q.SQL)
	}
}

// TestJoinHint_None: no hint (JoinHintNone) emits NO comment (the no-regression
// path — current behaviour preserved exactly).
func TestJoinHint_None(t *testing.T) {
	q, err := Emit(hintedJoinPipe(ir.JoinHintNone))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(q.SQL, "/*+") {
		t.Errorf("expected NO hint comment for JoinHintNone, got:\n%s", q.SQL)
	}
}

// TestJoinHint_IndexLookup: IndexLookup is structural (deferred emit) — no hint
// comment is emitted (the IN-list rewrite is a future PostProc path).
func TestJoinHint_IndexLookup(t *testing.T) {
	q, err := Emit(hintedJoinPipe(ir.JoinHintIndexLookup))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(q.SQL, "/*+") {
		t.Errorf("expected NO hint for IndexLookup (deferred), got:\n%s", q.SQL)
	}
}

// TestJoinHintCTE_Hash: the CTE emit path also emits the hint.
func TestJoinHintCTE_Hash(t *testing.T) {
	q, err := EmitCTE(hintedJoinPipe(ir.JoinHintHash))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(q.SQL, "/*+ HashJoin(") {
		t.Errorf("CTE emit expected /*+ HashJoin(...) in SQL, got:\n%s", q.SQL)
	}
}

// TestJoinHintCTE_None: CTE path with no hint emits no comment.
func TestJoinHintCTE_None(t *testing.T) {
	q, err := EmitCTE(hintedJoinPipe(ir.JoinHintNone))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(q.SQL, "/*+") {
		t.Errorf("CTE emit expected NO hint for JoinHintNone, got:\n%s", q.SQL)
	}
}
