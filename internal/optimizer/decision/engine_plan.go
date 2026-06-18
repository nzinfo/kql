// Package decision — Engine-level AltPlan (Step 3).
//
// Extends the optimizer to decide which ENGINE executes each pipeline segment,
// not just which join method (O4). The EngineAltPlan enumerates engine
// candidates per segment (pg/DuckDB/client) and uses the cost model to pick.
//
// This builds on the EngineRouter (Step 2) but makes the decision cost-based
// rather than heuristic. The cost model considers:
//   - pg: indexed scan advantage (low IO for selective filters), but row-based
//   - DuckDB: vectorized aggregation advantage (10-100× on large data), but
//     no indexes (full scan unless data is pre-filtered)
//   - client: zero transfer cost, but no parallelism
//
// The key tradeoff: moving data between engines has a cost proportional to row
// count (Arrow conversion). So the router must ensure the cheaper engine's
// advantage exceeds the transfer cost.
package decision

import (
	"fmt"
	"strings"

	"nzinfo/kql/internal/ir"
	"nzinfo/kql/internal/optimizer/cost"
	"nzinfo/kql/internal/optimizer/stats"
)

// EngineKind identifies an execution engine.
type EngineKind int

const (
	EnginePg     EngineKind = iota // PostgreSQL (indexed, row-based)
	EngineDuckDB                   // DuckDB (vectorized, columnar)
	EngineClient                   // client-side Go (zero transfer)
)

func (e EngineKind) String() string {
	switch e {
	case EnginePg:
		return "pg"
	case EngineDuckDB:
		return "duckdb"
	case EngineClient:
		return "client"
	}
	return "unknown"
}

// EngineSegment is one pipeline segment with an engine assignment.
type EngineSegment struct {
	Engine EngineKind
	Stages []ir.Stage
	Cost   cost.Cost
	Reason string
}

// EnginePlan decides engine assignment for each pipeline segment.
type EnginePlan struct {
	Catalog *stats.Catalog
	Weights cost.CostWeights

	// HasDuckDB controls whether DuckDB is a candidate. When false, everything
	// stays on pg (single-engine mode).
	HasDuckDB bool
}

// Plan splits the pipeline into segments and assigns each to an engine.
// Returns the segments + a Decision for Explain.
func (p EnginePlan) Plan(pipe *ir.Pipeline) ([]EngineSegment, Decision) {
	if pipe == nil || p.Catalog == nil {
		return []EngineSegment{{Engine: EnginePg, Stages: pipe.Stages}},
			Decision{PolicyName: "EnginePlan", Choice: "EngineRoute", Reason: "no catalog → default pg"}
	}

	// Find the aggregate boundary (the natural split point).
	aggIdx := -1
	for i, st := range pipe.Stages {
		if _, ok := st.(*ir.Aggregate); ok {
			aggIdx = i
			break
		}
	}

	if aggIdx < 0 || !p.HasDuckDB {
		// No aggregate or no DuckDB → everything on pg.
		c := p.estimateSegmentCost(pipe.Stages, pipe.Source, EnginePg)
		return []EngineSegment{{Engine: EnginePg, Stages: pipe.Stages, Cost: c,
				Reason: "no aggregate or no DuckDB → pg"}},
			Decision{PolicyName: "EnginePlan", Choice: "EngineRoute",
				Reason: fmt.Sprintf("single engine pg (cost=%.2f)", c.Total(p.Weights))}
	}

	// Split: pre-aggregate → pg, aggregate+post → best engine.
	preStages := pipe.Stages[:aggIdx]
	postStages := pipe.Stages[aggIdx:]

	preCost := p.estimateSegmentCost(preStages, pipe.Source, EnginePg)

	// Cost the aggregate segment on both engines.
	duckCost := p.estimateSegmentCost(postStages, pipe.Source, EngineDuckDB)
	pgCost := p.estimateSegmentCost(postStages, pipe.Source, EnginePg)

	// Add transfer cost: pg→DuckDB Arrow conversion (proportional to pre-seg
	// output rows). Only applies if we switch engines.
	transferCost := p.transferCost(preStages, pipe.Source)

	var postEngine EngineKind
	var postReason string
	if duckCost.Total(p.Weights)+transferCost.Total(p.Weights) < pgCost.Total(p.Weights) {
		postEngine = EngineDuckDB
		postReason = fmt.Sprintf("DuckDB vectorized agg (cost=%.2f + transfer=%.2f < pg=%.2f)",
			duckCost.Total(p.Weights), transferCost.Total(p.Weights), pgCost.Total(p.Weights))
	} else {
		postEngine = EnginePg
		postReason = fmt.Sprintf("pg (cost=%.2f <= DuckDB %.2f + transfer %.2f)",
			pgCost.Total(p.Weights), duckCost.Total(p.Weights), transferCost.Total(p.Weights))
	}

	segments := []EngineSegment{
		{Engine: EnginePg, Stages: preStages, Cost: preCost,
			Reason: "pre-aggregate filter/sort → pg indexed scan"},
		{Engine: postEngine, Stages: postStages, Cost: duckCost,
			Reason: postReason},
	}
	if postEngine == EnginePg {
		segments[1].Cost = pgCost
	}

	// Build decision summary.
	totalCost := preCost.Total(p.Weights) + segments[1].Cost.Total(p.Weights)
	return segments, Decision{
		PolicyName: "EnginePlan",
		Choice:     "EngineRoute",
		Reason:     fmt.Sprintf("pg(%d stages)→%s(%d stages) total=%.2f [%s]",
			len(preStages), postEngine, len(postStages), totalCost, postReason),
	}
}

// estimateSegmentCost roughly costs a segment on a given engine.
func (p EnginePlan) estimateSegmentCost(stages []ir.Stage, source ir.Source, engine EngineKind) cost.Cost {
	est := cost.NewEstimator(p.Catalog)
	srcTable := ""
	if st, ok := source.(*ir.SourceTable); ok {
		srcTable = st.Table
	}
	srcCard := tableRowCount(p.Catalog, srcTable)

	total := cost.Cost{}
	card := srcCard
	for _, st := range stages {
		switch s := st.(type) {
		case *ir.Filter:
			sel := est.Selectivity(srcTable, s.Predicate)
			out := int64(float64(card) * sel)
			// pg: indexed scan advantage (low IO if selective). DuckDB: full scan.
			if engine == EnginePg && sel < 0.1 {
				total.IO += float64(out) * 4.0 // random IO (indexed)
			} else {
				total.IO += float64(card) // full scan
			}
			total.CPU += float64(card) * 0.01
			card = out
		case *ir.Aggregate:
			keys := int64(len(s.Keys))*10 + 1
			if engine == EngineDuckDB {
				total.CPU += float64(card) * 0.001 // vectorized: 10× faster
			} else {
				total.CPU += float64(card) * 0.01 // pg: row-based hash agg
			}
			total.Mem += float64(keys) * 64 / 8192
			card = keys
		case *ir.Sort:
			if engine == EngineDuckDB {
				total.CPU += float64(card) * 0.002 // vectorized sort
			} else {
				total.CPU += float64(card) * 0.02
			}
		case *ir.Limit:
			n := limitValue2(s.Count, card)
			card = n
		case *ir.Project, *ir.Extend:
			total.CPU += float64(card) * 0.005
		}
	}
	return total
}

// transferCost estimates the cost of moving data between engines (Arrow
// conversion: proportional to row count + column count).
func (p EnginePlan) transferCost(preStages []ir.Stage, source ir.Source) cost.Cost {
	srcTable := ""
	if st, ok := source.(*ir.SourceTable); ok {
		srcTable = st.Table
	}
	srcCard := tableRowCount(p.Catalog, srcTable)
	card := srcCard
	// Estimate output rows after pre-stages (rough: assume filters halve).
	for _, st := range preStages {
		if l, ok := st.(*ir.Limit); ok {
			card = limitValue2(l.Count, card)
		}
	}
	// Arrow conversion: ~1µs per row (empirical for row→column conversion).
	return cost.Cost{CPU: float64(card) * 0.001, Net: float64(card) * 64 / 1e6}
}

func tableRowCount(c *stats.Catalog, table string) int64 {
	if c == nil || table == "" {
		return 1000
	}
	if t, ok := c.Tables[table]; ok {
		return t.RowCount
	}
	if t, ok := c.Tables[strings.ToLower(table)]; ok {
		return t.RowCount
	}
	return 1000
}

func limitValue2(e ir.Expr, input int64) int64 {
	if l, ok := e.(*ir.Lit); ok {
		if n, ok := l.Value.(int64); ok {
			if n < input {
				return n
			}
		}
	}
	return input
}
