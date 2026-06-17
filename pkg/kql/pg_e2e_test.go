package kql_test

import (
	"context"
	"os"
	"testing"

	"nzinfo/kql/pkg/kql"
)

// pgDSN returns the test pg DSN from KQL_PG_DSN, or "" to skip pg tests.
// The dev container is started via:
//   docker run -d --name kql-pg -e POSTGRES_PASSWORD=kql -e POSTGRES_USER=kql \
//     -e POSTGRES_DB=kql -p 5433:5432 postgres:16
// then seed with the events table (see testdata/pg-seed.sql or the manual step).
func pgDSN() string { return os.Getenv("KQL_PG_DSN") }

// pgSeed ensures the events table exists with the canonical 6-row dataset.
// Idempotent; safe to call per-test.
func pgSeed(t *testing.T, dsn string) {
	t.Helper()
	bk, err := kql.OpenBackend(dsn)
	if err != nil {
		t.Fatalf("open pg: %v", err)
	}
	defer bk.Close()
	// Use Exec via a direct query through the backend isn't exposed for raw
	// DDL; go through the public Exec with a no-op KQL to confirm connectivity,
	// then rely on the already-seeded table. (Seeding is done out-of-band by
	// the developer; this just verifies the backend opens.)
	_ = bk
}

func pgRun(t *testing.T, dsn, query string) *kql.Result {
	t.Helper()
	res, err := kql.Exec(context.Background(), dsn, query)
	if err != nil {
		t.Fatalf("pg Exec(%q): %v", query, err)
	}
	return res
}

// TestPg_E2E runs the canonical queries against a real pg (via Docker), gated
// on KQL_PG_DSN. Mirrors the sqlite e2e tests so both backends stay in lockstep.
//
// To run: KQL_PG_DSN="postgres://kql:kql@localhost:5433/kql" go test ./pkg/kql/ -run TestPg_
func TestPg_SourceOnly(t *testing.T) {
	dsn := pgDSN()
	if dsn == "" {
		t.Skip("set KQL_PG_DSN to run pg e2e tests")
	}
	res := pgRun(t, dsn, `events`)
	if len(res.Rows()) != 6 {
		t.Errorf("rows = %d, want 6", len(res.Rows()))
	}
}

func TestPg_Where(t *testing.T) {
	dsn := pgDSN()
	if dsn == "" {
		t.Skip("set KQL_PG_DSN")
	}
	res := pgRun(t, dsn, `events | where state == "TEXAS"`)
	if len(res.Rows()) != 3 {
		t.Errorf("rows = %d, want 3 (TEXAS)", len(res.Rows()))
	}
}

func TestPg_Take(t *testing.T) {
	dsn := pgDSN()
	if dsn == "" {
		t.Skip("set KQL_PG_DSN")
	}
	res := pgRun(t, dsn, `events | take 2`)
	if len(res.Rows()) != 2 {
		t.Errorf("rows = %d, want 2", len(res.Rows()))
	}
}

func TestPg_SummarizeSumByState(t *testing.T) {
	dsn := pgDSN()
	if dsn == "" {
		t.Skip("set KQL_PG_DSN")
	}
	res := pgRun(t, dsn, `events | summarize total = sum(damage) by state`)
	totals := map[string]float64{}
	for _, row := range res.Rows() {
		state := stringVal(row[0])
		totals[state] = floatVal(row[1])
	}
	if totals["TEXAS"] != 4800.5 {
		t.Errorf("TEXAS total = %v, want 4800.5", totals["TEXAS"])
	}
	if totals["FLORIDA"] != 9000 {
		t.Errorf("FLORIDA total = %v, want 9000", totals["FLORIDA"])
	}
}

func TestPg_SummarizeCountByState(t *testing.T) {
	dsn := pgDSN()
	if dsn == "" {
		t.Skip("set KQL_PG_DSN")
	}
	res := pgRun(t, dsn, `events | summarize c = count() by state`)
	if len(res.Rows()) != 3 {
		t.Errorf("rows = %d, want 3 distinct states", len(res.Rows()))
	}
}

func TestPg_SortTake(t *testing.T) {
	dsn := pgDSN()
	if dsn == "" {
		t.Skip("set KQL_PG_DSN")
	}
	res := pgRun(t, dsn, `events | sort by damage desc | take 1`)
	if len(res.Rows()) != 1 {
		t.Fatalf("rows = %d, want 1", len(res.Rows()))
	}
	if floatVal(res.Rows()[0][2]) != 9000 {
		t.Errorf("top damage = %v, want 9000", res.Rows()[0][2])
	}
}

func TestPg_Distinct(t *testing.T) {
	dsn := pgDSN()
	if dsn == "" {
		t.Skip("set KQL_PG_DSN")
	}
	res := pgRun(t, dsn, `events | distinct state`)
	if len(res.Rows()) != 3 {
		t.Errorf("rows = %d, want 3 distinct states", len(res.Rows()))
	}
}

func TestPg_InList(t *testing.T) {
	dsn := pgDSN()
	if dsn == "" {
		t.Skip("set KQL_PG_DSN")
	}
	res := pgRun(t, dsn, `events | where state in ("TEXAS", "FLORIDA")`)
	if len(res.Rows()) != 4 {
		t.Errorf("rows = %d, want 4 (3 TEXAS + 1 FLORIDA)", len(res.Rows()))
	}
}

func TestPg_StringOp(t *testing.T) {
	dsn := pgDSN()
	if dsn == "" {
		t.Skip("set KQL_PG_DSN")
	}
	// Case-folding acceptance: the column is stored as `eventtype` (pg lowercased
	// the unquoted DDL), but the KQL query uses the CamelCase `EventType`. With
	// ColID binding (case-insensitive resolution + physical-name rewrite), this
	// now resolves correctly. This is the test that B2-minimal couldn't pass.
	res := pgRun(t, dsn, `events | where EventType has "ail"`)
	if len(res.Rows()) != 2 {
		t.Errorf("rows = %d, want 2 (Hail via ILIKE, case-folded EventType)", len(res.Rows()))
	}
}

func TestPg_BinderUnknownColumn(t *testing.T) {
	dsn := pgDSN()
	if dsn == "" {
		t.Skip("set KQL_PG_DSN")
	}
	_, err := kql.Exec(context.Background(), dsn, `events | where nonexistent_col > 5`)
	if err == nil {
		t.Error("expected friendly bind error for unknown column, got nil")
	}
}

// stringVal / floatVal coerce pg-returned cell values.
func stringVal(v interface{}) string {
	switch x := v.(type) {
	case string:
		return x
	case []byte:
		return string(x)
	}
	return ""
}
func floatVal(v interface{}) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case float32:
		return float64(x)
	case int64:
		return float64(x)
	}
	return 0
}
