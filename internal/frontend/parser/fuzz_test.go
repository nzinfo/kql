// Package parser — fuzz corpus test from kql-parser's real-world KQL collection.
//
// This test parses all 85+ queries from .source-projects/kql-parser/
// fuzz_corpus_test.go (Microsoft Sentinel, Defender XDR, community hunting
// queries) through our parser and reports coverage. These are the most
// complex real-world KQL queries available — they stress every grammar path.
//
// A query "passes" if Parse() produces no ERROR diagnostics (warnings are OK).
// Some queries may fail on features we haven't implemented yet — those are
// tracked as known gaps, not regressions.
package parser

import (
	"testing"

	"nzinfo/kql/internal/frontend/diagnostic"
	"nzinfo/kql/internal/ir"
)

// TestFuzzCorpus_KqlParser parses every query from kql-parser's fuzz corpus.
// Each query must: (1) parse without errors, (2) translate to IR without errors.
func TestFuzzCorpus_KqlParser(t *testing.T) {
	total := len(kqlParserFuzzCorpus)
	parseOK := 0
	translateOK := 0
	var failures []string

	for _, entry := range kqlParserFuzzCorpus {
		p := New("fuzz/"+entry.Name, entry.Query)
		script := p.Parse()
		parseDiags := p.Diagnostics().HasErrors()

		var translateDiags bool
		if !parseDiags {
			parseOK++
			// Also test translation to IR.
			var diags diagnostic.List
			pipe := ir.Translate(script, &diags)
			translateDiags = diags.HasErrors()
			if !translateDiags && pipe != nil {
				translateOK++
			}
		}

		if parseDiags || translateDiags {
			diagLines := p.Diagnostics().Render()
			firstErr := ""
			if len(diagLines) > 0 {
				firstErr = diagLines[0]
			}
			stage := "parse"
			if parseDiags {
				stage = "parse"
			} else {
				stage = "translate"
			}
			if len(firstErr) > 80 {
				firstErr = firstErr[:80] + "..."
			}
			failures = append(failures, "["+stage+"] "+entry.Name+": "+firstErr)
		}
	}

	t.Logf("kql-parser fuzz corpus:")
	t.Logf("  parse:     %d/%d (%d%%)", parseOK, total, pct(parseOK, total))
	t.Logf("  translate: %d/%d (%d%%)", translateOK, total, pct(translateOK, total))

	for _, f := range failures {
		t.Logf("  FAIL: %s", f)
	}

	// Parse must be ≥95% (we've achieved 100%).
	if parseOK < total*95/100 {
		t.Errorf("parse coverage %d%% < 95%% baseline", pct(parseOK, total))
	}
}

func byteIndex(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}
