package rules

import (
	"testing"

	"nzinfo/kql/internal/frontend/token"
	"nzinfo/kql/internal/ir"
	"nzinfo/kql/internal/optimizer/stats"
)

// SaturationRewrite (O6.S4) tests: distinct elimination + not-null fold.

// satCatalog: table "users" with 100 rows; "id" is unique (Card==100, Nulls==0);
// "email" is non-unique (Card==90); "name" is nullable (Nulls==5).
func satCatalog() *stats.Catalog {
	return &stats.Catalog{
		Tables: map[string]*stats.Table{
			"users": {
				RowCount: 100, AvgRowBytes: 50,
				Columns: map[string]*stats.ColumnStats{
					"id":    {Card: 100, Nulls: 0}, // unique + not null
					"email": {Card: 90, Nulls: 0},  // not null, not unique
					"name":  {Card: 95, Nulls: 5},  // nullable
				},
			},
		},
	}
}

// TestSaturation_DistinctOnUniqueEliminated: distinct id (unique) → dropped.
func TestSaturation_DistinctOnUniqueEliminated(t *testing.T) {
	cat := satCatalog()
	sr := NewSaturationReader(cat)
	r := SaturationRewrite{}
	pipe := &ir.Pipeline{
		Source: &ir.SourceTable{Table: "users"},
		Stages: []ir.Stage{
			&ir.Distinct{Cols: []ir.Expr{&ir.Col{Name: "id"}}},
		},
	}
	out, changed := r.Apply(pipe, sr)
	if !changed {
		t.Fatal("distinct on unique: changed=false, want true")
	}
	if len(out.Stages) != 0 {
		t.Errorf("distinct on unique: %d stages remain, want 0 (eliminated)", len(out.Stages))
	}
}

// TestSaturation_DistinctOnNonUniqueKept: distinct email (Card 90 < 100) → kept.
func TestSaturation_DistinctOnNonUniqueKept(t *testing.T) {
	cat := satCatalog()
	sr := NewSaturationReader(cat)
	r := SaturationRewrite{}
	pipe := &ir.Pipeline{
		Source: &ir.SourceTable{Table: "users"},
		Stages: []ir.Stage{
			&ir.Distinct{Cols: []ir.Expr{&ir.Col{Name: "email"}}},
		},
	}
	out, changed := r.Apply(pipe, sr)
	if changed {
		t.Errorf("distinct on non-unique email: changed=true, want false (kept)")
	}
	if len(out.Stages) != 1 {
		t.Errorf("distinct on email: %d stages, want 1 (kept)", len(out.Stages))
	}
}

// TestSaturation_IsNotNullTautologyDropped: isnotnull(id) on NOT NULL col → dropped.
func TestSaturation_IsNotNullTautologyDropped(t *testing.T) {
	cat := satCatalog()
	sr := NewSaturationReader(cat)
	r := SaturationRewrite{}
	pipe := &ir.Pipeline{
		Source: &ir.SourceTable{Table: "users"},
		Stages: []ir.Stage{
			&ir.Filter{Predicate: &ir.FuncCall{Name: "isnotnull", Args: []ir.Expr{&ir.Col{Name: "id"}}}},
		},
	}
	out, changed := r.Apply(pipe, sr)
	if !changed {
		t.Fatal("isnotnull(id): changed=false, want true")
	}
	if len(out.Stages) != 0 {
		t.Errorf("isnotnull(id): %d stages, want 0 (tautology dropped)", len(out.Stages))
	}
}

// TestSaturation_IsNullFoldsFalse: isnull(id) on NOT NULL col → folds to false.
func TestSaturation_IsNullFoldsFalse(t *testing.T) {
	cat := satCatalog()
	sr := NewSaturationReader(cat)
	r := SaturationRewrite{}
	pipe := &ir.Pipeline{
		Source: &ir.SourceTable{Table: "users"},
		Stages: []ir.Stage{
			&ir.Filter{Predicate: &ir.FuncCall{Name: "isnull", Args: []ir.Expr{&ir.Col{Name: "id"}}}},
		},
	}
	out, changed := r.Apply(pipe, sr)
	if !changed {
		t.Fatal("isnull(id): changed=false, want true")
	}
	if len(out.Stages) != 1 {
		t.Fatalf("isnull(id): %d stages, want 1 (folded to false, kept)", len(out.Stages))
	}
	f, ok := out.Stages[0].(*ir.Filter)
	if !ok {
		t.Fatalf("stage type = %T, want *Filter", out.Stages[0])
	}
	lit, ok := f.Predicate.(*ir.Lit)
	if !ok || lit.Value != false {
		t.Errorf("isnull(id) folded to %v, want false", f.Predicate)
	}
}

// TestSaturation_NullableColumnKept: isnotnull(name) on nullable col → kept.
func TestSaturation_NullableColumnKept(t *testing.T) {
	cat := satCatalog()
	sr := NewSaturationReader(cat)
	r := SaturationRewrite{}
	pipe := &ir.Pipeline{
		Source: &ir.SourceTable{Table: "users"},
		Stages: []ir.Stage{
			&ir.Filter{Predicate: &ir.FuncCall{Name: "isnotnull", Args: []ir.Expr{&ir.Col{Name: "name"}}}},
		},
	}
	_, changed := r.Apply(pipe, sr)
	if changed {
		t.Error("isnotnull(name) on nullable: changed=true, want false (kept)")
	}
}

// TestSaturation_NoStatsNoOp: nil catalog → no-op (safe).
func TestSaturation_NoStatsNoOp(t *testing.T) {
	r := SaturationRewrite{}
	pipe := &ir.Pipeline{
		Source: &ir.SourceTable{Table: "users"},
		Stages: []ir.Stage{
			&ir.Distinct{Cols: []ir.Expr{&ir.Col{Name: "id"}}},
		},
	}
	_, changed := r.Apply(pipe, noopReader{})
	if changed {
		t.Error("nil stats: changed=true, want false (no-op)")
	}
}

// ensure imports used
var _ = token.NoPos
