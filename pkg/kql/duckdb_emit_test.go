package kql_test

import (
	"strings"
	"testing"

	"nzinfo/kql/internal/backend/duckdb"
	"nzinfo/kql/pkg/kql"
)

// Regression tests for DuckDB emit correctness of the aggregate/network
// overrides added in Phase 1-3. These guard against the ir.Expr→%s formatting
// bug (emit must produce valid SQL, not Go's "%!s(EXPECTED)" artifact) and
// verify arg-order for the *if(value, pred) family.
//
// The bug: duckdb.emitFuncCall used fmt.Sprintf("...%s...", n.Args[0], ...)
// where n.Args[i] is an ir.Expr (a Go struct), so %s rendered the struct's
// String() form instead of the emitted SQL. Fixed by routing through emitExpr.

// TestDuckDB_EmitSQL_NoFormatArtifact asserts that the generated SQL contains
// no Go-formatting artifact ("%!", "(EXPECTED)") for the aggif/ipv4 families.
func TestDuckDB_EmitSQL_NoFormatArtifact(t *testing.T) {
	queries := []string{
		`events | summarize s = sumif(damage, state == "TEXAS")`,
		`events | summarize a = avgif(damage, state == "TEXAS")`,
		`events | summarize m = maxif(damage, state == "TEXAS")`,
		`events | summarize m = minif(damage, state == "TEXAS")`,
		`events | summarize d = dcountif(damage, state == "TEXAS")`,
		`events | summarize x = arg_max(state, damage)`,
		`events | summarize x = arg_min(state, damage)`,
		`events | project x = ipv4_is_match('10.0.0.1', '10.0.0.0/8')`,
		`events | project x = ipv4_is_in_range('10.0.0.1', '10.0.0.0/8')`,
	}
	for _, q := range queries {
		pipe, err := kql.ParseTranslate(q)
		if err != nil {
			t.Fatalf("ParseTranslate(%q): %v", q, err)
		}
		bq, err := duckdb.Emit(pipe)
		if err != nil {
			t.Errorf("Emit(%q): %v", q, err)
			continue
		}
		if strings.Contains(bq.SQL, "%!") || strings.Contains(bq.SQL, "EXPECTED") {
			t.Errorf("Emit(%q): SQL contains Go formatting artifact:\n%s", q, bq.SQL)
		}
		// The aggif family must produce CASE WHEN ... THEN ... in the right order:
		// sumif(value, pred) → sum(CASE WHEN pred THEN value ...).
		if strings.Contains(q, "sumif") && !strings.Contains(bq.SQL, "CASE WHEN") {
			t.Errorf("sumif SQL missing CASE WHEN:\n%s", bq.SQL)
		}
	}
}

// TestDuckDB_AggIf_Exec verifies arg-order correctness end-to-end:
// sumif(damage, state=="TEXAS") over events {1500,3200,100}=4800 (TEXAS rows).
func TestDuckDB_AggIf_Exec(t *testing.T) {
	bk := duckSeed(t)
	res := duckRun(t, bk, `events | summarize s = sumif(damage, state == "TEXAS")`)
	if len(res.Rows()) != 1 {
		t.Fatalf("rows = %d, want 1", len(res.Rows()))
	}
	got := aggFloat64(res.Rows()[0][0])
	// TEXAS rows: 1500.0 + 3200.5 + 100.0 = 4800.5
	if got != 4800.5 {
		t.Errorf("sumif(damage, state==TEXAS) = %v, want 4800.5", got)
	}
}

// TestDuckDB_ArgMax_Exec: arg_max(state, damage) → the state with the largest damage.
func TestDuckDB_ArgMax_Exec(t *testing.T) {
	bk := duckSeed(t)
	// FLORIDA has damage 9000 (max). arg_max returns the state column.
	res := duckRun(t, bk, `events | summarize x = arg_max(state, damage)`)
	if len(res.Rows()) != 1 {
		t.Fatalf("rows = %d, want 1", len(res.Rows()))
	}
	got := res.Rows()[0][0]
	if got != "FLORIDA" {
		t.Errorf("arg_max(state, damage) = %v, want FLORIDA", got)
	}
}

// TestDuckDB_CountNoArg: count() → count(*).
func TestDuckDB_CountNoArg_Exec(t *testing.T) {
	bk := duckSeed(t)
	res := duckRun(t, bk, `events | summarize c = count()`)
	if got := aggInt64(res.Rows()[0][0]); got != 6 {
		t.Errorf("count() = %d, want 6", got)
	}
}

// TestDuckDB_LikeOp: like operator emits ILIKE.
func TestDuckDB_LikeOp_Exec(t *testing.T) {
	bk := duckSeed(t)
	res := duckRun(t, bk, `events | where state like 'T%' | count`)
	if got := aggInt64(res.Rows()[0][0]); got != 3 {
		t.Errorf("state like 'T%%' = %d, want 3 (TEXAS x3)", got)
	}
}
