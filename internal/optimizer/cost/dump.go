// Package cost — pipeline cost dump (O5.S1).
//
// Dump renders a pipeline with per-stage cost annotations for Explain output.
// Each stage gets its estimated output cardinality + the cost of evaluating it.
// This gives users visibility into where the query spends resources — the
// "before/after cost numbers" that O3.S6's Explain shows.
package cost

import (
	"fmt"
	"strings"

	"nzinfo/kql/internal/ir"
	"nzinfo/kql/internal/optimizer/stats"
)

// StageCost is the cost annotation for one pipeline stage.
type StageCost struct {
	StageKind  string  // "filter", "join", etc.
	OutputCard int64   // estimated output rows
	Cost       Cost    // the stage's cost vector
	Note       string  // optional explanation (e.g. "predicate selectivity=0.1")
}

// Dump computes per-stage costs and returns a human-readable annotated pipeline.
// Requires a catalog for selectivity estimates; nil catalog → uniform estimates.
func Dump(pipe *ir.Pipeline, catalog *stats.Catalog, weights CostWeights) string {
	if pipe == nil {
		return "(nil pipeline)"
	}
	est := NewEstimator(catalog)
	var sb strings.Builder
	sb.WriteString("Pipeline cost dump:\n")

	// Source cardinality.
	srcCard := sourceCardinality(catalog, pipe)
	fmt.Fprintf(&sb, "  source: %s (est. %d rows)\n", sourceLabel(pipe), srcCard)

	card := srcCard
	for _, st := range pipe.Stages {
		sc := stageCost(est, catalog, st, card, weights)
		fmt.Fprintf(&sb, "    %s: out=%d cost=%.2f", sc.StageKind, sc.OutputCard, sc.Cost.Total(weights))
		if sc.Note != "" {
			fmt.Fprintf(&sb, " [%s]", sc.Note)
		}
		sb.WriteString("\n")
		card = sc.OutputCard
	}
	return sb.String()
}

func sourceLabel(pipe *ir.Pipeline) string {
	if pipe == nil || pipe.Source == nil {
		return "(none)"
	}
	if st, ok := pipe.Source.(*ir.SourceTable); ok {
		return "table:" + st.Table
	}
	return "expr"
}

func sourceCardinality(catalog *stats.Catalog, pipe *ir.Pipeline) int64 {
	if catalog == nil || pipe == nil || pipe.Source == nil {
		return 1000 // default assumption
	}
	if st, ok := pipe.Source.(*ir.SourceTable); ok {
		if t, ok := catalog.Tables[st.Table]; ok {
			return t.RowCount
		}
		if t, ok := catalog.Tables[strings.ToLower(st.Table)]; ok {
			return t.RowCount
		}
	}
	return 1000
}

func stageCost(est *Estimator, catalog *stats.Catalog, st ir.Stage, inputCard int64, w CostWeights) StageCost {
	switch s := st.(type) {
	case *ir.Filter:
		sel := est.Selectivity("", s.Predicate)
		out := int64(float64(inputCard) * sel)
		return StageCost{
			StageKind:  "filter",
			OutputCard: out,
			Cost:       Cost{CPU: float64(inputCard) * defaultCPUTuple},
			Note:       fmt.Sprintf("sel=%.4f", sel),
		}
	case *ir.Limit:
		n := limitValue(s.Count, inputCard)
		return StageCost{
			StageKind:  "limit",
			OutputCard: n,
			Cost:       Cost{IO: 1},
		}
	case *ir.Join:
		rightCard := joinRightCard(catalog, s)
		sel := est.JoinSelectivity("", "", s.On, inputCard, rightCard)
		out := est.OutputCardinality("", "", s.On, inputCard, rightCard)
		return StageCost{
			StageKind:  "join",
			OutputCard: out,
			Cost:       Cost{CPU: float64(inputCard + rightCard) * defaultCPUTuple, Mem: float64(rightCard) / 8192},
			Note:       fmt.Sprintf("sel=%.4f inner=%d", sel, rightCard),
		}
	case *ir.Aggregate:
		keyCard := aggregateKeyCard(catalog, s)
		return StageCost{
			StageKind:  "aggregate",
			OutputCard: keyCard,
			Cost:       Cost{CPU: float64(inputCard) * defaultCPUTuple, Mem: float64(keyCard) * 64 / 8192},
		}
	case *ir.Sort:
		return StageCost{
			StageKind:  "sort",
			OutputCard: inputCard,
			Cost:       Cost{CPU: float64(inputCard) * defaultCPUTuple * 2},
			Note:       "O(n log n)",
		}
	case *ir.Project, *ir.Extend:
		return StageCost{
			StageKind:  irStageKind(st),
			OutputCard: inputCard,
			Cost:       Cost{CPU: float64(inputCard) * defaultCPUTuple * 0.5},
		}
	case *ir.Distinct:
		return StageCost{
			StageKind:  "distinct",
			OutputCard: inputCard / 2, // rough estimate
			Cost:       Cost{CPU: float64(inputCard) * defaultCPUTuple, Mem: float64(inputCard/2) * 64 / 8192},
		}
	}
	return StageCost{StageKind: irStageKind(st), OutputCard: inputCard, Cost: Cost{}}
}

func irStageKind(st ir.Stage) string {
	switch st.(type) {
	case *ir.Filter:
		return "filter"
	case *ir.Project:
		return "project"
	case *ir.Extend:
		return "extend"
	case *ir.Aggregate:
		return "aggregate"
	case *ir.Join:
		return "join"
	case *ir.Sort:
		return "sort"
	case *ir.Limit:
		return "limit"
	case *ir.Distinct:
		return "distinct"
	case *ir.Union:
		return "union"
	}
	return fmt.Sprintf("%T", st)
}

func joinRightCard(catalog *stats.Catalog, j *ir.Join) int64 {
	if catalog == nil || j == nil || j.Right == nil || j.Right.Source == nil {
		return 1000
	}
	if st, ok := j.Right.Source.(*ir.SourceTable); ok {
		if t, ok := catalog.Tables[st.Table]; ok {
			return t.RowCount
		}
		if t, ok := catalog.Tables[strings.ToLower(st.Table)]; ok {
			return t.RowCount
		}
	}
	return 1000
}

func aggregateKeyCard(catalog *stats.Catalog, a *ir.Aggregate) int64 {
	if a == nil {
		return 1
	}
	return int64(len(a.Keys))*10 + 1 // rough: ~10 groups per key
}

func limitValue(e ir.Expr, input int64) int64 {
	if l, ok := e.(*ir.Lit); ok {
		if n, ok := l.Value.(int64); ok {
			if n < input {
				return n
			}
		}
	}
	return input
}

const defaultCPUTuple = 0.01
