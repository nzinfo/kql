package kql_test

import (
	"context"
	"database/sql"
	"testing"

	"nzinfo/kql/internal/backend/sqlite"
	"nzinfo/kql/pkg/kql"
	_ "modernc.org/sqlite"
)

// TestSerialize_RowNumber: serialize rn = row_number() adds an rn column while
// preserving all existing columns, with correct sequential numbering. This is
// the sqlite window-function emit path (ROW_NUMBER() OVER () in a subquery).
func TestSerialize_RowNumber(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.Exec(`CREATE TABLE SecurityEvent (EventID INTEGER, Account TEXT)`)
	db.Exec(`INSERT INTO SecurityEvent VALUES (1,'alice'),(2,'bob'),(3,'carol')`)
	bk := sqlite.NewFromDB(db)
	res, err := kql.ExecOn(context.Background(), bk,
		`SecurityEvent | serialize rn = row_number() | where rn <= 2`)
	if err != nil {
		t.Fatalf("serialize+row_number: %v", err)
	}
	// Must preserve EventID + Account AND add rn.
	cols := res.Columns()
	if len(cols) != 3 {
		t.Errorf("columns = %d (%v), want 3 (EventID, Account, rn)", len(cols), cols)
	}
	// 2 rows (rn <= 2).
	if got := len(res.Rows()); got != 2 {
		t.Fatalf("rows = %d, want 2", got)
	}
	// Verify rn values are sequential 1, 2.
	rnIdx := -1
	for i, c := range cols {
		if c.Name == "rn" {
			rnIdx = i
		}
	}
	if rnIdx < 0 {
		t.Fatalf("rn column not found in %v", cols)
	}
	if v := aggInt64(res.Rows()[0][rnIdx]); v != 1 {
		t.Errorf("row0 rn = %d, want 1", v)
	}
	if v := aggInt64(res.Rows()[1][rnIdx]); v != 2 {
		t.Errorf("row1 rn = %d, want 2", v)
	}
}

// TestSerialize_RowNumber_AllRows: without a filter, rn covers all rows.
func TestSerialize_RowNumber_AllRows(t *testing.T) {
	db, _ := sql.Open("sqlite", ":memory:")
	db.Exec(`CREATE TABLE t (v INTEGER)`)
	db.Exec(`INSERT INTO t VALUES (10),(20),(30),(40)`)
	bk := sqlite.NewFromDB(db)
	res, err := kql.ExecOn(context.Background(), bk, `t | serialize rn = row_number()`)
	if err != nil {
		t.Fatalf("serialize+row_number: %v", err)
	}
	if got := len(res.Rows()); got != 4 {
		t.Errorf("rows = %d, want 4 (all rows get an rn)", got)
	}
}
