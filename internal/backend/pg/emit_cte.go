// CTE-based merged emit for PostgreSQL (mirrors sqlite/emit_cte.go but uses
// pg's emitter for expression SQL — $N placeholders, ILIKE, etc.). This is the
// production emit path for pg, replacing the nested-subquery-per-stage approach.
package pg

import (
	"fmt"
	"strings"

	"nzinfo/kql/internal/backend"
	"nzinfo/kql/internal/ir"
	"nzinfo/kql/internal/optimizer/decision"
	"nzinfo/kql/internal/optimizer/stats"
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

// EmitCTEWithCatalog is the cost-based variant: the emitter carries a stats
// catalog so cteMaterialization can make cost-based MATERIALIZED decisions
// (O6). When catalog is nil this is equivalent to EmitCTE.
func EmitCTEWithCatalog(pipe *ir.Pipeline, catalog *stats.Catalog) (*backend.Query, error) {
	if pipe == nil {
		return nil, fmt.Errorf("nil pipeline")
	}
	e := newEmitter()
	e.catalog = catalog
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
		// B3.S4: MATERIALIZED hint. Large breakpoint stages (Aggregate/Join)
		// benefit from materialization (force pg to compute + cache the CTE);
		// small mergeable stages (Filter/Sort/Limit) benefit from inlining
		// (NOT MATERIALIZED → pg can flatten the CTE into the outer query).
		// Only emitted for pg 14+ (earlier versions ignore the keyword safely).
		matHint := e.cteMaterialization(seg, pipe.Source)
		cteParts = append(cteParts, fmt.Sprintf("%s AS%s (%s)", cteName, matHint, segSQL))
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
		// O4: emit a pg_hint_plan comment when the optimizer chose a join method.
		// The hint is silently ignored if pg_hint_plan isn't installed (graceful
		// degrade). Hints reference the relation aliases (_sN, _sN_j) which are
		// the CTE names in scope. IndexLookup is structural (deferred emit — no
		// hint; the IN-list rewrite is a future PostProc path).
		hint := joinHintPG(s.Hint, alias, rightAlias)
		return fmt.Sprintf("%sSELECT %s.*, %s.* FROM %s AS %s %s JOIN (%s) AS %s ON %s",
			hint, alias, rightAlias, from, alias, joinType, rightSQL, rightAlias, on), nil
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

// joinHintPG returns a pg_hint_plan comment prefix for the optimizer-chosen
// join method (O4), or "" when no hint applies. The comment is placed before
// the SELECT so pg_hint_plan associates it with the join. pg_hint_plan uses
// relation aliases (the CTE names _sN, _sN_j) as hint targets.
//
// Hints:
//   /*+ HashJoin(left right) */     — prefer hash join
//   /*+ NestLoop(left right) */     — prefer nested loop
//   /*+ MergeJoin(left right) */    — prefer merge join
//
// JoinHintNone → "" (let pg's planner decide). JoinHintIndexLookup is a
// structural variant with no hint equivalent (its IN-list rewrite is deferred).
func joinHintPG(hint ir.JoinHint, leftAlias, rightAlias string) string {
	switch hint {
	case ir.JoinHintHash:
		return fmt.Sprintf("/*+ HashJoin(%s %s) */ ", leftAlias, rightAlias)
	case ir.JoinHintNestLoop:
		return fmt.Sprintf("/*+ NestLoop(%s %s) */ ", leftAlias, rightAlias)
	case ir.JoinHintMerge:
		return fmt.Sprintf("/*+ MergeJoin(%s %s) */ ", leftAlias, rightAlias)
	}
		return "" // JoinHintNone, JoinHintIndexLookup, or unknown → no hint
}

// cteMaterialization returns the pg 14+ MATERIALIZED hint for a CTE segment.
// When the emitter has a stats catalog (cost-based path, O6), it consults
// decision.ShouldMaterialize for a cost-based refinement of the static rule.
// Otherwise it uses the static stage-type heuristic (B3.S4): Aggregate/Join →
// MATERIALIZED, Filter/Sort/Limit → NOT MATERIALIZED. Returns "" for pg < 14
// compatibility (no keyword = pg's default behavior).
func (e *emitter) cteMaterialization(seg pgSegment, source ir.Source) string {
	// Cost-based path: if a catalog is wired, ask the optimizer.
	if e.catalog != nil {
		if cat, ok := e.catalog.(*stats.Catalog); ok {
			src := ""
			if st, ok := source.(*ir.SourceTable); ok {
				src = st.Table
			}
			hint := decision.ShouldMaterialize(cat, src, seg.stages)
			switch hint {
			case decision.MatForceMaterialize:
				return " MATERIALIZED"
			case decision.MatForceInline:
				return " NOT MATERIALIZED"
			}
			// MatDefault → fall through to static rule below.
		}
	}
	// Static stage-type heuristic (the B3.S4 default).
	if len(seg.stages) == 1 {
		switch seg.stages[0].(type) {
		case *ir.Aggregate, *ir.Join:
			return " MATERIALIZED" // large: force materialization
		case *ir.Filter, *ir.Sort, *ir.Limit, *ir.Distinct:
			return " NOT MATERIALIZED" // small: allow inlining
		}
	}
	return " NOT MATERIALIZED" // merged segments (Filter+Sort+Limit) → inline
}
