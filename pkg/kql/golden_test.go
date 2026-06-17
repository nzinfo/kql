// Package kql_test golden snapshot tests (T4): lock the exact SQL each
// backend emits for a representative query set, so any emit regression is
// caught at the SQL level before it reaches a DB.
//
// Golden files live in testdata/golden/<name>.{sqlite,pg}.sql. Run
//
//	go test ./pkg/kql/ -run TestGolden -update
//
// to regenerate after an intentional emit change (e.g. a new optimizer rule).
// Without -update, the test fails on any diff, showing the expected/actual.
package kql_test

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nzinfo/kql/internal/backend/pg"
	"nzinfo/kql/internal/backend/sqlite"
	"nzinfo/kql/pkg/kql"
)

var updateGolden = flag.Bool("update", false, "regenerate golden snapshot files")

// goldenCases is the representative query set. Each emits to a sqlite and a pg
// golden file. Covers P0 operators, P1 passthroughs, join $left/$right, iff,
// in~, aggregates, and the optimizer's rewrite effects (pushdown/fold/prune).
//
// Add a case when a new emit path lands; the -update flag writes its snapshot.
var goldenCases = []struct {
	name  string
	query string
}{
	{"source_only", `events`},
	{"where_eq", `events | where state == "TX"`},
	{"where_and", `events | where state == "TX" and damage > 1000`},
	{"where_in_list", `events | where state in ("TX", "FL")`},
	{"where_in_ci", `events | where state in~ ("TX", "FL")`},
	{"where_has", `events | where eventtype has "ail"`},
	{"take", `events | take 5`},
	{"project_rename", `events | project s = state, d = damage`},
	{"extend", `events | extend doubled = damage * 2`},
	{"sort", `events | sort by damage desc nulls first`},
	{"summarize_count_by", `events | summarize c = count() by state`},
	{"summarize_multi", `events | summarize cnt = count(), tot = sum(damage) by state`},
	{"distinct", `events | distinct state`},
	{"count", `events | count`},
	{"top", `events | top 3 by damage desc`},
	{"iff", `events | extend big = iff(damage > 5000, 1, 0)`},
	{"tostring", `events | extend s = tostring(damage)`},
	{"dcount", `events | summarize d = dcount(state) by eventtype`},
	// optimizer effects
	{"pushdown", `events | extend x = id * 2 | where id > 5`},
	{"constfold_tautology", `events | where 1 == 1 | take 1`},
	{"columnprune", `events | where id > 0 | project id`},
	// join with $left/$right
	{"join_qualified", `events | join kind=inner (meta) on $left.id == $right.id`},
	// P1 passthrough (parses, emits best-effort)
	{"mvexpand_passthrough", `events | mv-expand x = dynamic([1,2])`},
	{"render_nodrop", `events | take 1 | render barchart`},
}

// TestGolden emits each case through both backends and compares to the snapshot.
func TestGolden(t *testing.T) {
	for _, tc := range goldenCases {
		t.Run(tc.name, func(t *testing.T) {
			pipe, err := kql.ParseTranslate(tc.query)
			if err != nil {
				t.Fatalf("ParseTranslate(%q): %v", tc.query, err)
			}
			// sqlite snapshot
			t.Run("sqlite", func(t *testing.T) {
				q, err := sqlite.Emit(pipe)
				if err != nil {
					t.Fatalf("sqlite.Emit: %v", err)
				}
				compareGolden(t, tc.name+".sqlite.sql", normaliseSQL(q.SQL))
			})
			// pg snapshot (fresh pipe — Emit may mutate via numbered placeholders,
			// but ParseTranslate returns a fresh tree each call; re-translate for pg).
			pipePg, _ := kql.ParseTranslate(tc.query)
			t.Run("pg", func(t *testing.T) {
				q, err := pg.Emit(pipePg)
				if err != nil {
					t.Fatalf("pg.Emit: %v", err)
				}
				compareGolden(t, tc.name+".pg.sql", normaliseSQL(q.SQL))
			})
		})
	}
}

// compareGolden checks actual against testdata/golden/<file>, writing it when
// -update is set.
func compareGolden(t *testing.T, file, actual string) {
	t.Helper()
	path := filepath.Join("testdata", "golden", file)
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(actual), 0644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("golden file %s missing (run with -update to create): %v", path, err)
	}
	if string(want) != actual {
		t.Errorf("golden mismatch for %s\n--- want ---\n%s\n--- got ---\n%s", file, want, actual)
	}
}

// normaliseSQL trims trailing whitespace so editor reformatting doesn't cause
// spurious diffs.
func normaliseSQL(s string) string {
	return strings.TrimRight(s, " \n\t")
}
