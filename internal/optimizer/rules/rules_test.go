package rules

import (
	"testing"

	"nzinfo/kql/internal/frontend/token"
	"nzinfo/kql/internal/ir"
)

// helpers to build small IR trees for rule tests.
func tbl(name string) *ir.SourceTable { return &ir.SourceTable{Table: name} }
func c(name string) *ir.Col           { return &ir.Col{Name: name} }
func litInt(v int64) *ir.Lit          { return &ir.Lit{T: ir.TypeLong, Value: v, HasValue: true} }
func gt(x, y ir.Expr) *ir.BinOp       { return &ir.BinOp{Op: token.GTR, X: x, Y: y} }
func named(name string, e ir.Expr) *ir.NamedExpr { return &ir.NamedExpr{Name: name, Expr: e} }

// stageNames returns the stage type names (for asserting order).
func stageNames(p *ir.Pipeline) []string {
	out := make([]string, len(p.Stages))
	for i, s := range p.Stages {
		switch s.(type) {
		case *ir.Filter:
			out[i] = "Filter"
		case *ir.Extend:
			out[i] = "Extend"
		case *ir.Project:
			out[i] = "Project"
		case *ir.Aggregate:
			out[i] = "Aggregate"
		case *ir.Limit:
			out[i] = "Limit"
		default:
			out[i] = "?"
		}
	}
	return out
}

// TestPushdown_PastExtend: `T | extend x = ... | where id > 0` → filter moves
// before the extend (the predicate `id > 0` doesn't reference the added col x).
func TestPushdown_PastExtend(t *testing.T) {
	pipe := &ir.Pipeline{
		Source: tbl("T"),
		Stages: []ir.Stage{
			&ir.Extend{Cols: []*ir.NamedExpr{named("x", c("id"))}},
			&ir.Filter{Predicate: gt(c("id"), litInt(0))},
		},
	}
	got, changed := PredicatePushdown{}.Apply(pipe, noopReader{})
	if !changed {
		t.Fatal("expected pushdown to change the pipeline")
	}
	want := []string{"Filter", "Extend"}
	if names := stageNames(got); !equal(names, want) {
		t.Errorf("stage order = %v, want %v (filter should be before extend)", names, want)
	}
}

// TestPushdown_BlockedByExtendColumn: `T | extend x = ... | where x > 0` →
// filter must NOT move (it references the extend-added column x).
func TestPushdown_BlockedByExtendColumn(t *testing.T) {
	pipe := &ir.Pipeline{
		Source: tbl("T"),
		Stages: []ir.Stage{
			&ir.Extend{Cols: []*ir.NamedExpr{named("x", c("id"))}},
			&ir.Filter{Predicate: gt(c("x"), litInt(0))},
		},
	}
	got, changed := PredicatePushdown{}.Apply(pipe, noopReader{})
	if changed {
		t.Errorf("filter referencing extend-col should NOT push; got order %v", stageNames(got))
	}
}

// TestPushdown_PastProjectPassthrough: project of bare cols, filter on those
// cols → filter pushes past.
func TestPushdown_PastProjectPassthrough(t *testing.T) {
	pipe := &ir.Pipeline{
		Source: tbl("T"),
		Stages: []ir.Stage{
			&ir.Project{Cols: []*ir.NamedExpr{
				{Expr: c("id")},
				{Expr: c("state")},
			}},
			&ir.Filter{Predicate: gt(c("id"), litInt(0))},
		},
	}
	got, changed := PredicatePushdown{}.Apply(pipe, noopReader{})
	if !changed {
		t.Fatal("filter should push past passthrough project")
	}
	want := []string{"Filter", "Project"}
	if names := stageNames(got); !equal(names, want) {
		t.Errorf("order = %v, want %v", names, want)
	}
}

// TestPushdown_BlockedByProjectRename: project renames a column → filter on the
// new name must NOT push past (the name doesn't exist before project).
func TestPushdown_BlockedByProjectRename(t *testing.T) {
	pipe := &ir.Pipeline{
		Source: tbl("T"),
		Stages: []ir.Stage{
			&ir.Project{Cols: []*ir.NamedExpr{named("s", c("state"))}},
			&ir.Filter{Predicate: gt(c("s"), litInt(0))},
		},
	}
	got, changed := PredicatePushdown{}.Apply(pipe, noopReader{})
	if changed {
		t.Errorf("filter on renamed col should NOT push; got %v", stageNames(got))
	}
}

// TestPushdown_BlockedByAggregate: summarize is an impassable barrier.
func TestPushdown_BlockedByAggregate(t *testing.T) {
	pipe := &ir.Pipeline{
		Source: tbl("T"),
		Stages: []ir.Stage{
			&ir.Aggregate{
				Keys:       []*ir.NamedExpr{named("state", c("state"))},
				Aggregates: []*ir.NamedExpr{named("total", &ir.FuncCall{Name: "sum", Args: []ir.Expr{c("id")}})},
			},
			&ir.Filter{Predicate: gt(c("total"), litInt(0))},
		},
	}
	got, changed := PredicatePushdown{}.Apply(pipe, noopReader{})
	if changed {
		t.Errorf("filter should NOT cross aggregate; got %v", stageNames(got))
	}
}

// TestPushdown_MultipleStages: a filter pushes past multiple extend stages.
func TestPushdown_MultipleStages(t *testing.T) {
	pipe := &ir.Pipeline{
		Source: tbl("T"),
		Stages: []ir.Stage{
			&ir.Extend{Cols: []*ir.NamedExpr{named("x", c("id"))}},
			&ir.Extend{Cols: []*ir.NamedExpr{named("y", c("id"))}},
			&ir.Filter{Predicate: gt(c("id"), litInt(0))},
		},
	}
	got, changed := PredicatePushdown{}.Apply(pipe, noopReader{})
	if !changed {
		t.Fatal("filter should push past both extends")
	}
	want := []string{"Filter", "Extend", "Extend"}
	if names := stageNames(got); !equal(names, want) {
		t.Errorf("order = %v, want %v", names, want)
	}
}

// TestPushdown_NoOpWhenAlreadyAtSource: filter already first → no change.
func TestPushdown_NoOpWhenAlreadyAtSource(t *testing.T) {
	pipe := &ir.Pipeline{
		Source: tbl("T"),
		Stages: []ir.Stage{
			&ir.Filter{Predicate: gt(c("id"), litInt(0))},
			&ir.Extend{Cols: []*ir.NamedExpr{named("x", c("id"))}},
		},
	}
	_, changed := PredicatePushdown{}.Apply(pipe, noopReader{})
	if changed {
		t.Error("filter already at source should not change")
	}
}

// TestEngine_Fixpoint: the engine runs to fixpoint (one PredicatePushdown pass
// suffices here; verify totalChanges and termination).
func TestEngine_Fixpoint(t *testing.T) {
	pipe := &ir.Pipeline{
		Source: tbl("T"),
		Stages: []ir.Stage{
			&ir.Extend{Cols: []*ir.NamedExpr{named("x", c("id"))}},
			&ir.Filter{Predicate: gt(c("id"), litInt(0))},
		},
	}
	e := NewEngine(PredicatePushdown{})
	got, changes := e.Optimize(pipe)
	if changes == 0 {
		t.Error("expected at least one change")
	}
	want := []string{"Filter", "Extend"}
	if names := stageNames(got); !equal(names, want) {
		t.Errorf("after engine: order = %v, want %v", names, want)
	}
}

// TestEngine_MultipleRules: engine with a no-op rule still reaches fixpoint.
func TestEngine_MultipleRules(t *testing.T) {
	pipe := &ir.Pipeline{
		Source: tbl("T"),
		Stages: []ir.Stage{
			&ir.Extend{Cols: []*ir.NamedExpr{named("x", c("id"))}},
			&ir.Filter{Predicate: gt(c("id"), litInt(0))},
		},
	}
	e := NewEngine(noopRule{}, PredicatePushdown{}, noopRule{})
	got, changes := e.Optimize(pipe)
	if changes == 0 {
		t.Error("expected changes from PredicatePushdown")
	}
	if names := stageNames(got); !equal(names, []string{"Filter", "Extend"}) {
		t.Errorf("order = %v", names)
	}
}

// TestEngine_MaxIterGuard: an oscillating rule pair respects the maxIter cap.
type swapRule struct{ name string }

func (s swapRule) Name() string                      { return s.name }
func (s swapRule) Apply(p *ir.Pipeline, _ StatsReader) (*ir.Pipeline, bool) {
	if len(p.Stages) >= 2 {
		p.Stages[0], p.Stages[1] = p.Stages[1], p.Stages[0]
		return p, true // always "changes" → oscillates
	}
	return p, false
}

func TestEngine_MaxIterGuard(t *testing.T) {
	pipe := &ir.Pipeline{
		Source: tbl("T"),
		Stages: []ir.Stage{
			&ir.Filter{Predicate: c("a")},
			&ir.Extend{Cols: []*ir.NamedExpr{named("x", c("a"))}},
		},
	}
	e := NewEngine(swapRule{name: "Swap"}).WithMaxIter(5)
	_, changes := e.Optimize(pipe)
	// Should have run at least one pass but stopped at maxIter.
	if changes == 0 {
		t.Error("oscillating rule should report changes")
	}
}

// noopRule never changes anything.
type noopRule struct{}

func (noopRule) Name() string                                  { return "Noop" }
func (noopRule) Apply(p *ir.Pipeline, _ StatsReader) (*ir.Pipeline, bool) { return p, false }

// TestCatalogStatsReader: selectivity estimate from a catalog.
func TestCatalogStatsReader(t *testing.T) {
	// Build via the real stats package through CatalogStatsReader.
	pipe := &ir.Pipeline{Source: tbl("T")}
	_ = pipe
	// CatalogStatsReader(nil) → noopReader (returns 0).
	r := CatalogStatsReader(nil)
	if r.Selectivity("T", "c") != 0 {
		t.Error("nil catalog should give 0 selectivity")
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
