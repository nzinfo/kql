// Package parser — kqlparser grammar alignment corpus test.
//
// Parses every query from .source-projects/kqlparser/testdata/grammar/
// (operators.kql, expressions.kql, literals.kql, statements.kql) and reports
// parse coverage. This validates against a broad, independently-maintained
// KQL grammar test suite — the gold-standard for operator/expression/statement
// coverage.
//
// The queries are separated by blank lines (each block is a standalone query).
// Comments (// ...) are skipped. A query "passes" if Parse() produces no ERROR
// diagnostics (warnings are OK — KQL's dynamic system allows loose types).
package parser

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sourceGrammarDir is the path to the kqlparser grammar test data, relative
// to the project root (the test runner sets CWD to the package dir).
const sourceGrammarDir = "../../../.source-projects/kqlparser/testdata/grammar"

// TestGrammarCorpus parses every query from the kqlparser grammar files.
func TestGrammarCorpus(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(sourceGrammarDir, "*.kql"))
	if err != nil || len(files) == 0 {
		t.Skipf("kqlparser grammar dir not found at %s", sourceGrammarDir)
	}

	totalQueries := 0
	passed := 0
	var failures []string

	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Logf("skip %s: %v", filepath.Base(file), err)
			continue
		}
		queries := splitKQLQueries(string(data))
		for i, q := range queries {
			q = strings.TrimSpace(q)
			if q == "" || strings.HasPrefix(q, "//") {
				continue
			}
			totalQueries++
			p := New(file, q)
			p.Parse()
			if !p.Diagnostics().HasErrors() {
				passed++
			} else {
				name := fmtShortName(filepath.Base(file), i)
				failures = append(failures, name+": "+firstLine(q))
			}
		}
	}

	t.Logf("Grammar corpus: %d/%d queries parsed cleanly (%d%%)",
		passed, totalQueries, pct(passed, totalQueries))
	for _, f := range failures {
		t.Logf("  FAIL: %s", f)
	}

	// We expect ≥80% coverage (some queries use constructs we parse-but-noop
	// that may still produce minor errors in nested expressions).
	if passed < totalQueries*80/100 {
		t.Errorf("grammar corpus coverage %d%% < 80%% baseline", pct(passed, totalQueries))
	}
}

// splitKQLQueries splits a .kql file into individual queries by blank lines.
// Lines starting with // are comments (attached to the next query, skipped).
func splitKQLQueries(src string) []string {
	var queries []string
	var current strings.Builder
	lines := strings.Split(src, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			// Blank line = query boundary.
			if current.Len() > 0 {
				queries = append(queries, current.String())
				current.Reset()
			}
			continue
		}
		if strings.HasPrefix(trimmed, "//") {
			continue // comment
		}
		if current.Len() > 0 {
			current.WriteString("\n")
		}
		current.WriteString(line)
	}
	if current.Len() > 0 {
		queries = append(queries, current.String())
	}
	return queries
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	if len(s) > 60 {
		return s[:60] + "..."
	}
	return s
}

func fmtShortName(file string, idx int) string {
	return file + "#" + intToStr(idx)
}

func intToStr(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}

func pct(n, total int) int {
	if total == 0 {
		return 0
	}
	return n * 100 / total
}
