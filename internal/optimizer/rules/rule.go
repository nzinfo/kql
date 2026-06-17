// Package rules implements the rule-based IR rewriter (optimizer phase 1,
// docs/phases/optimizer/O2-rules-core.md).
//
// A RewriteRule transforms a *Pipeline into an equivalent *Pipeline (changed
// = true) or leaves it (changed = false). The Engine runs a set of rules
// repeatedly to a fixpoint (until no rule reports a change), bounded by a max
// iteration count to guard against rule oscillation.
//
// All rules are dialect-agnostic: they operate on IR only, so the rewritten
// pipeline emits identically-correct SQL on every backend. O2.S1 defines the
// interface + engine; O2.S2 adds PredicatePushdown; S3/S4 add ColumnPrune and
// ConstantFold (later).
package rules

import (
	"nzinfo/kql/internal/frontend/token"
	"nzinfo/kql/internal/ir"
	"nzinfo/kql/internal/optimizer/cost"
	"nzinfo/kql/internal/optimizer/stats"
)

// StatsReader is the read-only stats accessor rules MAY consult (e.g. to pick
// which of two predicates to push first by selectivity). The O0 catalog
// implements it; a nil reader makes rules stats-blind but still safe.
// Kept as an interface here (not importing stats' StatsReader) so rules don't
// hard-depend on the stats package shape yet — O0.S5 will formalise this.
type StatsReader interface {
	// Selectivity returns an estimate of the fraction of rows a predicate on
	// the given column retains, in [0,1]. 0 = unknown (rules treat as 1.0).
	Selectivity(table, column string) float64
}

// noopReader is the default when no stats are available.
type noopReader struct{}

func (noopReader) Selectivity(table, column string) float64 { return 0 }

// RewriteRule is one IR→IR transformation.
type RewriteRule interface {
	// Name returns a short identifier for diagnostics/logging.
	Name() string
	// Apply attempts to rewrite pipe. Returns the (possibly new) pipeline and
	// changed=true if it made a change. A rule must be CONFLUENT with itself
	// (repeated application reaches a fixpoint) and preserve semantics.
	Apply(pipe *ir.Pipeline, sr StatsReader) (*ir.Pipeline, bool)
}

// Engine runs a sequence of RewriteRules to fixpoint.
type Engine struct {
	rules       []RewriteRule
	maxIter     int    // fixpoint guard (default 16)
	statsReader StatsReader
}

// NewEngine builds an engine running the given rules. maxIter defaults to 16
// if 0; the engine stops after that many passes even if not at fixpoint (and
// records a warning, since it likely indicates a rule-oscillation bug).
func NewEngine(rules ...RewriteRule) *Engine {
	return &Engine{rules: rules, maxIter: 16, statsReader: noopReader{}}
}

// WithMaxIter sets the fixpoint-iteration cap.
func (e *Engine) WithMaxIter(n int) *Engine { e.maxIter = n; return e }

// WithStats injects a stats reader (enables selectivity-aware rewrites).
func (e *Engine) WithStats(sr StatsReader) *Engine {
	if sr != nil {
		e.statsReader = sr
	}
	return e
}

// Optimize runs the rules to fixpoint and returns the rewritten pipeline plus
// the number of rule applications that changed the IR. The pipeline is
// mutated in place; the same pointer is returned for convenience.
func (e *Engine) Optimize(pipe *ir.Pipeline) (*ir.Pipeline, int) {
	if pipe == nil || len(e.rules) == 0 {
		return pipe, 0
	}
	totalChanges := 0
	for iter := 0; iter < e.maxIter; iter++ {
		anyChanged := false
		for _, r := range e.rules {
			var changed bool
			pipe, changed = r.Apply(pipe, e.statsReader)
			if changed {
				totalChanges++
				anyChanged = true
			}
		}
		if !anyChanged {
			return pipe, totalChanges // fixpoint reached
		}
	}
	// Hit the iteration cap without fixpoint — likely rule oscillation.
	// Return the current pipeline (still valid); the caller may warn.
	return pipe, totalChanges
}

// CatalogStatsReader adapts a *stats.Catalog into a rules.StatsReader using
// the O1 selectivity estimator (MCV/range/IN/AND/OR — the DESIGN §6.3 formula
// table) for selectivity-aware rules. Returns a noopReader (default 0) if the
// catalog is nil.
func CatalogStatsReader(c *stats.Catalog) StatsReader {
	if c == nil {
		return noopReader{}
	}
	return &catalogReader{est: cost.NewEstimator(c)}
}

type catalogReader struct{ est *cost.Estimator }

// Selectivity estimates a column's equality selectivity (the most common case
// for predicate-ordering rules). For richer per-predicate selectivity, rules
// can use cost.Estimator directly; this StatsReader keeps the rule interface
// simple (column-level).
func (r *catalogReader) Selectivity(table, column string) float64 {
	// A column-level selectivity call from a rule typically means "how selective
	// is an equality predicate on this column?" — model it as col = <any const>
	// using the estimator's non-MCV path (1/card), which is the right default
	// when the rule doesn't know the constant.
	return r.est.Selectivity(table, &ir.BinOp{
		Op: token.EQL,
		X:  &ir.Col{Name: column},
		Y:  &ir.Lit{T: ir.TypeString, HasValue: false}, // null literal → non-MCV path
	})
}
