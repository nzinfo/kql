// CTE-based merged emit for PostgreSQL (mirrors sqlite/emit_cte.go but uses
// pg's emitter for expression SQL — $N placeholders, ILIKE, etc.). This is the
// production emit path for pg, replacing the nested-subquery-per-stage approach.
package pg

import (
	"fmt"
	"strings"

	"nzinfo/kql/internal/backend"
	"nzinfo/kql/internal/ir"
)

// EmitCTE translates an IR Pipeline into a CTE-based pg backend.Query.
func EmitCTE(pipe *ir.Pipeline) (*backend.Query, error) {
	if pipe == nil {
		return nil, fmt.Errorf("nil pipeline")
	}
	e := newEmitter()
	sql, err := e.emitPipelineCTE(pipe)
	if err != nil {
		return Emit(pipe) // fallback to nested emit
	}
	return &backend.Query{SQL: sql, Args: e.orderedArgs()}, nil
}

func isBreakpoint(st ir.Stage) bool {
	switch st.(type) {
	case *ir.Aggregate, *ir.Join, *ir.Distinct, *ir.Union,
		*ir.Extend, *ir.Project:
		return true
	}
	return false
}

type pgSegment struct{ stages []ir.Stage }

func splitSegmentsPG(stages []ir.Stage) []pgSegment {
	var segs []pgSegment
	cur := pgSegment{}
	for _, st := range stages {
		if isBreakpoint(st) {
			if len(cur.stages) > 0 {
				segs = append(segs, cur)
				cur = pgSegment{}
			}
			segs = append(segs, pgSegment{stages: []ir.Stage{st}})
			continue
		}
		cur.stages = append(cur.stages, st)
	}
	if len(cur.stages) > 0 {
		segs = append(segs, cur)
	}
	return segs
}

func (e *emitter) emitPipelineCTE(pipe *ir.Pipeline) (string, error) {
	segs := splitSegmentsPG(pipe.Stages)
	if len(segs) == 0 {
		from, err := emitSource(pipe.Source)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("SELECT * FROM %s", from), nil
	}
	cteParts := make([]string, 0, len(segs))
	prevCTE := ""
	for i, seg := range segs {
		cteName := fmt.Sprintf("_s%d", i)
		var fromSQL string
		if i == 0 {
			src, err := emitSource(pipe.Source)
			if err != nil {
				return "", err
			}
			fromSQL = src
		} else {
			fromSQL = prevCTE
		}
		segSQL, err := e.emitSegmentPG(seg, fromSQL, cteName)
		if err != nil {
			return "", err
		}
		cteParts = append(cteParts, fmt.Sprintf("%s AS (%s)", cteName, segSQL))
		prevCTE = cteName
	}
	finalCTE := fmt.Sprintf("_s%d", len(segs)-1)
	return "WITH " + strings.Join(cteParts, ", ") + " SELECT * FROM " + finalCTE, nil
}

func (e *emitter) emitSegmentPG(seg pgSegment, from, alias string) (string, error) {
	if len(seg.stages) == 1 && isBreakpoint(seg.stages[0]) {
		return e.emitBreakpointDirectPG(seg.stages[0], from, alias)
	}
	return e.emitMergedSelectPG(seg.stages, from, alias)
}

// emitBreakpointDirectPG emits a breakpoint stage reading directly from the
// CTE name, avoiding the redundant (SELECT * FROM _sN) wrapper.
func (e *emitter) emitBreakpointDirectPG(st ir.Stage, from, alias string) (string, error) {
	switch s := st.(type) {
	case *ir.Aggregate:
		selectParts := make([]string, 0, len(s.Keys)+len(s.Aggregates))
		groupParts := make([]string, 0, len(s.Keys))
		for i, k := range s.Keys {
			ex, err := e.emitExpr(k.Expr, alias)
			if err != nil {
				return "", err
			}
			an := k.Name
			if an == "" {
				if col, ok := k.Expr.(*ir.Col); ok && col.Name != "" {
					an = col.Name
				} else {
					an = fmt.Sprintf("key%d", i)
				}
			}
			selectParts = append(selectParts, fmt.Sprintf("%s AS %s", ex, quoteIdent(an)))
			groupParts = append(groupParts, ex)
		}
		for i, a := range s.Aggregates {
			ex, err := e.emitExpr(a.Expr, alias)
			if err != nil {
				return "", err
			}
			an := a.Name
			if an == "" {
				an = fmt.Sprintf("agg%d", i)
			}
			selectParts = append(selectParts, fmt.Sprintf("%s AS %s", ex, quoteIdent(an)))
		}
		sql := fmt.Sprintf("SELECT %s FROM %s AS %s", strings.Join(selectParts, ", "), from, alias)
		if len(groupParts) > 0 {
			sql += " GROUP BY " + strings.Join(groupParts, ", ")
		}
		return sql, nil
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
				subSQL, err = e.emitPipeline(in)
				if err != nil {
					return "", err
				}
			}
			parts = append(parts, fmt.Sprintf("SELECT * FROM (%s)", subSQL))
		}
		return strings.Join(parts, " UNION "), nil
	case *ir.Join:
		if s.Right == nil {
			return "", fmt.Errorf("join with no right side")
		}
		rightSQL, err := e.emitPipelineCTE(s.Right)
		if err != nil {
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
		rightAlias := alias + "_j"
		onParts := make([]string, 0, len(s.On))
		for _, c := range s.On {
			ex, err := e.emitJoinOnExpr(c, alias, rightAlias)
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
			alias, rightAlias, from, alias, joinType, rightSQL, rightAlias, on), nil
	}
	return e.emitStage(fmt.Sprintf("SELECT * FROM %s", from), st)
}

func (e *emitter) emitMergedSelectPG(stages []ir.Stage, from, alias string) (string, error) {
	var whereParts, selectParts, orderParts, projectCols []string
	var limitExpr string
	selectMode := "passthrough"

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

	var selectClause string
	if selectMode == "project" && len(projectCols) > 0 {
		selectClause = strings.Join(projectCols, ", ")
		if len(selectParts) > 0 {
			selectClause += ", " + strings.Join(selectParts, ", ")
		}
	} else {
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
