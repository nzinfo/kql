package main

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"strings"
	"testing"

	"nzinfo/kql/internal/backend"
	"nzinfo/kql/internal/backend/sqlite"
	"nzinfo/kql/pkg/kql"

	_ "modernc.org/sqlite"
)

// ---- output formatter unit tests ----

func sampleResult() *kql.Result {
	return kql.WrapResult(&backend.Result{
		Columns: []backend.ResultColumn{{Name: "state"}, {Name: "total"}},
		Rows: [][]interface{}{
			{"TEXAS", 4700.5},
			{"FLORIDA", 9000.0},
		},
	})
}

func TestPrintCSV(t *testing.T) {
	var buf bytes.Buffer
	if err := printResult(&buf, sampleResult(), "csv"); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	if !strings.Contains(got, "state,total") {
		t.Errorf("csv missing header: %q", got)
	}
	if !strings.Contains(got, "TEXAS,4700.5") {
		t.Errorf("csv missing row: %q", got)
	}
}

func TestPrintJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := printResult(&buf, sampleResult(), "json"); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	if !strings.Contains(got, `"state": "TEXAS"`) {
		t.Errorf("json missing field: %q", got)
	}
	if !strings.Contains(got, `"total": 4700.5`) {
		t.Errorf("json missing value: %q", got)
	}
}

func TestPrintTable(t *testing.T) {
	var buf bytes.Buffer
	if err := printResult(&buf, sampleResult(), "table"); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	if !strings.Contains(got, "state") || !strings.Contains(got, "TEXAS") {
		t.Errorf("table missing content: %q", got)
	}
	if !strings.Contains(got, "----") {
		t.Errorf("table missing separator: %q", got)
	}
}

func TestPrintUnknownFormat(t *testing.T) {
	var buf bytes.Buffer
	if err := printResult(&buf, sampleResult(), "xml"); err == nil {
		t.Error("expected error for unknown format")
	}
}

// ---- run() dispatch tests ----

func TestRunValidate(t *testing.T) {
	if err := run([]string{"validate", `events | take 1`}); err != nil {
		t.Errorf("validate(valid) error: %v", err)
	}
	// invalid query → validate prints diagnostics, returns no Go error
	if err := run([]string{"validate", `events | where`}); err != nil {
		t.Errorf("validate(invalid) should not return Go error, got: %v", err)
	}
}

func TestRunExplain(t *testing.T) {
	if err := run([]string{"explain", `events | where x > 0 | take 1`}); err != nil {
		t.Errorf("explain error: %v", err)
	}
}

func TestRunMissingQuery(t *testing.T) {
	if err := run([]string{"-d", ":memory:"}); err == nil {
		t.Error("expected error for missing query")
	}
}

func TestRunMissingDSN(t *testing.T) {
	if err := run([]string{`events`}); err == nil {
		t.Error("expected error for missing -d")
	}
}

func TestRunHelp(t *testing.T) {
	if err := run([]string{"--help"}); err != nil {
		t.Errorf("help error: %v", err)
	}
}

// ---- end-to-end CLI run against a shared in-memory DB ----

func TestRunQuerySharedInMemory(t *testing.T) {
	const dsn = "file:kql_cli_e2e?mode=memory&cache=shared"

	seed, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer seed.Close()
	seed.Exec(`CREATE TABLE IF NOT EXISTS events (id INTEGER PRIMARY KEY, state TEXT, damage REAL)`)
	seed.Exec(`DELETE FROM events`)
	for _, r := range []struct {
		id int64
		st string
		d  float64
	}{
		{1, "TEXAS", 1500}, {2, "TEXAS", 3200}, {3, "OKLAHOMA", 500}, {4, "FLORIDA", 9000},
	} {
		seed.Exec(`INSERT INTO events VALUES (?,?,?)`, r.id, r.st, r.d)
	}

	// Run via the CLI's own query path (opens a fresh sqlite.New on the dsn).
	err = runQuery(context.Background(), dsn, "csv",
		`events | summarize total = sum(damage) by state | sort by total desc | take 1`)
	if err != nil {
		t.Fatalf("runQuery error: %v", err)
	}
	// We don't assert on stdout here (printed to os.Stdout); the formatter unit
	// tests above cover output correctness. This test verifies the full dispatch
	// + execute path runs without error against real data.

	// Also exercise the direct API to confirm the result is correct end-to-end.
	bk, err := sqlite.New(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer bk.Close()
	res, err := kql.ExecOn(context.Background(), bk,
		`events | summarize total = sum(damage) by state | sort by total desc | take 1`)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows()) != 1 || res.Rows()[0][0] != "FLORIDA" {
		t.Errorf("top row = %v, want [FLORIDA ...]", res.Rows())
	}
}

var _ = io.Copy
