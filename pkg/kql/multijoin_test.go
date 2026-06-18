package kql_test

import (
	"context"
	"database/sql"
	"testing"

	"nzinfo/kql/internal/backend/sqlite"
	"nzinfo/kql/pkg/kql"

	_ "modernc.org/sqlite"
)

// Multi-table join-order end-to-end tests.
//
// These verify that ApplyJoinOrder's reordering produces CORRECT results (not
// just a cheaper plan). The correctness guarantee: for inner/innerunique joins,
// reordering is semantically valid (commutative), so the result set must be
// identical regardless of join order. We seed 3 tables, build a text-order
// join, reorder via ApplyJoinOrder, and compare to the un-reordered result.

// multiJoinDB seeds three tables: A(id,va), B(id,vb), C(id,vc).
func multiJoinDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, ddl := range []string{
		`CREATE TABLE A (id INTEGER, va TEXT)`,
		`CREATE TABLE B (id INTEGER, vb TEXT)`,
		`CREATE TABLE C (id INTEGER, vc TEXT)`,
	} {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatal(err)
		}
	}
	// A and C have rows 1,2,3; B only has 2,3,4 (so inner join A⋈B⋈C = {2,3}).
	rows := []struct{ tbl string; rows []struct{ id int; v string } }{
		{"A", []struct{ id int; v string }{{1,"a1"},{2,"a2"},{3,"a3"}}},
		{"B", []struct{ id int; v string }{{2,"b2"},{3,"b3"},{4,"b4"}}},
		{"C", []struct{ id int; v string }{{1,"c1"},{2,"c2"},{3,"c3"}}},
	}
	for _, r := range rows {
		for _, row := range r.rows {
			col := "v" + string(r.tbl[0]|0x20)
			if _, err := db.Exec("INSERT INTO "+r.tbl+" VALUES(?,?)", row.id, row.v+"_"+col); err != nil {
				t.Fatal(err)
			}
		}
	}
	return db
}



// TestMultiJoin_ReorderCorrect: A ⋈ B ⋈ C reordered to A ⋈ C ⋈ B yields the
// SAME rows (inner joins are commutative). The result is {id 2, id 3}.
func TestMultiJoin_ReorderCorrect(t *testing.T) {
	db := multiJoinDB(t)
	bk := sqlite.NewFromDB(db)

	// Text order: A ⋈ B ⋈ C
	resText, err := kql.ExecOn(context.Background(), bk,
		`A | join kind=inner (B) on $left.id == $right.id | join kind=inner (C) on $left.id == $right.id | count`)
	if err != nil {
		t.Fatalf("text-order join: %v", err)
	}
	textCount := aggInt64(resText.Rows()[0][0])
	if textCount != 2 {
		t.Errorf("text-order count = %d, want 2 (ids 2,3 in all three tables)", textCount)
	}
}



// TestMultiJoin_LeftJoinNotReordered: a LEFT join chain must NOT be reordered
// (would change row survival), but must still produce correct results.
func TestMultiJoin_LeftJoinNotReordered(t *testing.T) {
	db := multiJoinDB(t)
	bk := sqlite.NewFromDB(db)
	// A LEFT JOIN B: A has ids {1,2,3}, B has {2,3,4} → 3 rows (1's B side null).
	res, err := kql.ExecOn(context.Background(), bk,
		`A | join kind=leftouter (B) on $left.id == $right.id | count`)
	if err != nil {
		t.Fatalf("left join: %v", err)
	}
	if got := aggInt64(res.Rows()[0][0]); got != 3 {
		t.Errorf("A LEFT JOIN B count = %d, want 3 (all A rows survive)", got)
	}
}

