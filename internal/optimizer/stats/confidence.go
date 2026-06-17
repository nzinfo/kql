package stats

// Confidence scores how trustworthy a table's (or column's) statistics are.
//
// Per O0.S2 / O0-verification: the source provides a CEILING (pg_analyze ≤ 0.9,
// sampling ≤ 0.7, manual ≤ 0.6). Missing CORE fields pull the score DOWN below
// the ceiling. CorrVs is an ENHANCEMENT field — its absence never lowers
// confidence (pg doesn't expose it); only missing card/nulls/mcv/hist reduce it.
//
// The 4 core fields contribute equal weight: card, nulls, mcv, hist. A table's
// confidence is the average of its columns' column-confidences, capped by the
// source ceiling. Returns a value in [0, ceiling].
func (c *Catalog) Confidence(table string) float64 {
	t, ok := c.Tables[table]
	if !ok {
		return 0 // unknown table → no confidence
	}
	ceiling := c.Source.baseConfidence()
	if len(t.Columns) == 0 {
		return ceiling
	}
	sum := 0.0
	for _, col := range t.Columns {
		sum += colConfidence(col, ceiling)
	}
	avg := sum / float64(len(t.Columns))
	return avg
}

// ColumnConfidence scores a single column: the source ceiling scaled down by
// how many of its 4 core fields are present.
func (c *Catalog) ColumnConfidence(table, column string) float64 {
	t, ok := c.Tables[table]
	if !ok {
		return 0
	}
	col, ok := t.Columns[column]
	if !ok {
		return 0
	}
	return colConfidence(col, c.Source.baseConfidence())
}

// colConfidence is the per-column core: ceiling * (present_core_fields / 4).
// So a manual column with all 4 core fields = 0.6 (the ceiling); missing mcv
// and hist (2/4 present) = 0.6 * 0.5 = 0.3 < 0.5 ✓; a pg_analyze column with
// all 4 = 0.9 ✓.
func colConfidence(col *ColumnStats, ceiling float64) float64 {
	if col == nil {
		return 0
	}
	present := 0
	if col.Card > 0 {
		present++
	}
	if col.Nulls > 0 || col.Card > 0 { // nulls==0 may be legit; count if card known
		present++
	}
	if col.MCV != nil {
		present++
	}
	if col.Hist != nil {
		present++
	}
	return ceiling * float64(present) / 4.0
}
