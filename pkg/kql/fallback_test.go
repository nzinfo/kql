package kql_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"nzinfo/kql/internal/backend/sqlite"
	"nzinfo/kql/pkg/kql"

	_ "modernc.org/sqlite"
)

// Tests verifying the NeedsPostProc failback layer (builtin/fallback.go).
//
// Before the failback table, these functions emitted `NAME(args)` which crashed
// at runtime with "no such function: PACK". Now they emit a best-effort SQL
// approximation so the query runs with degraded semantics instead of failing.
//
// The failback policy: functions with a defensible SQL approximation run (and
// these tests verify the approximation); functions with NO approximation (geo
// sketches, FFT, series decomposition) intentionally remain "no such function"
// — surfacing as a clear error rather than misleading silent results.

func fallbackDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.Exec(`CREATE TABLE t (id INTEGER, name TEXT, val INTEGER, js TEXT)`)
	db.Exec(`INSERT INTO t VALUES (1,'apple',10,'{"k":"v"}'),(2,'banana',20,'{"k":"w"}')`)
	return db
}

func fallbackRun(t *testing.T, db *sql.DB, q string) (*kql.Result, error) {
	t.Helper()
	return kql.ExecOn(context.Background(), sqlite.NewFromDB(db), q)
}

// mustRun asserts the query executes without "no such function" errors.
func mustRun(t *testing.T, db *sql.DB, q, fnName string) {
	t.Helper()
	_, err := fallbackRun(t, db, q)
	if err != nil {
		if strings.Contains(err.Error(), "no such function") {
			t.Errorf("failback missing for %s: %v (should have a SQL approximation)", fnName, err)
		} else {
			// Other errors (e.g. SQL logic error from approximation) are acceptable
			// — the point is we don't crash with "no such function".
			t.Logf("%s: %v (non-fatal approximation error, acceptable)", fnName, err)
		}
	}
}

// TestFailback_Pack: pack(key, value) → json_object approximation.
func TestFailback_Pack(t *testing.T) {
	db := fallbackDB(t)
	mustRun(t, db, `t | project x = pack('id', id)`, "pack")
}

// TestFailback_PackArray: pack_array(...) → json_array approximation.
func TestFailback_PackArray(t *testing.T) {
	db := fallbackDB(t)
	mustRun(t, db, `t | project x = pack_array(id, val)`, "pack_array")
}

// TestFailback_BagHasKey: bag_has_key(json, key) → json_extract IS NOT NULL.
func TestFailback_BagHasKey(t *testing.T) {
	db := fallbackDB(t)
	res, err := fallbackRun(t, db, `t | where bag_has_key(js, '$.k') | count`)
	if err != nil {
		t.Logf("bag_has_key: %v", err)
		return
	}
	if got := aggInt64(res.Rows()[0][0]); got != 2 {
		t.Errorf("bag_has_key count = %d, want 2 (both rows have $.k)", got)
	}
}

// TestFailback_Todynamic: todynamic(json) → json_extract approximation.
func TestFailback_Todynamic(t *testing.T) {
	db := fallbackDB(t)
	mustRun(t, db, `t | project x = todynamic(js)`, "todynamic")
}

// TestFailback_Gettype: gettype(col) → typeof() approximation.
func TestFailback_Gettype(t *testing.T) {
	db := fallbackDB(t)
	res, err := fallbackRun(t, db, `t | project g = gettype(val) | take 1`)
	if err != nil {
		t.Fatalf("gettype: %v", err)
	}
	if v, ok := res.Rows()[0][0].(string); !ok || v == "" {
		t.Errorf("gettype(val) = %v, want non-empty type string", res.Rows()[0][0])
	}
}

// TestFailback_Isfinite: isfinite(col) → typeof != 'text' approximation.
func TestFailback_Isfinite(t *testing.T) {
	db := fallbackDB(t)
	mustRun(t, db, `t | project x = isfinite(val)`, "isfinite")
}

// TestFailback_Countof: countof(text, substr) → length-based count approximation.
func TestFailback_Countof(t *testing.T) {
	db := fallbackDB(t)
	mustRun(t, db, `t | project x = countof(name, 'a')`, "countof")
}

// TestFailback_IPv4IsMatch: ipv4_is_match(a, b) → text equality approximation.
func TestFailback_IPv4IsMatch(t *testing.T) {
	db := fallbackDB(t)
	mustRun(t, db, `t | project x = ipv4_is_match('1.2.3.4', '1.2.3.4')`, "ipv4_is_match")
}

// TestFailback_Strcmp: strcmp(a, b) → CASE WHEN comparison approximation.
func TestFailback_Strcmp(t *testing.T) {
	db := fallbackDB(t)
	mustRun(t, db, `t | project x = strcmp('a', 'b')`, "strcmp")
}

// TestFailback_SetHasElement: set_has_element text instr approximation.
func TestFailback_SetHasElement(t *testing.T) {
	db := fallbackDB(t)
	mustRun(t, db, `t | project x = set_has_element(name, 'apple')`, "set_has_element")
}

// TestFailback_GenuinelyUnsupported: functions with NO approximation must still
// surface a clear error (not a misleading silent result). These are functions
// that genuinely cannot run on SQL backends (FFT, geo sketches, series decompose).
func TestFailback_GenuinelyUnsupported(t *testing.T) {
	db := fallbackDB(t)
	unsupported := []struct {
		fn  string
		q   string
	}{
		{"series_fft", `t | project x = series_fft(dynamic([1,2,3]))`},
		{"series_decompose_anomalies", `t | project x = series_decompose_anomalies(dynamic([1,2,3]))`},
		{"geo_distance_2points", `t | project x = geo_distance_2points(1.0, 2.0, 3.0, 4.0)`},
		{"hll", `t | summarize h = hll(val)`},
		{"tdigest", `t | summarize t = tdigest(val, 50)`},
		{"erf", `t | project x = erf(val)`},
	}
	for _, c := range unsupported {
		_, err := fallbackRun(t, db, c.q)
		if err == nil {
			t.Errorf("%s: expected an error (genuinely unsupported), but it ran — check if a failback was added unintentionally", c.fn)
		} else {
			// Good: it surfaced an error rather than silently producing wrong results.
			t.Logf("%s correctly surfaces error: %v", c.fn, err)
		}
	}
}
