package kql_test

import (
	"context"
	"database/sql"
	"testing"

	"nzinfo/kql/internal/backend/sqlite"
	"nzinfo/kql/pkg/kql"

	_ "modernc.org/sqlite"
)

// PostProc end-to-end tests: mv-expand and parse now execute client-side
// (previously they were silent passthroughs — Project{Star} that dropped
// semantics). These verify the real semantics run via exec.applyPostProc.

// TestPostProc_MvExpand: explode a JSON array column into multiple rows.
func TestPostProc_MvExpand(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.Exec(`CREATE TABLE t (id INTEGER, tags TEXT)`)
	// tags is a JSON array stored as text (how dynamic columns arrive from SQL).
	db.Exec(`INSERT INTO t VALUES (1, '["a","b","c"]'), (2, '["x"]')`)

	bk := sqlite.NewFromDB(db)
	res, err := kql.ExecOn(context.Background(), bk, `t | mv-expand tag = tags`)
	if err != nil {
		t.Fatalf("mv-expand: %v", err)
	}
	// Row 1 has 3 tags → 3 rows; row 2 has 1 → 1 row. Total = 4 rows.
	if got := len(res.Rows()); got != 4 {
		t.Errorf("mv-expand rows = %d, want 4 (3 from row1 + 1 from row2)", got)
	}
}

// TestPostProc_MvExpand_NestedPipeline: mv-expand followed by summarize proves
// the exploded rows flow into downstream client-side stages.
func TestPostProc_MvExpand_NestedPipeline(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.Exec(`CREATE TABLE t (id INTEGER, tags TEXT)`)
	db.Exec(`INSERT INTO t VALUES (1, '["a","b","a"]'), (2, '["a","c"]')`)

	bk := sqlite.NewFromDB(db)
	// mv-expand then count total exploded elements.
	res, err := kql.ExecOn(context.Background(), bk, `t | mv-expand tag = tags | count`)
	if err != nil {
		t.Fatalf("mv-expand+count: %v", err)
	}
	if got := aggInt64(res.Rows()[0][0]); got != 5 {
		t.Errorf("mv-expand+count = %d, want 5 (3+2 elements)", got)
	}
}

// TestPostProc_Parse: regex extraction into new columns.
func TestPostProc_Parse(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.Exec(`CREATE TABLE t (line TEXT)`)
	db.Exec(`INSERT INTO t VALUES ('<Tag>alpha</Tag>'), ('<Tag>beta</Tag>'), ('<Other>x</Other>')`)

	bk := sqlite.NewFromDB(db)
	// parse line with '<Tag>' Tag '</Tag>' → extract Tag column.
	res, err := kql.ExecOn(context.Background(), bk,
		`t | parse line with '<Tag>' Tag '</Tag>'`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// All 3 rows kept (plain parse, not parse-where); row 3 has null Tag.
	if got := len(res.Rows()); got != 3 {
		t.Errorf("parse rows = %d, want 3 (all rows kept)", got)
	}
	// Verify the Tag column was added and captured alpha/beta.
	tagIdx := -1
	for i, c := range res.Columns() {
		if c.Name == "Tag" {
			tagIdx = i
			break
		}
	}
	if tagIdx < 0 {
		t.Fatalf("parse: Tag column not in schema %v", res.Columns())
	}
	if v := res.Rows()[0][tagIdx]; v != "alpha" {
		t.Errorf("parse row0 Tag = %v, want alpha", v)
	}
	if v := res.Rows()[1][tagIdx]; v != "beta" {
		t.Errorf("parse row1 Tag = %v, want beta", v)
	}
}

// TestPostProc_ParseWhere: parse-where drops non-matching rows.
func TestPostProc_ParseWhere(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.Exec(`CREATE TABLE t (line TEXT)`)
	db.Exec(`INSERT INTO t VALUES ('<Tag>alpha</Tag>'), ('<Other>x</Other>')`)

	bk := sqlite.NewFromDB(db)
	// parse-where: only matching rows survive.
	res, err := kql.ExecOn(context.Background(), bk,
		`t | parse-where line with '<Tag>' Tag '</Tag>'`)
	if err != nil {
		t.Fatalf("parse-where: %v", err)
	}
	if got := len(res.Rows()); got != 1 {
		t.Errorf("parse-where rows = %d, want 1 (only the matching row)", got)
	}
}
