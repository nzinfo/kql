// Package stats defines the statistics catalog used by the cost-based
// optimizer (DESIGN.md §6.2, docs/phases/optimizer/O0-stats-catalog.md).
//
// The catalog is a versioned, source-tagged description of table shapes — row
// counts, per-column cardinality/nulls/MCV/histogram, indexes, and a cost
// model. It is loaded from YAML (S3) and consumed read-only by the optimizer
// via the StatsReader interface (S5).
//
// Key design choices (O0-verification.md, gold-standard-checked):
//   - CorrVs (cross-column correlation) is OPTIONAL (*CorrVs) — PostgreSQL
//     does not expose column correlation ρ; absence is the norm, not an error.
//   - Hist.Kind is `equi_freq` by default (matches pg histogram_bounds: equal-
//     frequency, variable-width buckets), with `equi_width` reserved for
//     future sampling-based estimation.
//   - CostModel.CacheHitRate is *float64 (optional; pg has no direct mapping).
//   - Confidence scoring (confidence.go) does NOT penalise missing CorrVs
//     (it's an enhancement field); only missing core stats (card/nulls/mcv/
//     hist) lower confidence.
package stats

// Source identifies how a catalog was produced, driving the base confidence
// score (manual < sampling < pg_analyze).
type Source string

// Catalog sources.
const (
	SourceManual     Source = "manual"      // hand-written; base confidence 0.6
	SourceSampling   Source = "sampling"    // estimated by sampling; base 0.7
	SourcePgAnalyze  Source = "pg_analyze"  // from pg_stats; base 0.9
)

// baseConfidence returns the per-source confidence floor.
func (s Source) baseConfidence() float64 {
	switch s {
	case SourcePgAnalyze:
		return 0.9
	case SourceSampling:
		return 0.7
	case SourceManual:
		return 0.6
	}
	return 0.5 // unknown source
}

// Catalog is the top-level container: a schema's worth of table/view stats
// plus a cost model. Versioned + source-tagged so the optimizer can refuse a
// mismatched/old catalog.
type Catalog struct {
	Version   string                  `yaml:"version"`
	Source    Source                  `yaml:"source"`
	Schema    string                  `yaml:"schema"`
	Tables    map[string]*Table       `yaml:"tables"`
	Views     map[string]*ViewDef     `yaml:"views,omitempty"`
	CostModel *CostModel              `yaml:"cost_model,omitempty"`
}

// Table holds per-table statistics.
type Table struct {
	RowCount   int64                  `yaml:"row_count"`
	AvgRowBytes int                   `yaml:"avg_row_bytes"`
	Columns    map[string]*ColumnStats `yaml:"columns"`
	Indexes    []IndexDef             `yaml:"indexes,omitempty"`
}

// ColumnStats describes one column's distribution. Core fields (Card, Nulls,
// MCV, Hist) drive confidence; CorrVs is an optional enhancement.
type ColumnStats struct {
	Card   int64         `yaml:"card"`          // distinct value count
	Nulls  int64         `yaml:"nulls"`         // null row count
	Type   string        `yaml:"type"`          // KQL type name (string/long/...)
	MCV    *MCV          `yaml:"mcv,omitempty"` // most-common-values + freqs
	Hist   *Hist         `yaml:"hist,omitempty"`
	CorrVs *CorrVs       `yaml:"corr_vs,omitempty"` // OPTIONAL (pg doesn't expose ρ)
}

// MCV is the most-common-values distribution: parallel value/frequency slices.
type MCV struct {
	Values    []string  `yaml:"values"`
	Frequencies []float64 `yaml:"frequencies"`
}

// HistKind classifies a histogram. pg histogram_bounds is equi_freq
// (equal-frequency, variable-width buckets); equi_width is reserved for
// sampling-based equal-width bucketing.
type HistKind string

// Histogram kinds.
const (
	HistEquiFreq  HistKind = "equi_freq"
	HistEquiWidth HistKind = "equi_width"
)

// Hist is a column value histogram.
type Hist struct {
	Kind    HistKind `yaml:"kind"`
	Bounds  []string `yaml:"bounds"` // bucket boundaries (equi_freq: N+1 for N buckets)
}

// CorrVs describes a column's correlation with another (enhancement field).
// Absent in practice (pg doesn't expose ρ); present only for manually-curated
// catalogs that captured it.
type CorrVs struct {
	OtherColumn string  `yaml:"other_column"`
	Rho         float64 `yaml:"rho"` // Pearson correlation ∈ [-1, 1]
}

// IndexDef describes one index.
type IndexDef struct {
	Name    string   `yaml:"name"`
	Columns []string `yaml:"columns"` // key columns, in order
	Include []string `yaml:"include,omitempty"` // covering columns (pg INCLUDE)
	Unique  bool     `yaml:"unique,omitempty"`
}

// ViewDef describes a view (minimal; for completeness — the optimizer reads
// through views via their underlying table stats).
type ViewDef struct {
	Definition string `yaml:"definition"`
}

// CostModel holds per-backend cost constants (pg seq/random page cost etc).
// CacheHitRate is optional (no direct pg mapping).
type CostModel struct {
	SeqPageCost    float64  `yaml:"seq_page_cost"`
	RandomPageCost float64  `yaml:"random_page_cost"`
	CPUTupleCost   float64  `yaml:"cpu_tuple_cost"`
	NetCost        float64  `yaml:"net_cost,omitempty"`
	IOCost         float64  `yaml:"io_cost,omitempty"`
	CacheHitRate   *float64 `yaml:"cache_hit_rate,omitempty"`
}
