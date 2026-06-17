package rules

import (
	"testing"

	"nzinfo/kql/internal/frontend/token"
	"nzinfo/kql/internal/ir"
)

// --- ConstantFold tests ---

// TestFold_Arithmetic: 1 + 2 → 3
func TestFold_Arithmetic(t *testing.T) {
	expr := &ir.BinOp{Op: token.ADD, X: litInt(1), Y: litInt(2)}
	got, changed := foldExpr(expr)
	if !changed {
		t.Fatal("expected fold")
	}
	l, ok := got.(*ir.Lit)
	if !ok {
		t.Fatalf("got %T, want *Lit", got)
	}
	if l.Value.(int64) != 3 {
		t.Errorf("folded value = %v, want 3", l.Value)
	}
}

// TestFold_TautologyFilter: `where 1 == 1` → filter removed.
func TestFold_TautologyFilter(t *testing.T) {
	pipe := &ir.Pipeline{
		Source: tbl("T"),
		Stages: []ir.Stage{
			&ir.Filter{Predicate: &ir.BinOp{Op: token.EQL, X: litInt(1), Y: litInt(1)}},
			&ir.Extend{Cols: []*ir.NamedExpr{named("x", c("id"))}},
		},
	}
	got, changed := ConstantFold{}.Apply(pipe, noopReader{})
	if !changed {
		t.Fatal("expected fold to remove tautology filter")
	}
	if len(got.Stages) != 1 {
		t.Errorf("stages = %d, want 1 (filter removed): %v", len(got.Stages), stageNames(got))
	}
}

// TestFold_ContradictionFilter: `where 1 == 0` → Limit 0 (empty result).
func TestFold_ContradictionFilter(t *testing.T) {
	pipe := &ir.Pipeline{
		Source: tbl("T"),
		Stages: []ir.Stage{
			&ir.Filter{Predicate: &ir.BinOp{Op: token.EQL, X: litInt(1), Y: litInt(0)}},
		},
	}
	got, changed := ConstantFold{}.Apply(pipe, noopReader{})
	if !changed {
		t.Fatal("expected fold to replace contradiction with Limit 0")
	}
	if _, ok := got.Stages[0].(*ir.Limit); !ok {
		t.Errorf("stage[0] = %T, want *Limit (Limit 0)", got.Stages[0])
	}
}

// TestFold_NestedArithmetic: 1 + 2 + 3 → 6 (fold recurses).
func TestFold_NestedArithmetic(t *testing.T) {
	// (1 + 2) + 3
	inner := &ir.BinOp{Op: token.ADD, X: litInt(1), Y: litInt(2)}
	expr := &ir.BinOp{Op: token.ADD, X: inner, Y: litInt(3)}
	got, _ := foldExpr(expr)
	l, ok := got.(*ir.Lit)
	if !ok {
		t.Fatalf("got %T, want *Lit", got)
	}
	if l.Value.(int64) != 6 {
		t.Errorf("folded = %v, want 6", l.Value)
	}
}

// TestFold_PreservesColumnRefs: `id + 2` does NOT fold (id is a column).
func TestFold_PreservesColumnRefs(t *testing.T) {
	expr := &ir.BinOp{Op: token.ADD, X: c("id"), Y: litInt(2)}
	got, changed := foldExpr(expr)
	if changed {
		t.Error("expression with a column ref should not fold")
	}
	if _, ok := got.(*ir.BinOp); !ok {
		t.Errorf("got %T, want *BinOp unchanged", got)
	}
}

// TestFold_UnaryNegation: -5 folds to -5; -(1+2) folds to -3.
func TestFold_UnaryNegation(t *testing.T) {
	expr := &ir.UnaryOp{Op: token.SUB, X: &ir.BinOp{Op: token.ADD, X: litInt(1), Y: litInt(2)}}
	got, _ := foldExpr(expr)
	l, ok := got.(*ir.Lit)
	if !ok {
		t.Fatalf("got %T, want *Lit", got)
	}
	if l.Value.(int64) != -3 {
		t.Errorf("folded = %v, want -3", l.Value)
	}
}

// TestFold_IffConstantCond: iff(true, a, b) → a.
func TestFold_IffConstantCond(t *testing.T) {
	expr := &ir.FuncCall{
		Name: "iff",
		Args: []ir.Expr{boolLit(true), c("a"), c("b")},
	}
	got, changed := foldExpr(expr)
	if !changed {
		t.Fatal("iff(true,...) should fold")
	}
	col, ok := got.(*ir.Col)
	if !ok || col.Name != "a" {
		t.Errorf("iff(true,a,b) → %v, want Col a", got)
	}
}

// --- ColumnPrune tests ---

// TestPrune_TerminalProject: `T | where id > 0 | project id, state` inserts a
// source-level project of {id, state}.
func TestPrune_TerminalProject(t *testing.T) {
	pipe := &ir.Pipeline{
		Source: tbl("T"),
		Stages: []ir.Stage{
			&ir.Filter{Predicate: gt(c("id"), litInt(0))},
			&ir.Project{Cols: []*ir.NamedExpr{
				{Expr: c("id")},
				{Expr: c("state")},
			}},
		},
	}
	got, changed := ColumnPrune{}.Apply(pipe, noopReader{})
	if !changed {
		t.Fatal("expected ColumnPrune to insert a source projection")
	}
	// First stage should now be a Project (the inserted source prune).
	if _, ok := got.Stages[0].(*ir.Project); !ok {
		t.Errorf("stage[0] = %T, want *Project (inserted prune)", got.Stages[0])
	}
}

// TestPrune_NoTerminalProject: without a terminal Project, no pruning.
func TestPrune_NoTerminalProject(t *testing.T) {
	pipe := &ir.Pipeline{
		Source: tbl("T"),
		Stages: []ir.Stage{
			&ir.Filter{Predicate: gt(c("id"), litInt(0))},
		},
	}
	_, changed := ColumnPrune{}.Apply(pipe, noopReader{})
	if changed {
		t.Error("ColumnPrune should not fire without a terminal Project")
	}
}

// TestPrune_BlockedByExtend: an Extend before the terminal Project blocks prune
// (Extend could add columns the Project uses).
func TestPrune_BlockedByExtend(t *testing.T) {
	pipe := &ir.Pipeline{
		Source: tbl("T"),
		Stages: []ir.Stage{
			&ir.Extend{Cols: []*ir.NamedExpr{named("x", c("id"))}},
			&ir.Project{Cols: []*ir.NamedExpr{{Expr: c("x")}}},
		},
	}
	_, changed := ColumnPrune{}.Apply(pipe, noopReader{})
	if changed {
		t.Error("ColumnPrune should not fire past an Extend")
	}
}

// TestPrune_ProjectWithComputed: a terminal Project with a computed column
// (not a bare ref) blocks prune.
func TestPrune_ProjectWithComputed(t *testing.T) {
	pipe := &ir.Pipeline{
		Source: tbl("T"),
		Stages: []ir.Stage{
			&ir.Project{Cols: []*ir.NamedExpr{{Name: "doubled", Expr: &ir.BinOp{Op: token.MUL, X: c("id"), Y: litInt(2)}}}},
		},
	}
	_, changed := ColumnPrune{}.Apply(pipe, noopReader{})
	if changed {
		t.Error("ColumnPrune should not fire for a computed-column Project")
	}
}

// TestPrune_FoldedAwayFilter: a tautology filter removed by ConstantFold
// doesn't break ColumnPrune (the remaining terminal Project still prunes).
func TestPrune_FoldedAwayFilter(t *testing.T) {
	pipe := &ir.Pipeline{
		Source: tbl("T"),
		Stages: []ir.Stage{
			&ir.Filter{Predicate: &ir.BinOp{Op: token.EQL, X: litInt(1), Y: litInt(1)}},
			&ir.Project{Cols: []*ir.NamedExpr{{Expr: c("id")}}},
		},
	}
	// Run ConstantFold first (removes the filter), then ColumnPrune.
	pipe, _ = ConstantFold{}.Apply(pipe, noopReader{})
	got, changed := ColumnPrune{}.Apply(pipe, noopReader{})
	if !changed {
		t.Fatal("ColumnPrune should fire after ConstantFold removed the filter")
	}
	if _, ok := got.Stages[0].(*ir.Project); !ok {
		t.Errorf("stage[0] = %T, want *Project", got.Stages[0])
	}
}
