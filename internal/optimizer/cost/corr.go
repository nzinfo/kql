package cost

// Correlation correction (O1.S3, docs/phases/optimizer/O1-selectivity-cost.md).
//
// The independence assumption (AND → s1*s2) overestimates selectivity when two
// columns are correlated — classic case: created_at vs id (monotonic, rho≈1).
// When the catalog records corr_vs {OtherColumn, Rho} for a column, the
// estimator adjusts the combined selectivity:
//
//	s_corrected = s1*s2 + rho * sqrt(s1*(1-s1) * s2*(1-s2))
//
// (a standard second-order adjustment for positively-correlated predicates).
// rho > 0 raises the estimate (correlated predicates overlap more), rho < 0
// lowers it (anti-correlated). Without rho, the plain independence product is
// used (the catalog's normal state, since pg doesn't expose rho).

// applyCorrCorrection adjusts a product of per-key selectivities when the
// catalog records correlation between join key columns. For each pair of keys
// (i,j), if column i has corr_vs pointing at column j (same table), the product
// is nudged by the rho-weighted term. This is a conservative, pairwise
// correction — full multivariate would need a covariance matrix the catalog
// doesn't carry.
//
// selIn is the independence-product estimate; returns the corrected value.
func (e *Estimator) applyCorrCorrection(keys []joinKey, leftTable string, selIn float64) float64 {
	if e.catalog == nil || len(keys) < 2 {
		return selIn // nothing to correlate
	}
	// Re-compute per-key selectivities so we can apply pairwise correction.
	// (We could thread them in, but the join path is short; keep it readable.)
	sels := make([]float64, len(keys))
	for i, k := range keys {
		sels[i] = e.eqJoinSelectivity(leftTable, "", k.lCol, k.rCol)
	}
	// Look up pairwise rho on the LEFT table's columns (corr_vs is recorded
	// per-column against another column in the same table, per the O0 catalog
	// shape). For each pair (i,j) where col_i has corr_vs.col_j, apply the
	// correction once (avoid double-counting by only handling i<j).
	adjusted := make([]float64, len(sels))
	copy(adjusted, sels)
	for i := 0; i < len(keys); i++ {
		ci := e.columnStats(leftTable, keys[i].lCol)
		if ci == nil || ci.CorrVs == nil {
			continue
		}
		for j := i + 1; j < len(keys); j++ {
			if ci.CorrVs.OtherColumn != keys[j].lCol {
				continue
			}
			rho := clampRho(ci.CorrVs.Rho)
			if rho == 0 {
				continue
			}
			// Replace the independence product s_i*s_j with the rho-adjusted form.
			si, sj := sels[i], sels[j]
			corrected := si*sj + rho*sqrt(si*(1-si)*sj*(1-sj))
			// Re-scale: the original product contributed sels[i]*sels[j] to the
			// running product; swap in the corrected pair-product.
			if sels[i] > 0 && sels[j] > 0 {
				selIn *= corrected / (sels[i] * sels[j])
			}
		}
	}
	return clamp01(selIn)
}

// clampRho constrains a Pearson correlation to [-1, 1] (guards catalog typos).
func clampRho(r float64) float64 {
	if r > 1 {
		return 1
	}
	if r < -1 {
		return -1
	}
	return r
}

// sqrt is a tiny helper to avoid importing math just for one call site.
func sqrt(x float64) float64 {
	if x <= 0 {
		return 0
	}
	// Newton's method — a few iterations suffice for the selectivity estimate's
	// precision needs (this isn't a hot path; correctness, not speed).
	z := x
	for i := 0; i < 16; i++ {
		z = (z + x/z) / 2
	}
	return z
}
