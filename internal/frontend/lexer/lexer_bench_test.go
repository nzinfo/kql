package lexer

import (
	"strings"
	"testing"

	"nzinfo/kql/internal/frontend/token"
)

// benchQueries is a slice of realistic KQL queries (lightly adapted from the
// kql-parser fuzz corpus, MIT) used for throughput benchmarking. Tokenised in
// a loop to approximate F1.S6's "≥ 50% of kqlparser lexer throughput" target.
var benchQueries = []string{
	`SecurityEvent | project Computer, EventID, TimeGenerated`,
	`StormEvents | where State == "TEXAS" | take 10`,
	`T | where x > 0 | extend y = x*2 | summarize count() by y | order by y desc | take 10`,
	`T | where TimeGenerated > ago(1d) | summarize cnt = count() by bin(TimeGenerated, 1h) | render timechart`,
	`T | join kind=inner (T2) on Key | where Status has "ok" and Value !in (1,2,3) | distinct ColA, ColB`,
	`datatable(name:string, age:int)["alice", 30, "bob", 25] | where age >= 18 | order by age desc`,
	`let N = 10; T | top N by Score | where Name matches regex "(?i)^a" | extend guid_col = guid(12345678-1234-1234-1234-123456789012)`,
	`T | mv-expand Items | parse Kind with "type=" Type | where Type == "x" | summarize m = max(Value) by Type, bin(TimeGenerated, 1d)`,
}

// scanAllToEOF drains the lexer until EOF, returning the token count.
func scanAllToEOF(l *Lexer) int {
	n := 0
	for {
		tk := l.Scan()
		if tk.Type == token.EOF {
			return n
		}
		n++
	}
}

// BenchmarkLexer measures end-to-end tokenisation throughput across a mix of
// realistic KQL queries. This is the F1.S6 baseline; record results vs the
// kqlparser reference lexer into docs O5 (per docs/phases/README.md §4).
func BenchmarkLexer(b *testing.B) {
	src := strings.Join(benchQueries, "\n")
	b.SetBytes(int64(len(src)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l := New("bench.kql", src)
		_ = scanAllToEOF(l)
	}
}

// BenchmarkScanToken isolates a single Scan call's hot path.
func BenchmarkScanToken(b *testing.B) {
	src := strings.Repeat(`StormEvents | where State == "TEXAS" | take 10`+"\n", 1000)
	l := New("bench.kql", src)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if tk := l.Scan(); tk.Type == token.EOF {
			l = New("bench.kql", src)
		}
	}
}
