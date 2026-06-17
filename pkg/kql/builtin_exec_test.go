package kql_test

import (
	"context"
	"database/sql"
	"testing"

	"nzinfo/kql/internal/backend/sqlite"
	"nzinfo/kql/pkg/kql"

	_ "modernc.org/sqlite"
)

// fnDB seeds an in-memory db with a small events table for function-execution tests.
func fnDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.Exec(`CREATE TABLE events (id INTEGER PRIMARY KEY, state TEXT, score REAL, active INTEGER)`)
	rows := []struct {
		id     int64
		state  string
		score  float64
		active int64
	}{
		{1, "TX", 10.0, 1},
		{2, "TX", 20.0, 0},
		{3, "FL", 30.0, 1},
		{4, "FL", 40.0, 1},
		{5, "CA", 50.0, 0},
	}
	for _, r := range rows {
		db.Exec(`INSERT INTO events VALUES (?,?,?,?)`, r.id, r.state, r.score, r.active)
	}
	return db
}

func fnRun(t *testing.T, db *sql.DB, query string) *kql.Result {
	t.Helper()
	bk := sqlite.NewFromDB(db)
	res, err := kql.ExecOn(context.Background(), bk, query)
	if err != nil {
		t.Fatalf("ExecOn(%q): %v", query, err)
	}
	return res
}

// TestFnExec_ToString proves tostring() executes and casts to TEXT.
func TestFnExec_ToString(t *testing.T) {
	db := fnDB(t)
	res := fnRun(t, db, `events | extend s = tostring(score) | take 1`)
	if len(res.Rows()) != 1 {
		t.Fatalf("rows = %d, want 1", len(res.Rows()))
	}
	// tostring(10.0) → "10.0" (text)
	if s, ok := res.Rows()[0][4].(string); !ok || s == "" {
		t.Errorf("tostring result = %#v, want non-empty string", res.Rows()[0][4])
	}
}

// TestFnExec_Iff proves iff(cond, a, b) → CASE executes correctly.
func TestFnExec_Iff(t *testing.T) {
	db := fnDB(t)
	res := fnRun(t, db, `events | extend flag = iff(score > 25, 1, 0)`)
	if len(res.Rows()) != 5 {
		t.Fatalf("rows = %d, want 5", len(res.Rows()))
	}
	// id=1 score=10 → 0; id=3 score=30 → 1
	if got := asInt64(res.Rows()[0][4]); got != 0 {
		t.Errorf("row0 iff = %d, want 0", got)
	}
	if got := asInt64(res.Rows()[2][4]); got != 1 {
		t.Errorf("row2 iff = %d, want 1", got)
	}
}

// TestFnExec_Dcount proves dcount → COUNT(DISTINCT) executes.
func TestFnExec_Dcount(t *testing.T) {
	db := fnDB(t)
	res := fnRun(t, db, `events | summarize d = dcount(state)`)
	if len(res.Rows()) != 1 {
		t.Fatalf("rows = %d, want 1", len(res.Rows()))
	}
	// 3 distinct states: TX, FL, CA
	if got := asInt64(res.Rows()[0][0]); got != 3 {
		t.Errorf("dcount = %d, want 3", got)
	}
}

// TestFnExec_Countif proves countif → SUM(CASE...) executes.
func TestFnExec_Countif(t *testing.T) {
	db := fnDB(t)
	res := fnRun(t, db, `events | summarize c = countif(active == 1) by state | sort by state`)
	// TX: 1 active (id=1); FL: 2 active; CA: 0 active
	if len(res.Rows()) != 3 {
		t.Fatalf("rows = %d, want 3", len(res.Rows()))
	}
	got := map[string]int64{}
	for _, row := range res.Rows() {
		got[row[0].(string)] = asInt64(row[1])
	}
	if got["TX"] != 1 || got["FL"] != 2 || got["CA"] != 0 {
		t.Errorf("countif by state = %v, want TX=1 FL=2 CA=0", got)
	}
}

// TestFnExec_Coalesce proves variadic coalesce executes.
func TestFnExec_Coalesce(t *testing.T) {
	db := fnDB(t)
	// coalesce returns the first non-null; score is never null here so it returns score.
	res := fnRun(t, db, `events | extend c = coalesce(score, 999) | take 1`)
	if asFloat64(res.Rows()[0][4]) != 10.0 {
		t.Errorf("coalesce = %v, want 10.0", res.Rows()[0][4])
	}
}

// TestFnExec_Strcat proves variadic strcat → || concatenation executes.
func TestFnExec_Strcat(t *testing.T) {
	db := fnDB(t)
	res := fnRun(t, db, `events | extend label = strcat(state, "-", tostring(id)) | take 1`)
	if got, ok := res.Rows()[0][4].(string); !ok || got != "TX-1" {
		t.Errorf("strcat = %#v, want 'TX-1'", res.Rows()[0][4])
	}
}

// TestFnExec_SumMinMaxAvg proves aggregates execute.
func TestFnExec_SumMinMaxAvg(t *testing.T) {
	db := fnDB(t)
	res := fnRun(t, db, `events | summarize total = sum(score), lo = min(score), hi = max(score), mean = avg(score)`)
	if len(res.Rows()) != 1 {
		t.Fatalf("rows = %d, want 1", len(res.Rows()))
	}
	row := res.Rows()[0]
	if asFloat64(row[0]) != 150.0 { // 10+20+30+40+50
		t.Errorf("sum = %v, want 150", row[0])
	}
	if asFloat64(row[1]) != 10.0 {
		t.Errorf("min = %v, want 10", row[1])
	}
	if asFloat64(row[2]) != 50.0 {
		t.Errorf("max = %v, want 50", row[2])
	}
	if asFloat64(row[3]) != 30.0 { // 150/5
		t.Errorf("avg = %v, want 30", row[3])
	}
}

// TestFnExec_IsNotEmpty proves isnotempty() executes as a filter.
func TestFnExec_IsNotEmpty(t *testing.T) {
	db := fnDB(t)
	res := fnRun(t, db, `events | where isnotempty(state)`)
	if len(res.Rows()) != 5 {
		t.Errorf("rows = %d, want 5 (all states non-empty)", len(res.Rows()))
	}
}

// TestFnExec_AbsToint proves abs + toint execute.
func TestFnExec_AbsToint(t *testing.T) {
	db := fnDB(t)
	res := fnRun(t, db, `events | extend a = abs(score - 25) | take 1`)
	if asFloat64(res.Rows()[0][4]) != 15.0 { // |10-25|
		t.Errorf("abs = %v, want 15", res.Rows()[0][4])
	}
}

// helpers
func asInt64(v interface{}) int64 {
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
func asFloat64(v interface{}) float64 {
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
