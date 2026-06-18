package kql_test

import (
	"testing"

	"nzinfo/kql/pkg/kql"
)

// Round 5 network/array/series: verify parse + translate (no backend exec
// since these are NeedsPostProc; the goal is they resolve in the catalog and
// don't error at the frontend). Run against the corpus parser path.

func TestRound5_NetParseOnly(t *testing.T) {
	// These functions are NeedsPostProc; we only verify they parse + translate
	// (no KQL003 unknown-function warning) without executing.
	queries := []string{
		`t | project x = ipv4_compare('192.168.1.1', '192.168.1.0/24')`,
		`t | project x = ipv4_is_match('10.0.0.1', '10.0.0.0/8')`,
		`t | project x = ipv6_is_match('::1', '::1/128')`,
		`t | project x = has_ipv4('src 10.0.0.1', '10.0.0.1')`,
		`t | project x = format_ipv4(parse_ipv4('1.2.3.4'))`,
		`t | project x = array_concat(dynamic([1]), dynamic([2]))`,
		`t | project x = array_sort_asc(dynamic([3,1,2]))`,
		`t | project x = array_sum(dynamic([1,2,3]))`,
		`t | project x = series_add(dynamic([1,2]), dynamic([3,4]))`,
		`t | project x = series_fill_forward(dynamic([1,null,3]))`,
		`t | project x = series_fft(dynamic([1,2,3,4]))`,
		`t | project x = series_decompose_anomalies(dynamic([1,2,3,100,4]))`,
		`t | project x = series_fit_line(dynamic([1,2,3,4]))`,
		`t | project x = set_union(dynamic([1,2]), dynamic([2,3]))`,
	}
	for _, q := range queries {
		_, err := kql.ParseTranslate(q)
		if err != nil {
			t.Errorf("ParseTranslate(%q): %v", q, err)
		}
	}
}

