package cost

import "nzinfo/kql/internal/optimizer/stats"

// Cost is the per-plan cost vector (DESIGN.md §6.3). Components are kept
// separate so different backends can weight them differently (pg cares about
// Net; sqlite is single-machine IO-bound; duckdb is columnar CPU-bound).
type Cost struct {
	IO  float64 // sequential/random page reads scaled by selectivity
	CPU float64 // tuple-processing work scaled by row count
	Net float64 // bytes pulled to the client (pg remote path)
	Mem float64 // sort/hash-table footprint
}

// Add returns the component-wise sum of two costs.
func (c Cost) Add(o Cost) Cost {
	return Cost{IO: c.IO + o.IO, CPU: c.CPU + o.CPU, Net: c.Net + o.Net, Mem: c.Mem + o.Mem}
}

// Scale multiplies all components by a factor (e.g. by row count).
func (c Cost) Scale(f float64) Cost {
	return Cost{IO: c.IO * f, CPU: c.CPU * f, Net: c.Net * f, Mem: c.Mem * f}
}

// CostWeights turns a Cost vector into a single comparable scalar. Each backend
// has a default; a catalog's cost_model can override (O0 CostModel → weights).
type CostWeights struct {
	IO  float64
	CPU float64
	Net float64
	Mem float64
}

// Total returns the weighted sum, the scalar used to compare plans.
func (c Cost) Total(w CostWeights) float64 {
	return c.IO*w.IO + c.CPU*w.CPU + c.Net*w.Net + c.Mem*w.Mem
}

// DefaultWeights returns the conventional default weights for a backend
// (O1.S4). These are starting points; the catalog's cost_model overrides them.
func DefaultWeights(backend string) CostWeights {
	switch backend {
	case "pg", "postgres":
		// remote: Net matters (pulling bytes over the wire)
		return CostWeights{IO: 1.0, CPU: 0.01, Net: 1.5, Mem: 0.1}
	case "duckdb":
		// columnar, in-process: CPU dominates
		return CostWeights{IO: 0.5, CPU: 1.0, Net: 0.0, Mem: 0.3}
	case "sqlite":
		// single-machine, row-oriented: IO dominates, no Net
		return CostWeights{IO: 1.0, CPU: 0.05, Net: 0.0, Mem: 0.1}
	}
	// unknown backend: neutral
	return CostWeights{IO: 1.0, CPU: 0.05, Net: 0.5, Mem: 0.1}
}

// WeightsFromCatalog derives CostWeights from a catalog's cost_model, falling
// back to the backend default when fields are zero/absent. backend is the
// dialect name ("pg"/"duckdb"/"sqlite") used for the default.
func WeightsFromCatalog(c *stats.Catalog, backend string) CostWeights {
	w := DefaultWeights(backend)
	if c == nil || c.CostModel == nil {
		return w
	}
	cm := c.CostModel
	if cm.SeqPageCost > 0 {
		w.IO = cm.SeqPageCost
	}
	if cm.CPUTupleCost > 0 {
		w.CPU = cm.CPUTupleCost
	}
	if cm.NetCost > 0 {
		w.Net = cm.NetCost
	}
	return w
}

// Confidence flags for degradation (O1.S5). A LowConfidence plan is one whose
// selectivity estimates relied heavily on defaults (catalog missing/weak); the
// decision policy may prefer a conservative path for such plans.
type Confidence int

const (
	HighConfidence Confidence = iota // stats present, estimates trustworthy
	LowConfidence                    // mostly defaults; treat cautiously
)

// EstimateConfidence returns High if the catalog has core stats for the table,
// Low otherwise (O1.S5). Lets the decision policy pick conservative plans.
func EstimateConfidence(c *stats.Catalog, table string) Confidence {
	if c == nil || c.Tables == nil {
		return LowConfidence
	}
	if c.Confidence(table) >= 0.5 {
		return HighConfidence
	}
	return LowConfidence
}
