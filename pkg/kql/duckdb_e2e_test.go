package kql_test

import (
	"context"
	"testing"

	"nzinfo/kql/internal/backend"
	"nzinfo/kql/internal/backend/duckdb"
	"nzinfo/kql/pkg/kql"
)

// duckSeed creates an in-memory DuckDB with the canonical events table.
// DuckDB is in-process (no Docker needed) — these tests always run (no env gate).
func duckSeed(t *testing.T) *duckdb.Backend {
	t.Helper()
	bk, err := duckdb.New("")
	if err != nil {
		t.Skipf("duckdb not available: %v", err)
	}
	t.Cleanup(func() { bk.Close() })
	seed := []string{
		`CREATE TABLE events (id INTEGER, state VARCHAR, damage DOUBLE, eventtype VARCHAR)`,
		`INSERT INTO events VALUES (1,'TEXAS',1500.0,'Hail'),(2,'TEXAS',3200.5,'Wind'),(3,'OKLAHOMA',500.0,'Flood'),(4,'TEXAS',100.0,'Hail'),(5,'FLORIDA',9000.0,'Hurricane'),(6,'OKLAHOMA',750.0,'Wind')`,
	}
	for _, s := range seed {
		if _, err := bk.Exec(context.Background(), &backend.Query{SQL: s}); err != nil {
			t.Fatalf("duckdb seed %q: %v", s, err)
		}
	}
	return bk
}

func duckRun(t *testing.T, bk *duckdb.Backend, query string) *kql.Result {
	t.Helper()
	res, err := kql.ExecOn(context.Background(), bk, query)
	if err != nil {
		t.Fatalf("duckdb ExecOn(%q): %v", query, err)
	}
	return res
}

func TestDuck_SourceOnly(t *testing.T) {
	bk := duckSeed(t)
	res := duckRun(t, bk, `events`)
	if len(res.Rows()) != 6 {
		t.Errorf("rows = %d, want 6", len(res.Rows()))
	}
}

func TestDuck_Where(t *testing.T) {
	bk := duckSeed(t)
	res := duckRun(t, bk, `events | where state == "TEXAS"`)
	if len(res.Rows()) != 3 {
		t.Errorf("rows = %d, want 3", len(res.Rows()))
	}
}

func TestDuck_Take(t *testing.T) {
	bk := duckSeed(t)
	res := duckRun(t, bk, `events | take 2`)
	if len(res.Rows()) != 2 {
		t.Errorf("rows = %d, want 2", len(res.Rows()))
	}
}

func TestDuck_SummarizeSum(t *testing.T) {
	bk := duckSeed(t)
	res := duckRun(t, bk, `events | summarize total = sum(damage) by state`)
	totals := map[string]float64{}
	for _, row := range res.Rows() {
		totals[stringVal(row[0])] = floatVal(row[1])
	}
	if totals["TEXAS"] != 4800.5 {
		t.Errorf("TEXAS = %v, want 4800.5", totals["TEXAS"])
	}
}

func TestDuck_SummarizeCount(t *testing.T) {
	bk := duckSeed(t)
	res := duckRun(t, bk, `events | summarize c = count() by state`)
	if len(res.Rows()) != 3 {
		t.Errorf("rows = %d, want 3", len(res.Rows()))
	}
}

func TestDuck_SortTake(t *testing.T) {
	bk := duckSeed(t)
	res := duckRun(t, bk, `events | sort by damage desc | take 1`)
	if len(res.Rows()) != 1 || floatVal(res.Rows()[0][2]) != 9000 {
		t.Errorf("top damage = %v, want 9000", res.Rows())
	}
}

func TestDuck_Distinct(t *testing.T) {
	bk := duckSeed(t)
	res := duckRun(t, bk, `events | distinct state`)
	if len(res.Rows()) != 3 {
		t.Errorf("rows = %d, want 3", len(res.Rows()))
	}
}

func TestDuck_InList(t *testing.T) {
	bk := duckSeed(t)
	res := duckRun(t, bk, `events | where state in ("TEXAS", "FLORIDA")`)
	if len(res.Rows()) != 4 {
		t.Errorf("rows = %d, want 4", len(res.Rows()))
	}
}

func TestDuck_Iff(t *testing.T) {
	bk := duckSeed(t)
	res := duckRun(t, bk, `events | extend big = iff(damage > 5000, 1, 0) | sort by damage desc | take 1`)
	if len(res.Rows()) != 1 {
		t.Fatalf("rows = %d", len(res.Rows()))
	}
	// DuckDB returns ints; FLORIDA 9000 → 1
	if v := int64Val(res.Rows()[0][4]); v != 1 {
		t.Errorf("iff = %d, want 1", v)
	}
}

func TestDuck_Join(t *testing.T) {
	bk := duckSeed(t)
	// create meta table
	bk.Exec(context.Background(), &backend.Query{
		SQL: `CREATE TABLE meta (id INTEGER, region VARCHAR); INSERT INTO meta VALUES (1,'south'),(5,'gulf')`})
	res := duckRun(t, bk, `events | join kind=inner (meta) on $left.id == $right.id | project state, region | sort by state`)
	if len(res.Rows()) < 1 {
		t.Fatalf("join rows = %d, want ≥1", len(res.Rows()))
	}
}
