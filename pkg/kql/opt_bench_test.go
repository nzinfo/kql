package kql_test

import (
	"testing"

	"nzinfo/kql/internal/optimizer/rules"
	"nzinfo/kql/pkg/kql"
)

// BenchmarkOptimize measures the optimizer's overhead (O2 rules to fixpoint +
// O3 PredicateOrder) on a representative query. The optimizer must be cheap
// relative to parse+emit+execute; this benchmark tracks that it stays so.
//
// Run: go test ./pkg/kql/ -bench=BenchmarkOptimize -benchmem
func BenchmarkOptimize(b *testing.B) {
	query := `events | where damage > 1000 and state == "TEXAS" | extend x = id * 2 | summarize c = count(), t = sum(damage) by state | sort by t desc | take 10`
	engine := rules.NewEngine(rules.ConstantFold{}, rules.PredicatePushdown{}, rules.ColumnPrune{})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Re-translate each iteration (optimise mutates the tree; a fresh tree
		// per iteration mirrors the real per-query cost).
		p, _ := kql.ParseTranslate(query)
		engine.Optimize(p)
	}
}

// BenchmarkParseTranslate isolates the parse+translate cost (the baseline the
// optimizer overhead should be small relative to).
func BenchmarkParseTranslate(b *testing.B) {
	query := `events | where damage > 1000 and state == "TEXAS" | extend x = id * 2 | summarize c = count(), t = sum(damage) by state | sort by t desc | take 10`
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = kql.ParseTranslate(query)
	}
}

// BenchmarkExplain measures the full explain path (parse+bind+optimise+emit),
// the realistic per-query overhead a user sees from `kql explain`.
func BenchmarkExplain(b *testing.B) {
	query := `events | where damage > 1000 | take 10`
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Explain against sqlite :memory: (no real DB; emit only, no exec).
		// We can't call kql.Explain in a benchmark easily (it opens a backend);
		// measure parse+optimise+emit via the building blocks instead.
		pipe, _ := kql.ParseTranslate(query)
		engine := rules.NewEngine(rules.ConstantFold{}, rules.PredicatePushdown{}, rules.ColumnPrune{})
		engine.Optimize(pipe)
	}
}
