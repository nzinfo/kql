// Package cost implements selectivity estimation and the cost model
// (docs/phases/optimizer/O1-selectivity-cost.md, DESIGN.md §6.3).
//
// Selectivity turns a predicate into the fraction of rows it retains ∈ [0,1];
// it's the core input to cost-based decisions (which predicate to push first,
// join ordering). The model is intentionally simple and explainable (per
// DESIGN §6.3): MCV-lookup for equality, histogram-position for range, sum for
// IN, independence for AND/OR — with corr correction available (O1.S3, later).
//
// Sources of truth: the O0 stats catalog (card/mcv/hist/nulls). When stats are
// absent or confidence is low, rules get a conservative default (0.1) so they
// still make safe (if not optimal) decisions (O1.S5 degradation).
package cost

import (
	"nzinfo/kql/internal/frontend/token"
	"nzinfo/kql/internal/ir"
	"nzinfo/kql/internal/optimizer/stats"
)

// DefaultSelectivity is the conservative fallback when no statistics are
// available for a predicate (O1.S5 / DESIGN §6.3 "无任何统计 → 0.1").
const DefaultSelectivity = 0.1

// Estimator computes predicate selectivities against a stats catalog.
type Estimator struct {
	catalog *stats.Catalog
}

// NewEstimator builds an estimator over a catalog. A nil catalog yields
// DefaultSelectivity for everything (graceful degradation).
func NewEstimator(c *stats.Catalog) *Estimator { return &Estimator{catalog: c} }

// Selectivity estimates the fraction of rows a predicate retains. The optional
// table hints which table's column stats to consult (when known); "" means
// "no table context" → uses the default.
func (e *Estimator) Selectivity(table string, pred ir.Expr) float64 {
	if pred == nil {
		return 1.0 // no predicate → all rows
	}
	switch n := pred.(type) {
	case *ir.BinOp:
		return e.binOpSelectivity(table, n)
	case *ir.UnaryOp:
		// unary on a bool-ish expr; approximate as the inner selectivity.
		return e.Selectivity(table, n.X)
	case *ir.FuncCall:
		// A function-as-predicate (e.g. isnotempty(x)) — approximate with the
		// default; the catalog doesn't model function predicates.
		return DefaultSelectivity
	case *ir.Col:
		// bare column as a truthy filter → ~ non-null fraction if known, else default
		if s := e.columnStats(table, n.Name); s != nil && s.Nulls >= 0 {
			// approximate: non-null fraction. Without row_count on the column we
			// can't compute exactly; fall back to default.
		}
		return DefaultSelectivity
	}
	return DefaultSelectivity
}

// binOpSelectivity handles the predicate operators from DESIGN §6.3's table.
func (e *Estimator) binOpSelectivity(table string, b *ir.BinOp) float64 {
	switch b.Op {
	case token.AND:
		return e.Selectivity(table, b.X) * e.Selectivity(table, b.Y)
	case token.OR:
		s1 := e.Selectivity(table, b.X)
		s2 := e.Selectivity(table, b.Y)
		// P(a OR b) = P(a) + P(b) - P(a)P(b) (independence).
		return clamp01(s1 + s2 - s1*s2)
	case token.EQL, token.NEQ:
		return e.equalitySelectivity(table, b)
	case token.IN, token.INCI, token.HASANY:
		return e.inListSelectivity(table, b)
	case token.LSS, token.GTR, token.LEQ, token.GEQ:
		return e.rangeSelectivity(table, b)
	case token.BETWEEN, token.NOTBETWEEN:
		return e.betweenSelectivity(table, b)
	}
	// string operators (has/contains/...) — approximate with the default; the
	// catalog doesn't model substring distributions.
	return DefaultSelectivity
}

// equalitySelectivity: col = const. If const ∈ MCV → its frequency; else 1/card;
// else default. (DESIGN §6.3 table rows 1–2.)
func (e *Estimator) equalitySelectivity(table string, b *ir.BinOp) float64 {
	colName, lit := equalityOperands(b)
	if colName == "" {
		return DefaultSelectivity
	}
	s := e.columnStats(table, colName)
	if s == nil {
		return DefaultSelectivity
	}
	val := litStringValue(lit)
	// MCV hit?
	if s.MCV != nil && val != "" {
		for i, v := range s.MCV.Values {
			if v == val && i < len(s.MCV.Frequencies) {
				if b.Op == token.NEQ {
					return clamp01(1 - s.MCV.Frequencies[i])
				}
				return clamp01(s.MCV.Frequencies[i])
			}
		}
	}
	// Not in MCV (or no MCV): 1/card.
	if s.Card > 0 {
		sel := 1.0 / float64(s.Card)
		if b.Op == token.NEQ {
			sel = 1 - sel
		}
		return clamp01(sel)
	}
	return DefaultSelectivity
}

// inListSelectivity: col in (v1, v2, ...) → sum of per-value selectivities
// (capped at 1.0). (DESIGN §6.3 table row 4.)
func (e *Estimator) inListSelectivity(table string, b *ir.BinOp) float64 {
	colName := columnName(b.X)
	if colName == "" {
		return DefaultSelectivity
	}
	list, ok := b.Y.(*ir.List)
	if !ok {
		return DefaultSelectivity
	}
	s := e.columnStats(table, colName)
	if s == nil {
		return DefaultSelectivity
	}
	total := 0.0
	for _, el := range list.Elems {
		val := litStringValue(el)
		if s.MCV != nil && val != "" {
			hit := false
			for i, v := range s.MCV.Values {
				if v == val && i < len(s.MCV.Frequencies) {
					total += s.MCV.Frequencies[i]
					hit = true
					break
				}
			}
			if hit {
				continue
			}
		}
		if s.Card > 0 {
			total += 1.0 / float64(s.Card)
		} else {
			total += DefaultSelectivity
		}
	}
	return clamp01(total)
}

// rangeSelectivity: col < const → histogram-position fraction. Without a
// histogram, fall back to a fixed 0.33 (range predicates are common; pg uses
// ~0.33 by default for < on unbounded stats). (DESIGN §6.3 row 3, extended.)
func (e *Estimator) rangeSelectivity(table string, b *ir.BinOp) float64 {
	colName := columnName(b.X)
	if colName == "" {
		return DefaultSelectivity
	}
	s := e.columnStats(table, colName)
	if s == nil {
		return DefaultSelectivity
	}
	if s.Hist != nil && len(s.Hist.Bounds) > 0 {
		// Without knowing the const's type/position precisely (bounds are
		// strings), use a position-agnostic 1/(2*bucket_count) estimate for a
		// single-sided range. This is a coarse placeholder until typed bounds
		// land; it's better than 0.1 when we know the column has a histogram.
		return clamp01(1.0 / float64(2*len(s.Hist.Bounds)))
	}
	// No histogram: 0.33 default (matches pg's default_range_selectivity).
	return 0.33
}

// betweenSelectivity: col between (lo .. hi) → range width estimate.
func (e *Estimator) betweenSelectivity(table string, b *ir.BinOp) float64 {
	colName := columnName(b.X)
	if colName == "" {
		return DefaultSelectivity
	}
	if e.columnStats(table, colName) == nil {
		return DefaultSelectivity
	}
	// between is a double-sided range; without typed bounds, ~0.25 (pg default
	// for a scalar between is ~0.25 = two half-ranges).
	return 0.25
}

// columnStats looks up a column's stats in the catalog (nil-safe).
func (e *Estimator) columnStats(table, col string) *stats.ColumnStats {
	if e.catalog == nil || e.catalog.Tables == nil {
		return nil
	}
	t, ok := e.catalog.Tables[table]
	if !ok {
		return nil
	}
	return t.Columns[col]
}

// equalityOperands extracts (columnName, literal) from `col = lit` or
// `lit = col`, returning ("", nil) if the shape doesn't match.
func equalityOperands(b *ir.BinOp) (string, ir.Expr) {
	if cn := columnName(b.X); cn != "" {
		if _, ok := b.Y.(*ir.Lit); ok {
			return cn, b.Y
		}
	}
	if cn := columnName(b.Y); cn != "" {
		if _, ok := b.X.(*ir.Lit); ok {
			return cn, b.X
		}
	}
	return "", nil
}

// columnName returns the name of a *ir.Col expression, or "".
func columnName(e ir.Expr) string {
	if c, ok := e.(*ir.Col); ok {
		return c.Name
	}
	return ""
}

// litStringValue returns the string form of a literal's value (for MCV match),
// or "" if not string-representable.
func litStringValue(e ir.Expr) string {
	if l, ok := e.(*ir.Lit); ok && l.HasValue {
		switch v := l.Value.(type) {
		case string:
			return v
		case int64:
			return formatInt(v)
		case float64:
			return formatFloat(v)
		case bool:
			if v {
				return "true"
			}
			return "false"
		}
	}
	return ""
}

// clamp01 constrains a selectivity to [0, 1].
func clamp01(s float64) float64 {
	if s < 0 {
		return 0
	}
	if s > 1 {
		return 1
	}
	return s
}

// formatInt / formatFloat avoid importing strconv just for two conversions.
func formatInt(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func formatFloat(v float64) string {
	// crude — good enough for MCV string matching
	if v == float64(int64(v)) {
		return formatInt(int64(v))
	}
	// fall back to a fixed-point approximation
	whole := int64(v)
	frac := v - float64(whole)
	return formatInt(whole) + "." + formatInt(int64(frac*1000000))
}
