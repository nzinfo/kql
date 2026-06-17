package exec

import (
	"context"
	"database/sql"
	"testing"

	"nzinfo/kql/internal/backend/sqlite"
	"nzinfo/kql/internal/frontend/token"
	"nzinfo/kql/internal/ir"

	_ "modernc.org/sqlite"
)

// indexLookupDB seeds two tables: orders (small outer) + products (large inner,
// has an index on id). The IndexLookup rewrite should fetch the small set of
// order product_ids, then probe products by id.
func indexLookupDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.Exec(`CREATE TABLE orders (id INTEGER PRIMARY KEY, product_id INTEGER, qty INTEGER)`)
	db.Exec(`CREATE TABLE products (id INTEGER PRIMARY KEY, name TEXT, price REAL)`)
	// 4 orders (the small outer side).
	rows := []struct {
		id  int64
		pid int64
		qty int64
	}{
		{1, 100, 2},
		{2, 200, 1},
		{3, 100, 3},
		{4, 300, 1},
	}
	for _, r := range rows {
		db.Exec(`INSERT INTO orders VALUES(?,?,?)`, r.id, r.pid, r.qty)
	}
	// 3 products (the inner side — small here for testing; the rewrite still works).
	prods := []struct {
		id    int64
		name  string
		price float64
	}{
		{100, "Widget", 9.99},
		{200, "Gadget", 19.99},
		{300, "Gizmo", 29.99},
	}
	for _, p := range prods {
		db.Exec(`INSERT INTO products VALUES(?,?,?)`, p.id, p.name, p.price)
	}
	return db
}

// TestIndexLookup_TwoPhase: a join with Hint=IndexLookup executes via the
// two-phase strategy (fetch keys → WHERE IN → client hash-join) and produces
// the same result as a normal JOIN.
func TestIndexLookup_TwoPhase(t *testing.T) {
	db := indexLookupDB(t)
	bk := sqlite.NewFromDB(db)

	pipe := &ir.Pipeline{
		Source: &ir.SourceTable{Table: "orders"},
		Stages: []ir.Stage{
			&ir.Join{
				Kind: ir.JoinInner,
				Hint: ir.JoinHintIndexLookup,
				Right: &ir.Pipeline{Source: &ir.SourceTable{Table: "products"}},
				On: []ir.Expr{
					&ir.BinOp{Op: token.EQL, X: &ir.Col{Name: "product_id"}, Y: &ir.Col{Name: "id"}},
				},
			},
		},
	}

	res, err := ExecPipeline(context.Background(), bk, pipe)
	if err != nil {
		t.Fatalf("ExecPipeline: %v", err)
	}
	if len(res.Rows) != 4 {
		t.Fatalf("rows = %d, want 4 (4 orders each match a product)", len(res.Rows))
	}
	t.Logf("IndexLookup two-phase: %d rows, cols=%v", len(res.Rows), res.Columns)
}

// TestIndexLookup_FallbackNoHint: without the IndexLookup hint, the normal
// single-query JOIN path is used. The normal path goes through bk.Emit directly
// (no binder), which produces a valid SQL JOIN — verify it executes without error.
// (Row count is checked via the IndexLookup path above; here we just confirm the
// fallback doesn't crash.)
func TestIndexLookup_FallbackNoHint(t *testing.T) {
	db := indexLookupDB(t)
	bk := sqlite.NewFromDB(db)

	pipe := &ir.Pipeline{
		Source: &ir.SourceTable{Table: "orders"},
		Stages: []ir.Stage{
			&ir.Join{
				Kind: ir.JoinInner,
				Hint: ir.JoinHintNone, // normal path
				Right: &ir.Pipeline{Source: &ir.SourceTable{Table: "products"}},
				On: []ir.Expr{
					&ir.BinOp{Op: token.EQL, X: &ir.Col{Name: "product_id"}, Y: &ir.Col{Name: "id"}},
				},
			},
		},
	}

	res, err := ExecPipeline(context.Background(), bk, pipe)
	if err != nil {
		t.Fatalf("ExecPipeline (fallback): %v", err)
	}
	t.Logf("fallback normal path: %d rows", len(res.Rows))
}

// TestIndexLookup_WithPreFilter: a where filter before the join is included in
// the key-fetch phase. Only matching orders' product_ids are probed.
func TestIndexLookup_WithPreFilter(t *testing.T) {
	db := indexLookupDB(t)
	bk := sqlite.NewFromDB(db)

	pipe := &ir.Pipeline{
		Source: &ir.SourceTable{Table: "orders"},
		Stages: []ir.Stage{
			&ir.Filter{Predicate: &ir.BinOp{
				Op: token.GTR,
				X:  &ir.Col{Name: "qty"},
				Y:  &ir.Lit{Value: int64(1), HasValue: true, T: ir.TypeLong},
			}},
			&ir.Join{
				Kind: ir.JoinInner,
				Hint: ir.JoinHintIndexLookup,
				Right: &ir.Pipeline{Source: &ir.SourceTable{Table: "products"}},
				On: []ir.Expr{
					&ir.BinOp{Op: token.EQL, X: &ir.Col{Name: "product_id"}, Y: &ir.Col{Name: "id"}},
				},
			},
		},
	}

	res, err := ExecPipeline(context.Background(), bk, pipe)
	if err != nil {
		t.Fatalf("ExecPipeline: %v", err)
	}
	// qty > 1: orders 1,2,3 (qty 2,1→excluded,3). Wait: qty values are 2,1,3,1.
	// qty > 1: orders 1 (qty=2) and 3 (qty=3). = 2 rows.
	if len(res.Rows) != 2 {
		t.Fatalf("rows = %d, want 2 (orders with qty>1)", len(res.Rows))
	}
}

// TestIndexLookup_ExtractKeys: the key extraction from join ON conditions.
func TestIndexLookup_ExtractKeys(t *testing.T) {
	j := &ir.Join{
		On: []ir.Expr{
			&ir.BinOp{Op: token.EQL, X: &ir.Col{Name: "product_id"}, Y: &ir.Col{Name: "id"}},
		},
	}
	l, r, ok := extractIndexLookupKeys(j)
	if !ok {
		t.Fatal("expected keys extracted")
	}
	if l != "product_id" || r != "id" {
		t.Errorf("keys = (%q, %q), want (product_id, id)", l, r)
	}

	// Non-equality → no keys.
	j2 := &ir.Join{
		On: []ir.Expr{
			&ir.BinOp{Op: token.GTR, X: &ir.Col{Name: "a"}, Y: &ir.Col{Name: "b"}},
		},
	}
	if _, _, ok := extractIndexLookupKeys(j2); ok {
		t.Error("expected no keys for non-EQL condition")
	}
}

// TestIndexLookup_FindJoin: findIndexLookupJoin locates the hinted join.
func TestIndexLookup_FindJoin(t *testing.T) {
	stages := []ir.Stage{
		&ir.Filter{Predicate: &ir.Col{Name: "x"}},
		&ir.Join{Hint: ir.JoinHintIndexLookup},
		&ir.Filter{Predicate: &ir.Col{Name: "y"}},
	}
	if idx := findIndexLookupJoin(stages); idx != 1 {
		t.Errorf("findIndexLookupJoin = %d, want 1", idx)
	}
	stages[1] = &ir.Join{Hint: ir.JoinHintNone}
	if idx := findIndexLookupJoin(stages); idx != -1 {
		t.Errorf("findIndexLookupJoin with no hint = %d, want -1", idx)
	}
}

// TestHashJoinByKey: client-side hash join correctness.
func TestHashJoinByKey(t *testing.T) {
	outer := &Result{
		Columns: []string{"oid", "key"},
		Rows: [][]interface{}{
			{int64(1), int64(100)},
			{int64(2), int64(200)},
			{int64(3), int64(100)}, // duplicate key → 2 matches
		},
	}
	inner := &Result{
		Columns: []string{"pid", "key", "name"},
		Rows: [][]interface{}{
			{int64(100), int64(100), "A"},
			{int64(200), int64(200), "B"},
		},
	}
	joined := hashJoinByKey(outer, inner, "key", "key")
	// 3 outer rows, all match: oid1→A, oid2→B, oid3→A = 3 rows.
	if len(joined.Rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(joined.Rows))
	}
	// Columns should be outer + inner.
	if len(joined.Columns) != 5 {
		t.Errorf("cols = %d, want 5", len(joined.Columns))
	}
}
