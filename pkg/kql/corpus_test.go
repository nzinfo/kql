// Package corpus_test runs the real-world KQL corpus (extracted from the
// kql-parser reference's fuzz_corpus_test.go — Microsoft Sentinel / Defender
// queries) through the full parse → translate → sqlite-emit pipeline and
// reports the coverage. This is the project's parser/translator regression
// guard (T1–T3): any query the corpus can parse, we must keep parsing.
//
// A query "passes" if it parses AND translates AND emits SQL without errors.
// It does NOT need to execute correctly against a real schema (these queries
// reference Sentinel tables we don't have) — we only validate the translation
// surface here. Execution correctness is covered by pkg/kql/e2e_test.go.
//
// The pass/fail tallies are logged but not asserted at 100% (the corpus uses
// features beyond P0: parse/mv-expand/make-series/graph/externaldata/...).
// The test asserts the P0-subset pass rate stays high and never regresses.
package kql_test

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"nzinfo/kql/internal/backend/sqlite"
	"nzinfo/kql/internal/frontend/diagnostic"
	"nzinfo/kql/internal/frontend/parser"
	"nzinfo/kql/internal/ir"
)

// corpusDir holds the extracted real-world queries (see testdata/corpus/README).
const corpusDir = "testdata/corpus/sentinel"

// stageResult records where in the pipeline a query failed (or that it passed).
type stageResult int

const (
	stagePassed stageResult = iota
	stageParseFailed
	stageTranslateFailed
	stageEmitFailed
)

// runCorpusQuery parses → translates → emits a single query, returning the
// stage where it first failed (or stagePassed).
func runCorpusQuery(t *testing.T, src string) (stageResult, *diagnostic.List) {
	t.Helper()
	p := parser.New("corpus", src)
	_ = p.Parse()
	var diags diagnostic.List
	pDiags := p.Diagnostics()
	for _, d := range pDiags.Items() {
		diags.Add(d)
	}
	if pDiags.HasErrors() {
		return stageParseFailed, &diags
	}
	// translate: re-parse to get a fresh ast (Diagnostics() consumed above).
	p2 := parser.New("corpus", src)
	script := p2.Parse()
	pipe := ir.Translate(script, &diags)
	if diags.HasErrors() {
		return stageTranslateFailed, &diags
	}
	if pipe == nil {
		return stageTranslateFailed, &diags
	}
	if _, err := sqlite.Emit(pipe); err != nil {
		return stageEmitFailed, &diags
	}
	return stagePassed, &diags
}

// loadCorpus reads every *.kql file in dir, returning name→query sorted by name.
func loadCorpus(t *testing.T, dir string) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Skipf("corpus not present (%v) — skipping corpus coverage test", err)
		return nil
	}
	out := map[string]string{}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".kql") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		out[e.Name()] = string(data)
	}
	if len(out) == 0 {
		t.Skipf("no .kql files in %s", dir)
	}
	return out
}

// TestCorpusCoverage runs the full corpus through the pipeline and logs the
// pass/fail breakdown by stage + the specific failing queries. It ASSERTS that
// the overall pass rate never regresses below the recorded baseline; bumps to
// the baseline are expected as P1/P2 features land.
func TestCorpusCoverage(t *testing.T) {
	corpus := loadCorpus(t, corpusDir)
	names := make([]string, 0, len(corpus))
	for n := range corpus {
		names = append(names, n)
	}
	sort.Strings(names)

	stageCounts := [4]int{}
	type fail struct{ name string; stage stageResult; msg string }
	var fails []fail
	for _, name := range names {
		stage, _ := runCorpusQuery(t, corpus[name])
		stageCounts[stage]++
		if stage != stagePassed {
			fails = append(fails, fail{name, stage, stageName(stage)})
		}
	}
	total := len(corpus)
	passed := stageCounts[stagePassed]

	t.Logf("Corpus coverage: %d/%d passed (%.0f%%)", passed, total, 100*float64(passed)/float64(total))
	t.Logf("  parse failed:     %d", stageCounts[stageParseFailed])
	t.Logf("  translate failed: %d", stageCounts[stageTranslateFailed])
	t.Logf("  emit failed:      %d", stageCounts[stageEmitFailed])

	// Log the failing queries grouped by stage, for triage.
	if len(fails) > 0 {
		t.Logf("--- failing queries ---")
		for _, f := range fails {
			t.Logf("  [%s] %s", f.msg, f.name)
		}
	}

	// Regression baseline: 100% — all 89 real-world queries parse+translate+emit
	// clean. Any regression that drops this is a real parser/translator bug.
	const minPassRate = 1.0
	if got := float64(passed) / float64(total); got < minPassRate {
		t.Errorf("corpus pass rate %.0f%% below baseline %.0f%% — regression or corpus grew; investigate failing queries", 100*got, 100*minPassRate)
	}
}

func stageName(s stageResult) string {
	switch s {
	case stageParseFailed:
		return "parse"
	case stageTranslateFailed:
		return "translate"
	case stageEmitFailed:
		return "emit"
	}
	return "?"
}

// TestCorpusP0Subset runs only the queries that DON'T use P1+ operators (parse,
// mv-expand, make-series, externaldata, graph-*, invoke, evaluate, scan, fork,
// declare, datatable, render, plugin calls, function-form let, ...). This set
// must be ~100% — any failure here is a real P0 parser/translator bug.
func TestCorpusP0Subset(t *testing.T) {
	corpus := loadCorpus(t, corpusDir)
	// P1+ operator keywords/constructs that our parser doesn't handle yet. A
	// query using any of these is excluded from the P0 subset. (Keep this list
	// accurate — over-excluding hides real coverage; under-excluding reports
	// false "P0 bugs". Grows as P1/P2 lands.)
	p1plus := []string{
		"parse ", "parse-where", "parse-kv", "mv-expand", "mv-apply",
		"make-series", "externaldata", "graph-", "invoke ", "evaluate ",
		"scan ", "fork ", "declare ", "datatable(", "datatable (",
		"render ", "| as ", "macro-expand", "facet ", "top-nested",
		"sample-distinct", "top-hitters", "reduce ",
		// operators that are standalone stages with no P0 handling
		"| consume", "| getschema", "| serialize", "| partition ",
		"| lookup ", "| bagunpack", "| narrow",
	}
	// function-form let (let f(x)=...) and multi-statement pipelines with
	// complex let bodies are P1; flag them loosely by "let " + "(" + "=" pattern.
	// We keep this conservative (better to under-exclude than over-claim).
	names := make([]string, 0)
	for n, q := range corpus {
		lower := strings.ToLower(q)
		if containsAny(lower, p1plus) {
			continue
		}
		names = append(names, n)
	}
	sort.Strings(names)
	if len(names) == 0 {
		t.Skip("no P0-subset queries in corpus")
	}

	passed := 0
	var failed []string
	for _, name := range names {
		stage, _ := runCorpusQuery(t, corpus[name])
		if stage == stagePassed {
			passed++
		} else {
			failed = append(failed, name)
		}
	}
	t.Logf("P0-subset coverage: %d/%d passed", passed, len(names))
	if len(failed) > 0 {
		t.Logf("--- P0 failures (should be ~0; these are real bugs) ---")
		for _, n := range failed {
			t.Logf("  %s", n)
		}
	}
	// P0 subset should be near-total; allow a small margin for genuinely exotic
	// constructs (e.g. deeply nested let, unusual literals) but flag regressions.
	if got := float64(passed) / float64(len(names)); got < 0.90 {
		t.Errorf("P0-subset pass rate %.0f%% below 90%% — real parser/translator bugs in: %v", 100*got, failed)
	}
}

// containsAny reports whether s contains any of the substrings.
func containsAny(s string, subs []string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
