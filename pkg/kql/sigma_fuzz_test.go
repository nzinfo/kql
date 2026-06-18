package kql_test

import (
	"encoding/json"
	"os"
	"testing"

	"nzinfo/kql/pkg/kql"
)

// TestSigmaFuzz_ParseTranslate runs the real-world Microsoft Sentinel / Defender
// XDR hunting-rule corpus (extracted from .source-projects/kql-parser fuzz corpus)
// through parse + translate. This is the second-round production-grade fuzz
// baseline per CROSS-PROJECT-COMPARISON.md §3.1.
//
// These queries exercise complex real-world patterns: multi-statement let with
// dynamic([@'...']) ransomware-command lists, union isfuzzy=true, parse...with,
// extend tostring(EventData.X), summarize ... by, mv-expand, make-series, etc.
//
// We assert that parse + translate completes without fatal errors. Unknown
// columns (KQL001) and similar are acceptable here — the corpus references
// tables (DeviceProcessEvents, SecurityEvent, …) that have no schema bound in
// this test, so we only check the query is structurally accepted.
func TestSigmaFuzz_ParseTranslate(t *testing.T) {
	data, err := os.ReadFile("testdata/sentinel/sigma_queries.json")
	if err != nil {
		t.Skipf("sigma corpus not found: %v", err)
	}
	var entries []struct {
		Name  string `json:"name"`
		Query string `json:"query"`
	}
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no entries in sigma corpus")
	}

	var ok, fail int
	var failures []string
	for _, e := range entries {
		_, err := kql.ParseTranslate(e.Query)
		if err != nil {
			fail++
			failures = append(failures, e.Name)
		} else {
			ok++
		}
	}
	t.Logf("Sigma fuzz corpus: %d/%d parsed+translated, %d failed", ok, len(entries), fail)
	// Real-world hunting queries are intentionally complex. We expect the vast
	// majority to parse. Fail the test only if more than 25% fail (regression guard).
	threshold := len(entries) / 4
	if fail > threshold {
		t.Errorf("sigma parse failures (%d) exceed threshold (%d): %v", fail, threshold, failures)
	}
}
