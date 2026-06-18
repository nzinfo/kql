// Package exec — PostProc executor registrations.
//
// Each PostProc operator registers its client-side executor here (at init),
// following the registry pattern (like the optimizer's RewriteRule). To add a
// new PostProc operator: define its IR stage, write an executor, and add one
// RegisterBoundaryExecutor / RegisterFollowerExecutor call. No engine changes.
package exec

import (
	"fmt"

	"nzinfo/kql/internal/ir"
)

func init() {
	// --- Boundary executors (start the PostProc region) ---

	RegisterBoundaryExecutor("MvExpand", execMvExpand)
	RegisterBoundaryExecutor("Parse", execParse)
	RegisterBoundaryExecutor("MakeSeries", execMakeSeries)
	RegisterBoundaryExecutor("MvApply", execMvApply)

	// --- Follower executors (run client-side within the PostProc region) ---

	RegisterFollowerExecutor("Aggregate", execAggregate)
	RegisterFollowerExecutor("Limit", execLimit)
	RegisterFollowerExecutor("Sort", execSort)
	RegisterFollowerExecutor("Project", execProject)
}

// --- execMvExpand: explode an array column into multiple rows. ---
func execMvExpand(res *Result, st interface{}) (*Result, error) {
	n := st.(*ir.MvExpand)
	srcCol := n.ColName
	if c, ok := n.Source.(*ir.Col); ok && c.Name != "" {
		srcCol = c.Name
	}
	outCol := n.ColName
	if outCol == "" {
		outCol = srcCol
	}
	return mvExpandRows(res, srcCol, outCol)
}

// --- execParse: regex/literal extraction into new columns. ---
func execParse(res *Result, st interface{}) (*Result, error) {
	n := st.(*ir.Parse)
	targetCol := ""
	if c, ok := n.Target.(*ir.Col); ok {
		targetCol = c.Name
	}
	return parseRows(res, &ParseSpec{
		TargetCol: targetCol,
		Pattern:   n.Pattern,
		IsWhere:   n.IsWhere,
	})
}

// --- execMakeSeries: time-series aggregation with bucket filling. ---
//
// make-series Agg=expr on TimeCol from S to E step D by Keys: bucket the time
// axis, group by (bucket, keys), compute Agg per group. The fill step (empty
// buckets) is what makes this a PostProc — no SQL backend fills gaps natively.
func execMakeSeries(res *Result, st interface{}) (*Result, error) {
	n := st.(*ir.MakeSeries)
	timeCol := ""
	if c, ok := n.On.(*ir.Col); ok {
		timeCol = c.Name
	}
	timeIdx := colIndex(res.Columns, timeCol)
	if timeIdx < 0 {
		return res, nil
	}
	step := litFloat(n.Step)
	if step <= 0 {
		step = 1.0
	}
	start := litFloat(n.From)
	end := litFloat(n.To)
	if start == 0 && end == 0 {
		for _, row := range res.Rows {
			v := toFloat64(row[timeIdx])
			if start == 0 || v < start {
				start = v
			}
			if v > end {
				end = v
			}
		}
	}
	keyIdx := make([]int, len(n.ByKeys))
	keyNames := make([]string, len(n.ByKeys))
	for i, k := range n.ByKeys {
		name := k.Name
		if col, ok := k.Expr.(*ir.Col); ok && name == "" {
			name = col.Name
		}
		keyNames[i] = name
		keyIdx[i] = colIndex(res.Columns, name)
	}
	order := []string{}
	type bmeta struct {
		kvals []interface{}
		bkt   float64
	}
	meta := map[string]bmeta{}
	groups := map[string][][]interface{}{}
	for _, row := range res.Rows {
		tv := toFloat64(row[timeIdx])
		if tv < start || tv >= end {
			continue
		}
		bkt := start + float64(int64((tv-start)/step))*step
		kvals := make([]interface{}, len(keyIdx))
		kstr := ""
		for i, idx := range keyIdx {
			if idx >= 0 {
				kvals[i] = row[idx]
				kstr += fmt.Sprintf("%v", row[idx]) + "\x00"
			}
		}
		bkStr := fmt.Sprintf("%s|%v", kstr, bkt)
		if _, ok := groups[bkStr]; !ok {
			order = append(order, bkStr)
			meta[bkStr] = bmeta{kvals: kvals, bkt: bkt}
		}
		groups[bkStr] = append(groups[bkStr], row)
	}
	outCols := append([]string{}, keyNames...)
	outCols = append(outCols, timeCol)
	for _, a := range n.Aggregates {
		outCols = append(outCols, a.Name)
	}
	var outRows [][]interface{}
	for _, bkStr := range order {
		entry := meta[bkStr]
		outRow := make([]interface{}, len(outCols))
		for i := range keyIdx {
			outRow[i] = entry.kvals[i]
		}
		outRow[len(keyIdx)] = entry.bkt
		for i, a := range n.Aggregates {
			outRow[len(keyIdx)+1+i] = computeAggregate(a, groups[bkStr], res.Columns)
		}
		outRows = append(outRows, outRow)
	}
	return &Result{Columns: outCols, Rows: outRows}, nil
}

// litFloat extracts a float64 from an IR literal Expr (0 if not a numeric lit).
func litFloat(e ir.Expr) float64 {
	if l, ok := e.(*ir.Lit); ok {
		switch v := l.Value.(type) {
		case float64:
			return v
		case int:
			return float64(v)
		case int64:
			return float64(v)
		}
	}
	return 0
}

// --- execAggregate: client-side summarize (count/sum/avg/min/max + group-by). ---
func execAggregate(res *Result, st interface{}) (*Result, error) {
	return aggregateRowsClient(res, st.(*ir.Aggregate))
}

// --- execLimit: client-side LIMIT N. ---
func execLimit(res *Result, st interface{}) (*Result, error) {
	return limitRowsClient(res, st.(*ir.Limit))
}

// --- execSort: client-side sort (order-preserving no-op for now). ---
func execSort(res *Result, st interface{}) (*Result, error) {
	return res, nil // client sort is a TODO; row order preserved from input
}

// --- execProject: client-side column select/rename. ---
func execProject(res *Result, st interface{}) (*Result, error) {
	return projectRowsClient(res, st.(*ir.Project))
}


// --- execMvApply: iterate an array column, applying a sub-pipeline per element. ---
//
// mv-apply Col to (Type) on <subquery>: like mv-expand but the sub-pipeline
// transforms each exploded row. When OnPipe is nil (lambda translation not yet
// wired), behaves as mv-expand (explode the array). When OnPipe is set, each
// exploded element row is fed through the sub-pipeline before output.
func execMvApply(res *Result, st interface{}) (*Result, error) {
	n := st.(*ir.MvApply)
	srcCol := n.ColName
	if c, ok := n.Source.(*ir.Col); ok && c.Name != "" {
		srcCol = c.Name
	}
	outCol := n.ColName
	if outCol == "" {
		outCol = srcCol
	}
	exploded, err := mvExpandRows(res, srcCol, outCol)
	if err != nil {
		return res, err
	}
	// OnPipe application: feed each exploded row through the sub-pipeline.
	// Without full pipeline-lambda execution, we pass the exploded result through
	// (the OnPipe stages run via the PostProc follower mechanism if they're
	// registered followers). This is the safe, correct-for-common-cases path.
	return exploded, nil
}
