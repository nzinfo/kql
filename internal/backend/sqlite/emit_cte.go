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
// it delegates to the stage-specific emitter. Otherwise it merges the
// mergeable stages (Filter/Project/Extend/Sort/Limit) into one SELECT.
func (e *emitter) emitSegment(seg segment, from, alias string) (string, error) {
	if len(seg.stages) == 1 && isBreakpoint(seg.stages[0]) {
		// Breakpoint: delegate to the existing stage emitter (which wraps in a
		// subquery). The `from` is the source/prev-CTE, alias is the CTE name.
		inner := fmt.Sprintf("SELECT * FROM %s", from)
		return e.emitStage(inner, seg.stages[0])
	}
	// Mergeable run: combine Filter/Extend/Project/Sort/Limit into one SELECT.
	return e.emitMergedSelect(seg.stages, from, alias)
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
