package kql_test

import (
	"context"
	"database/sql"
	"testing"

	"nzinfo/kql/internal/backend/sqlite"
	"nzinfo/kql/pkg/kql"

	_ "modernc.org/sqlite"
)

// gapDB seeds an in-memory sqlite table for grammar-gap e2e tests.
func gapDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.Exec(`CREATE TABLE t (id INTEGER PRIMARY KEY, kind TEXT, n INTEGER)`)
	rows := []struct {
		id  int64
		k   string
		n   int64
	}{
		{1, "a", 10},
		{2, "a", 20},
		{3, "b", 30},
		{4, "b", 40},
	}
	for _, r := range rows {
		db.Exec(`INSERT INTO t VALUES(?,?,?)`, r.id, r.k, r.n)
	}
	return db
}

// TestE2E_AsOperator runs `T | ... | as Result | ...` and confirms `as` is a
// row-wise no-op (rows flow through unchanged). The name is metadata only.
func TestE2E_AsOperator(t *testing.T) {
	db := gapDB(t)
	bk := sqlite.NewFromDB(db)
	res, err := kql.ExecOn(context.Background(), bk,
		`t | where n > 15 | as Filtered | summarize total = sum(n) by kind | sort by kind`)
	if err != nil {
		t.Fatalf("ExecOn: %v", err)
	}
	// Rows with n>15: id2(a,20), id3(b,30), id4(b,40).
	// Grouped: a=20, b=70.
	if len(res.Rows()) != 2 {
		t.Fatalf("rows = %d, want 2: %+v", len(res.Rows()), res.Rows())
	}
	for _, row := range res.Rows() {
		kind, _ := row[0].(string)
		var n int64
		switch v := row[1].(type) {
		case int64:
			n = v
		case int:
			n = int64(v)
		}
		switch kind {
		case "a":
			if n != 20 {
				t.Errorf("kind a total = %d, want 20", n)
			}
		case "b":
			if n != 70 {
				t.Errorf("kind b total = %d, want 70", n)
			}
		}
	}
}

// TestE2E_InvokeOperator runs `T | ... | invoke Plugin(x)` end-to-end. invoke
// is a pass-through (real plugin semantics need a registry), so rows must
// flow through unchanged.
func TestE2E_InvokeOperator(t *testing.T) {
	db := gapDB(t)
	bk := sqlite.NewFromDB(db)
	res, err := kql.ExecOn(context.Background(), bk,
		`t | where kind == "a" | invoke MyPlugin(n) | sort by id`)
	if err != nil {
		t.Fatalf("ExecOn: %v", err)
	}
	if len(res.Rows()) != 2 {
		t.Fatalf("rows = %d, want 2 (kind=a passthrough)", len(res.Rows()))
	}
}

// TestE2E_SetStatement runs `set X; <query>` and confirms the `set` statement is
// skipped (no error, query executes normally).
func TestE2E_SetStatement(t *testing.T) {
	db := gapDB(t)
	bk := sqlite.NewFromDB(db)
	// Multi-statement: set option, then query.
	res, err := kql.ExecOn(context.Background(), bk,
		`set querytrace;
		 t | where kind == "b" | sort by id`)
	if err != nil {
		t.Fatalf("ExecOn: %v", err)
	}
	if len(res.Rows()) != 2 {
		t.Fatalf("rows = %d, want 2 (kind=b)", len(res.Rows()))
	}
}

// TestE2E_SetWithValue: `set opt = 30000000; <query>` — the value form parses
// and is skipped; query runs normally.
func TestE2E_SetWithValue(t *testing.T) {
	db := gapDB(t)
	bk := sqlite.NewFromDB(db)
	res, err := kql.ExecOn(context.Background(), bk,
		`set result_truncation_size = 30000000;
		 t | take 1`)
	if err != nil {
		t.Fatalf("ExecOn: %v", err)
	}
	if len(res.Rows()) != 1 {
		t.Fatalf("rows = %d, want 1", len(res.Rows()))
	}
}


// TestE2E_DeclareQueryParameters runs `declare query_parameters(...); <query>`
// end-to-end. The declare is metadata (translator skips it), so the query must
// execute normally. Parameter substitution is deferred; the query here doesn't
// reference the parameter (it would need binder/exec wiring to substitute).
func TestE2E_DeclareQueryParameters(t *testing.T) {
	db := gapDB(t)
	bk := sqlite.NewFromDB(db)
	res, err := kql.ExecOn(context.Background(), bk,
		`declare query_parameters(Limit:int = 5);
		 t | take 2 | sort by id`)
	if err != nil {
		t.Fatalf("ExecOn: %v", err)
	}
	if len(res.Rows()) != 2 {
		t.Fatalf("rows = %d, want 2", len(res.Rows()))
	}
}
