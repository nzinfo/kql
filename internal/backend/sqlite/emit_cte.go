// CTE-based merged emit (replaces the nested-subquery-per-stage approach).
//
// Strategy: split the pipeline's stages at BREAKPOINTS (Aggregate/Join/Distinct/
// Union — stages that change the row-set shape). Each segment of consecutive
// mergeable stages (Filter/Project/Extend/Sort/Limit) becomes a SINGLE SELECT
// with the WHERE/computed-cols/ORDER-BY/LIMIT all combined. Segments chain via
// CTEs: WITH s0 AS (...), s1 AS (SELECT ... FROM s0 ...).
//
// This dramatically reduces nesting depth (10 stages → 2-3 CTEs instead of 10
// nested subqueries), lowering planning time and avoiding planner edge cases.
// pg 12+ treats CTEs as NOT MATERIALIZED by default (inlined), so performance
// matches hand-written SQL.
//
// The old per-stage-nesting emit is kept as a fallback for complex cases that
// the merger doesn't yet handle (joins with sub-pipelines, etc.); the merger
// falls back gracefully rather than producing wrong SQL.

package sqlite

import (
	"fmt"
	"strings"

	"nzinfo/kql/internal/backend"
	"nzinfo/kql/internal/ir"
)

// isBreakpoint reports whether a stage starts a new CTE segment. BREAKPOINTS
// (Aggregate/Join/Distinct/Union) always start a new segment. Extend/Project
// are also treated as segment boundaries for correctness: an Extend that adds
// column X followed by another Extend referencing X can't merge into one
// SELECT (SQL aliases aren't visible within the same SELECT clause). Making
// each Extend/Project its own segment is conservative but always correct;
// future optimisation could merge stages whose expressions don't reference
// newly-introduced columns.
func isBreakpoint(st ir.Stage) bool {
	switch st.(type) {
	case *ir.Aggregate, *ir.Join, *ir.Distinct, *ir.Union,
		*ir.Extend, *ir.Project:
		return true
	}
	return false
}

// segment is a group of stages emitted as one SELECT. If it starts with a
// breakpoint stage, that stage IS the segment (breakpoints can't merge with
// each other). Otherwise it's a run of mergeable stages.
type segment struct {
	stages []ir.Stage
}

// splitSegments divides the pipeline's stages into segments at breakpoints.
// A breakpoint stage starts its own segment; mergeable stages attach to the
// current segment (or start one if none is open).
func splitSegments(stages []ir.Stage) []segment {
	var segs []segment
	cur := segment{}
	for _, st := range stages {
		if isBreakpoint(st) {
			// Flush the current mergeable run, then the breakpoint is its own segment.
			if len(cur.stages) > 0 {
				segs = append(segs, cur)
				cur = segment{}
			}
			segs = append(segs, segment{stages: []ir.Stage{st}})
			continue
		}
		cur.stages = append(cur.stages, st)
	}
	if len(cur.stages) > 0 {
		segs = append(segs, cur)
	}
	return segs
}

// EmitCTE translates an IR Pipeline into a CTE-based backend.Query. This is the
// production emit path (replaces the nested Emit). Falls back to the old Emit
// for complex pipelines the CTE merger doesn't yet handle.
func EmitCTE(pipe *ir.Pipeline) (*backend.Query, error) {
	if pipe == nil {
		return nil, fmt.Errorf("nil pipeline")
	}
	e := newEmitter()
	sql, err := e.emitPipelineCTE(pipe)
	if err != nil {
		// Fallback: the old nested emit (always works, just more nesting).
		return Emit(pipe)
	}
	return &backend.Query{SQL: sql, Args: e.orderedArgs()}, nil
}

// emitPipelineCTE builds CTE-chained SQL from the pipeline. Returns an error
// if a stage type isn't handled (caller falls back to nested emit).
func (e *emitter) emitPipelineCTE(pipe *ir.Pipeline) (string, error) {
	segs := splitSegments(pipe.Stages)
	if len(segs) == 0 {
		// No stages: just SELECT * FROM source.
		from, err := emitSource(pipe.Source)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("SELECT * FROM %s", from), nil
	}

	// Each segment becomes a CTE: WITH s0 AS (...), s1 AS (...), ...
	// The first segment reads from the source table; subsequent segments read
	// FROM the previous CTE name.
	cteParts := make([]string, 0, len(segs))
	prevCTE := ""
	for i, seg := range segs {
		cteName := fmt.Sprintf("_s%d", i)
		var fromSQL string
		var fromAlias string
		if i == 0 {
			src, err := emitSource(pipe.Source)
			if err != nil {
				return "", err
			}
			fromSQL = src
			fromAlias = cteName
		} else {
			fromSQL = prevCTE
			fromAlias = cteName
		}
		segSQL, err := e.emitSegment(seg, fromSQL, fromAlias)
		if err != nil {
			return "", err
		}
		cteParts = append(cteParts, fmt.Sprintf("%s AS (%s)", cteName, segSQL))
		prevCTE = cteName
	}

	// Final query: SELECT * FROM the last CTE.
	finalCTE := fmt.Sprintf("_s%d", len(segs)-1)
	cteClause := "WITH " + strings.Join(cteParts, ", ")
	return cteClause + " SELECT * FROM " + finalCTE, nil
}

// emitSegment emits a single segment (group of stages) as one SELECT.
// If the segment is a single breakpoint stage (aggregate/join/distinct/union),
// it delegates to the stage-specific emitter reading DIRECTLY from the CTE name
// (no redundant SELECT * FROM wrapper). Otherwise it merges the mergeable
// stages (Filter/Sort/Limit) into one SELECT.
func (e *emitter) emitSegment(seg segment, from, alias string) (string, error) {
	if len(seg.stages) == 1 && isBreakpoint(seg.stages[0]) {
		// Breakpoint: emit directly from the CTE name, no inner subquery wrap.
		return e.emitBreakpointDirect(seg.stages[0], from, alias)
	}
	// Mergeable run: combine Filter/Sort/Limit into one SELECT.
	return e.emitMergedSelect(seg.stages, from, alias)
}

// emitBreakpointDirect emits a breakpoint stage reading directly FROM the CTE
// name (or source table), avoiding the redundant (SELECT * FROM _sN) wrapper.
// alias is used as the FROM alias for column references.
func (e *emitter) emitBreakpointDirect(st ir.Stage, from, alias string) (string, error) {
	switch s := st.(type) {
	case *ir.Aggregate:
		return e.emitAggregateDirect(s, from, alias)
	case *ir.Distinct:
		cols := make([]string, 0, len(s.Cols))
		for _, c := range s.Cols {
			ex, err := e.emitExpr(c, alias)
			if err != nil {
				return "", err
			}
			cols = append(cols, ex)
		}
		if len(cols) == 0 {
			cols = []string{alias + ".*"}
		}
		return fmt.Sprintf("SELECT DISTINCT %s FROM %s AS %s", strings.Join(cols, ", "), from, alias), nil
	case *ir.Union:
		parts := []string{fmt.Sprintf("SELECT %s.* FROM %s AS %s", alias, from, alias)}
		for _, in := range s.Inputs {
			subSQL, err := e.emitPipelineCTE(in)
			if err != nil {
				return "", err
			}
			parts = append(parts, fmt.Sprintf("SELECT * FROM (%s)", subSQL))
		}
		return strings.Join(parts, " UNION "), nil
	case *ir.Join:
		return e.emitJoinDirect(s, from, alias)
	}
	// Fallback: wrap (shouldn't reach here for known breakpoints).
	return e.emitStage(fmt.Sprintf("SELECT * FROM %s", from), st)
}

// emitAggregateDirect emits an Aggregate reading directly from the CTE name.
func (e *emitter) emitAggregateDirect(s *ir.Aggregate, from, alias string) (string, error) {
	selectParts := make([]string, 0, len(s.Keys)+len(s.Aggregates))
	groupParts := make([]string, 0, len(s.Keys))
	for i, k := range s.Keys {
		ex, err := e.emitExpr(k.Expr, alias)
		if err != nil {
			return "", err
		}
		aliasName := k.Name
		if aliasName == "" {
			if col, ok := k.Expr.(*ir.Col); ok && col.Name != "" {
				aliasName = col.Name
			} else {
				aliasName = fmt.Sprintf("key%d", i)
			}
		}
		selectParts = append(selectParts, fmt.Sprintf("%s AS %s", ex, quoteIdent(aliasName)))
		groupParts = append(groupParts, ex)
	}
	for i, a := range s.Aggregates {
		ex, err := e.emitExpr(a.Expr, alias)
		if err != nil {
			return "", err
		}
		aliasName := a.Name
		if aliasName == "" {
			aliasName = fmt.Sprintf("agg%d", i)
		}
		selectParts = append(selectParts, fmt.Sprintf("%s AS %s", ex, quoteIdent(aliasName)))
	}
	if len(selectParts) == 0 {
		return "", fmt.Errorf("summarize with no aggregates")
	}
	sql := fmt.Sprintf("SELECT %s FROM %s AS %s", strings.Join(selectParts, ", "), from, alias)
	if len(groupParts) > 0 {
		sql += " GROUP BY " + strings.Join(groupParts, ", ")
	}
	return sql, nil
}

// emitJoinDirect emits a Join reading directly from the CTE name on the left.
func (e *emitter) emitJoinDirect(s *ir.Join, from, alias string) (string, error) {
	if s.Right == nil {
		return "", fmt.Errorf("join with no right side")
	}
	rightSQL, err := e.emitPipelineCTE(s.Right)
	if err != nil {
		// Fallback to old pipeline emit for complex sub-pipelines.
		rightSQL, err = e.emitPipeline(s.Right)
		if err != nil {
			return "", err
		}
	}
	joinType := "INNER"
	switch s.Kind {
	case ir.JoinLeftOuter:
		joinType = "LEFT"
	case ir.JoinRightOuter:
		joinType = "RIGHT"
	case ir.JoinFullOuter:
		joinType = "FULL"
	}
	leftAlias := alias
	rightAlias := alias + "_j"
	onParts := make([]string, 0, len(s.On))
	for _, c := range s.On {
		ex, err := e.emitJoinOnExpr(c, leftAlias, rightAlias)
		if err != nil {
			return "", err
		}
		onParts = append(onParts, ex)
	}
	on := "1=1"
	if len(onParts) > 0 {
		on = strings.Join(onParts, " AND ")
	}
	return fmt.Sprintf("SELECT %s.*, %s.* FROM %s AS %s %s JOIN (%s) AS %s ON %s",
		leftAlias, rightAlias, from, leftAlias, joinType, rightSQL, rightAlias, on), nil
}

// emitMergedSelect combines a run of mergeable stages into a single SELECT.
// It threads the alias through all stages: the SELECT clause, WHERE, ORDER BY,
// and LIMIT all reference the same FROM alias.
func (e *emitter) emitMergedSelect(stages []ir.Stage, from, alias string) (string, error) {
	var whereParts []string
	var selectParts []string // computed/projected columns (for extend/project)
	var orderParts []string
	var limitExpr string
	selectMode := "passthrough" // "passthrough" (SELECT alias.*) or "project" (explicit cols)
	projectCols := []string{}

	for _, st := range stages {
		switch s := st.(type) {
		case *ir.Filter:
			pred, err := e.emitExpr(s.Predicate, alias)
			if err != nil {
				return "", err
			}
			whereParts = append(whereParts, pred)
		case *ir.Extend:
			for _, c := range s.Cols {
				ex, err := e.emitExpr(c.Expr, alias)
				if err != nil {
					return "", err
				}
				selectParts = append(selectParts, fmt.Sprintf("%s AS %s", ex, quoteIdent(c.Name)))
			}
		case *ir.Project:
			selectMode = "project"
			projectCols = projectCols[:0]
			for _, c := range s.Cols {
				ex, err := e.emitExpr(c.Expr, alias)
				if err != nil {
					return "", err
				}
				if c.Name != "" {
					projectCols = append(projectCols, fmt.Sprintf("%s AS %s", ex, quoteIdent(c.Name)))
				} else {
					projectCols = append(projectCols, ex)
				}
			}
		case *ir.Sort:
			for _, k := range s.Keys {
				ex, err := e.emitExpr(k.Expr, alias)
				if err != nil {
					return "", err
				}
				dir := "ASC"
				if k.Desc {
					dir = "DESC"
				}
				if k.NullsFirst {
					orderParts = append(orderParts, fmt.Sprintf("%s %s NULLS FIRST", ex, dir))
				} else {
					orderParts = append(orderParts, fmt.Sprintf("%s %s", ex, dir))
				}
			}
		case *ir.Limit:
			ex, err := e.emitExpr(s.Count, alias)
			if err != nil {
				return "", err
			}
			limitExpr = ex
		}
	}

	// Build the SELECT clause.
	var selectClause string
	if selectMode == "project" && len(projectCols) > 0 {
		selectClause = strings.Join(projectCols, ", ")
		// If there are also extend cols (selectParts), append them.
		if len(selectParts) > 0 {
			selectClause += ", " + strings.Join(selectParts, ", ")
		}
	} else {
		// Passthrough: alias.* plus any extend cols.
		selectClause = alias + ".*"
		if len(selectParts) > 0 {
			selectClause += ", " + strings.Join(selectParts, ", ")
		}
	}

	sql := fmt.Sprintf("SELECT %s FROM %s AS %s", selectClause, from, alias)
	if len(whereParts) > 0 {
		sql += " WHERE " + strings.Join(whereParts, " AND ")
	}
	if len(orderParts) > 0 {
		sql += " ORDER BY " + strings.Join(orderParts, ", ")
	}
	if limitExpr != "" {
		sql += " LIMIT " + limitExpr
	}
	return sql, nil
}
