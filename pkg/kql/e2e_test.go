// Package kql_test exercises the public kql API end-to-end: parse KQL →
// translate to IR → emit SQLite SQL → execute against an in-memory SQLite
// database → verify the returned rows. This is the minimal closed loop
// (docs/PROGRESS.md / user direction) that validates the whole frontend→IR→
// backend pipeline.
package kql_test

import (
	"context"
	"database/sql"
	"testing"

	"nzinfo/kql/internal/backend"
	"nzinfo/kql/internal/backend/sqlite"
	"nzinfo/kql/pkg/kql"

	_ "modernc.org/sqlite"
)

// setupDB creates an in-memory SQLite database with a sample StormEvents-like
// table and returns a *sql.DB preloaded with deterministic rows.
func setupDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	_, err = db.Exec(`CREATE TABLE events (id INTEGER PRIMARY KEY, state TEXT, damage REAL, EventType TEXT)`)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}
	rows := []struct {
		id        int64
		state     string
		damage    float64
		eventType string
	}{
		{1, "TEXAS", 1500.0, "Hail"},
		{2, "TEXAS", 3200.5, "Wind"},
		{3, "OKLAHOMA", 500.0, "Flood"},
		{4, "TEXAS", 100.0, "Hail"},
		{5, "FLORIDA", 9000.0, "Hurricane"},
		{6, "OKLAHOMA", 750.0, "Wind"},
	}
	for _, r := range rows {
		if _, err := db.Exec(`INSERT INTO events (id, state, damage, EventType) VALUES (?, ?, ?, ?)`,
			r.id, r.state, r.damage, r.eventType); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	return db
}

// execKQL runs a KQL query against the events table and returns the result.
func execKQL(t *testing.T, db *sql.DB, query string) *kql.Result {
	t.Helper()
	bk := sqlite.NewFromDB(db)
	res, err := kql.ExecOn(context.Background(), bk, query)
	if err != nil {
		t.Fatalf("kql.ExecOn(%q): %v", query, err)
	}
	return res
}

// TestE2E_SourceOnly: `events` returns all 6 rows.
func TestE2E_SourceOnly(t *testing.T) {
	db := setupDB(t)
	res := execKQL(t, db, `events`)
	if len(res.Rows()) != 6 {
		t.Errorf("rows = %d, want 6", len(res.Rows()))
	}
	if len(res.Columns()) != 4 {
		t.Errorf("columns = %d, want 4", len(res.Columns()))
	}
}

// TestE2E_Where: `events | where state == "TEXAS"` → 3 rows.
func TestE2E_Where(t *testing.T) {
	db := setupDB(t)
	res := execKQL(t, db, `events | where state == "TEXAS"`)
	if len(res.Rows()) != 3 {
		t.Errorf("rows = %d, want 3 (TEXAS events)", len(res.Rows()))
	}
}

// TestE2E_Take: `events | take 2` → exactly 2 rows.
func TestE2E_Take(t *testing.T) {
	db := setupDB(t)
	res := execKQL(t, db, `events | take 2`)
	if len(res.Rows()) != 2 {
		t.Errorf("rows = %d, want 2", len(res.Rows()))
	}
}

// TestE2E_Project: `events | project state, damage` → 2 columns.
func TestE2E_Project(t *testing.T) {
	db := setupDB(t)
	res := execKQL(t, db, `events | project state, damage`)
	if len(res.Columns()) != 2 {
		t.Errorf("columns = %d, want 2 (state, damage)", len(res.Columns()))
	}
	if len(res.Rows()) != 6 {
		t.Errorf("rows = %d, want 6", len(res.Rows()))
	}
}

// TestE2E_ProjectRename: `events | project s = state` → column named "s".
func TestE2E_ProjectRename(t *testing.T) {
	db := setupDB(t)
	res := execKQL(t, db, `events | project s = state`)
	if res.Columns()[0].Name != "s" {
		t.Errorf("column name = %q, want 's'", res.Columns()[0].Name)
	}
}

// TestE2E_Extend: `events | extend doubled = damage * 2` → 5 cols incl doubled.
func TestE2E_Extend(t *testing.T) {
	db := setupDB(t)
	res := execKQL(t, db, `events | extend doubled = damage * 2`)
	if len(res.Columns()) != 5 {
		t.Errorf("columns = %d, want 5 (4 + doubled)", len(res.Columns()))
	}
	// first row damage=1500 → doubled=3000
	got := res.Rows()[0][4]
	if asFloat(got) != 3000.0 {
		t.Errorf("row0 doubled = %v, want 3000", got)
	}
}

// TestE2E_SortTake: `events | sort by damage desc | take 1` → top damage 9000.
func TestE2E_SortTake(t *testing.T) {
	db := setupDB(t)
	res := execKQL(t, db, `events | sort by damage desc | take 1`)
	if len(res.Rows()) != 1 {
		t.Fatalf("rows = %d, want 1", len(res.Rows()))
	}
	// damage is column index 2; Florida hurricane = 9000
	damage := res.Rows()[0][2]
	if asFloat(damage) != 9000.0 {
		t.Errorf("top damage = %v, want 9000", damage)
	}
}

// TestE2E_SummarizeCount: `events | summarize c = count() by state` → 3 states.
func TestE2E_SummarizeCount(t *testing.T) {
	db := setupDB(t)
	res := execKQL(t, db, `events | summarize c = count() by state`)
	if len(res.Rows()) != 3 {
		t.Errorf("rows = %d, want 3 distinct states", len(res.Rows()))
	}
}

// TestE2E_SummarizeSum: `events | summarize total = sum(damage) by state`
// TEXAS total = 1500+3200.5+100 = 4800.5
func TestE2E_SummarizeSum(t *testing.T) {
	db := setupDB(t)
	res := execKQL(t, db, `events | summarize total = sum(damage) by state`)
	totals := map[string]float64{}
	for _, row := range res.Rows() {
		state, _ := row[0].(string)
		totals[state] = asFloat(row[1])
	}
	if totals["TEXAS"] != 4800.5 {
		t.Errorf("TEXAS total = %v, want 4800.5", totals["TEXAS"])
	}
}

// TestE2E_Distinct: `events | distinct state` → 3 distinct states.
func TestE2E_Distinct(t *testing.T) {
	db := setupDB(t)
	res := execKQL(t, db, `events | distinct state`)
	if len(res.Rows()) != 3 {
		t.Errorf("rows = %d, want 3 distinct states", len(res.Rows()))
	}
}

// TestE2E_Count: `events | count` → 1 row with the count.
func TestE2E_Count(t *testing.T) {
	db := setupDB(t)
	res := execKQL(t, db, `events | count`)
	if len(res.Rows()) != 1 {
		t.Fatalf("rows = %d, want 1", len(res.Rows()))
	}
	if asInt(res.Rows()[0][0]) != 6 {
		t.Errorf("count = %v, want 6", res.Rows()[0][0])
	}
}

// TestE2E_Top: `events | top 1 by damage desc` → top-damage row.
func TestE2E_Top(t *testing.T) {
	db := setupDB(t)
	res := execKQL(t, db, `events | top 1 by damage desc`)
	if len(res.Rows()) != 1 {
		t.Fatalf("rows = %d, want 1", len(res.Rows()))
	}
	if asFloat(res.Rows()[0][2]) != 9000.0 {
		t.Errorf("top damage = %v, want 9000", res.Rows()[0][2])
	}
}

// TestE2E_InList: `events | where state in ("TEXAS", "FLORIDA")` → 4 rows.
func TestE2E_InList(t *testing.T) {
	db := setupDB(t)
	res := execKQL(t, db, `events | where state in ("TEXAS", "FLORIDA")`)
	if len(res.Rows()) != 4 {
		t.Errorf("rows = %d, want 4 (3 TEXAS + 1 FLORIDA)", len(res.Rows()))
	}
}

// TestE2E_FullPipeline: the canonical multi-stage query.
// `events | where damage > 500 | extend k = damage * 2 | summarize total = sum(k) by state | sort by total desc | take 1`
func TestE2E_FullPipeline(t *testing.T) {
	db := setupDB(t)
	query := `events | where damage > 500 | extend k = damage * 2 | summarize total = sum(k) by state | sort by total desc | take 1`
	res := execKQL(t, db, query)
	if len(res.Rows()) != 1 {
		t.Fatalf("rows = %d, want 1", len(res.Rows()))
	}
	// FLORIDA: damage 9000 → k 18000 → total 18000 (only FLORIDA row survives >500)
	// Actually OKLAHOMA Wind 750 → k 1500, TEXAS 1500→3000, 3200.5→6401, FL 9000→18000
	// states with damage>500: TX(1500,3200.5), OK(500? no, 500 excluded by >500; OK 750 yes), FL(9000)
	// totals: TX=3000+6401=9401, OK=1500, FL=18000 → top is FL 18000
	if asFloat(res.Rows()[0][1]) != 18000.0 {
		t.Errorf("top total = %v, want 18000 (FLORIDA)", res.Rows()[0][1])
	}
}

// TestE2E_StringOp: `events | where EventType has "ail"` → 2 Hail rows.
func TestE2E_StringOp(t *testing.T) {
	db := setupDB(t)
	res := execKQL(t, db, `events | where EventType has "ail"`)
	if len(res.Rows()) != 2 {
		t.Errorf("rows = %d, want 2 (Hail)", len(res.Rows()))
	}
}

// TestE2E_ParseError: malformed query surfaces an error (not a panic).
func TestE2E_ParseError(t *testing.T) {
	db := setupDB(t)
	bk := sqlite.NewFromDB(db)
	_, err := kql.ExecOn(context.Background(), bk, `events | where`)
	if err == nil {
		t.Error("expected error for incomplete query, got nil")
	}
}

// TestE2E_BackendInterface: the sqlite backend satisfies backend.Backend.
func TestE2E_BackendInterface(t *testing.T) {
	var _ backend.Backend = (*sqlite.Backend)(nil)
}

// asFloat coerces a driver-returned numeric value to float64.
func asFloat(v interface{}) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int64:
		return float64(n)
	case int:
		return float64(n)
	}
	return 0
}

// asInt coerces a driver-returned integer value to int64.
func asInt(v interface{}) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	}
	return 0
}
