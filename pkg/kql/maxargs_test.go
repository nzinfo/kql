package kql_test

import (
	"context"
	"database/sql"
	"testing"

	"nzinfo/kql/internal/backend/sqlite"
	"nzinfo/kql/pkg/kql"
	_ "modernc.org/sqlite"
)

// TestMakeSet_MaxSizeArgDropped: make_set(col, 10) must drop the maxSize arg
// before emitting (sqlite's group_concat(DISTINCT ...) takes exactly 1 arg).
// Regression for the Sigma "ProcessCreationWithParentAnalysis" hard error.
func TestMakeSet_MaxSizeArgDropped(t *testing.T) {
	db, _ := sql.Open("sqlite", ":memory:")
	db.Exec(`CREATE TABLE t (grp TEXT, v TEXT)`)
	db.Exec(`INSERT INTO t VALUES ('a','x'),('a','y'),('a','x'),('b','z')`)
	bk := sqlite.NewFromDB(db)
	res, err := kql.ExecOn(context.Background(), bk,
		`t | summarize s = make_set(v, 10) by grp | sort by grp`)
	if err != nil {
		t.Fatalf("make_set(v, 10): %v", err)
	}
	if len(res.Rows()) != 2 {
		t.Errorf("rows = %d, want 2 (groups a, b)", len(res.Rows()))
	}
}

// TestMakeList_MaxSizeArgDropped: same for make_list(col, maxSize).
func TestMakeList_MaxSizeArgDropped(t *testing.T) {
	db, _ := sql.Open("sqlite", ":memory:")
	db.Exec(`CREATE TABLE t (v TEXT)`)
	db.Exec(`INSERT INTO t VALUES ('x'),('y')`)
	bk := sqlite.NewFromDB(db)
	_, err := kql.ExecOn(context.Background(), bk, `t | summarize l = make_list(v, 5)`)
	if err != nil {
		t.Errorf("make_list(v, 5): %v", err)
	}
}
