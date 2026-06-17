package kql_test

import (
	"context"
	"database/sql"
	"testing"

	"nzinfo/kql/internal/backend/sqlite"
	"nzinfo/kql/pkg/kql"

	_ "modernc.org/sqlite"
)

// aggIfDB seeds an in-memory sqlite with scores for aggregate-if testing.
func aggIfDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.Exec(`CREATE TABLE scores (id INTEGER PRIMARY KEY, team TEXT, points INTEGER, active INTEGER)`)
	rows := []struct {
		id     int64
		team   string
		points int64
		active int64
	}{
		{1, "A", 10, 1},
		{2, "A", 20, 0},
		{3, "A", 30, 1},
		{4, "B", 40, 1},
		{5, "B", 50, 0},
	}
	for _, r := range rows {
		db.Exec(`INSERT INTO scores VALUES(?,?,?,?)`, r.id, r.team, r.points, r.active)
	}
	return db
}

func aggIfRun(t *testing.T, db *sql.DB, query string) *kql.Result {
	t.Helper()
	bk := sqlite.NewFromDB(db)
	res, err := kql.ExecOn(context.Background(), bk, query)
	if err != nil {
		t.Fatalf("ExecOn(%q): %v", query, err)
	}
	return res
}

// TestAggIf_SumIf: sumif(points, active == 1) by team.
// Team A: active rows have 10+30=40. Team B: only 40.
func TestAggIf_SumIf(t *testing.T) {
	db := aggIfDB(t)
	res := aggIfRun(t, db, `scores | summarize s = sumif(points, active == 1) by team | sort by team`)
	if len(res.Rows()) != 2 {
		t.Fatalf("rows = %d, want 2", len(res.Rows()))
	}
	// Team A: 10+30=40, Team B: 40
	got := map[string]int64{}
	for _, row := range res.Rows() {
		got[row[0].(string)] = aggInt64(row[1])
	}
	if got["A"] != 40 {
		t.Errorf("sumif A = %d, want 40", got["A"])
	}
	if got["B"] != 40 {
		t.Errorf("sumif B = %d, want 40", got["B"])
	}
}

// TestAggIf_AvgIf: avgif(points, active == 1) by team.
// Team A: avg(10,30)=20. Team B: avg(40)=40.
func TestAggIf_AvgIf(t *testing.T) {
	db := aggIfDB(t)
	res := aggIfRun(t, db, `scores | summarize a = avgif(points, active == 1) by team | sort by team`)
	got := map[string]float64{}
	for _, row := range res.Rows() {
		got[row[0].(string)] = aggFloat64(row[1])
	}
	if got["A"] != 20.0 {
		t.Errorf("avgif A = %v, want 20", got["A"])
	}
	if got["B"] != 40.0 {
		t.Errorf("avgif B = %v, want 40", got["B"])
	}
}

// TestAggIf_MaxIf: maxif(points, active == 1) by team.
func TestAggIf_MaxIf(t *testing.T) {
	db := aggIfDB(t)
	res := aggIfRun(t, db, `scores | summarize m = maxif(points, active == 1) by team | sort by team`)
	got := map[string]int64{}
	for _, row := range res.Rows() {
		got[row[0].(string)] = aggInt64(row[1])
	}
	if got["A"] != 30 {
		t.Errorf("maxif A = %d, want 30", got["A"])
	}
	if got["B"] != 40 {
		t.Errorf("maxif B = %d, want 40", got["B"])
	}
}

// TestAggIf_MinIf: minif(points, active == 1) by team.
func TestAggIf_MinIf(t *testing.T) {
	db := aggIfDB(t)
	res := aggIfRun(t, db, `scores | summarize m = minif(points, active == 1) by team | sort by team`)
	got := map[string]int64{}
	for _, row := range res.Rows() {
		got[row[0].(string)] = aggInt64(row[1])
	}
	if got["A"] != 10 {
		t.Errorf("minif A = %d, want 10", got["A"])
	}
	if got["B"] != 40 {
		t.Errorf("minif B = %d, want 40", got["B"])
	}
}

func aggInt64(v interface{}) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case int:
		return int64(x)
	case float64:
		return int64(x)
	}
	return 0
}
func aggFloat64(v interface{}) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int64:
		return float64(x)
	case int:
		return float64(x)
	}
	return 0
}
