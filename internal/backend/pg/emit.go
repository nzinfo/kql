// Package pg is a backend.Backend implementation over PostgreSQL via the
// pgx v5 driver (pure-Go, per DESIGN.md §7). It emits SQL from an IR Pipeline
// and executes it.
//
// The emit structure mirrors the sqlite backend (nested-subquery-per-stage),
// with two dialect differences: numbered placeholders use $N (pg style, not
// sqlite's ?N) and identifiers are double-quoted (same as sqlite; both use
// standard SQL identifiers). String operators use pg's ILIKE (true case-
// insensitive, vs sqlite's ASCII-only LIKE).
package pg

import (
	"fmt"
	"strconv"
	"strings"

	"nzinfo/kql/internal/backend"
	"nzinfo/kql/internal/ir"
)

// Emit translates an IR Pipeline into a PostgreSQL backend.Query.
//
// Bind-arg ordering uses pg's $N numbered placeholders — order-independent of
// the nested-subquery text (see sqlite backend's NOTES for why numbering
// matters: wrapping stages put their placeholders to the LEFT of inner stages').
func Emit(pipe *ir.Pipeline) (*backend.Query, error) {
	e := newEmitter()
	sql, err := e.emitPipeline(pipe)
	if err != nil {
		return nil, err
	}
	return &backend.Query{SQL: sql, Args: e.orderedArgs()}, nil
}

// emitter tracks numbered placeholder args during a single emit run. pg uses
// $N placeholders; nextIdx is the next free index (1-based); args maps idx→value.
type emitter struct {
	nextIdx int
	args    map[int]interface{}
	postProc map[string]bool
	catalog  interface{} // *stats.Catalog or nil (cost-based CTE materialization)
}

func newEmitter() *emitter { return &emitter{args: map[int]interface{}{}, postProc: map[string]bool{}} }

// bind assigns the next placeholder index to value and returns "$N".
func (e *emitter) bind(value interface{}) string {
	e.nextIdx++
	e.args[e.nextIdx] = value
	return fmt.Sprintf("$%d", e.nextIdx)
}

// orderedArgs returns args as a slice ordered by placeholder index.
func (e *emitter) orderedArgs() []interface{} {
	out := make([]interface{}, 0, len(e.args))
	for i := 1; i <= e.nextIdx; i++ {
		out = append(out, e.args[i])
	}
	return out
}

// emitPipeline builds SQL by nesting subqueries one per stage (source innermost).
func (e *emitter) emitPipeline(pipe *ir.Pipeline) (string, error) {
	if pipe == nil {
		return "", fmt.Errorf("nil pipeline")
	}
	var from string
	if pipe.Source != nil {
		srcSQL, err := emitSource(pipe.Source)
		if err != nil {
			return "", err
		}
		from = srcSQL
	} else {
		from = "(SELECT 1)"
	}
	current := fmt.Sprintf("SELECT * FROM %s", from)
	for _, st := range pipe.Stages {
		next, err := e.emitStage(current, st)
		if err != nil {
			return "", err
		}
		current = next
	}
	return current, nil
}

func emitSource(src ir.Source) (string, error) {
	switch s := src.(type) {
	case *ir.SourceTable:
		return quoteIdent(s.Table), nil
	case *ir.SourceDatatableLit:
		return pgEmitDatatableValues(s), nil
	case nil:
		return "(SELECT 1)", nil
	}
	return "", fmt.Errorf("unsupported source %T", src)
}

// emitStage wraps inner in a SELECT applying the stage. Mirror of sqlite's
// emitStage; differences only in expression emission (placeholders, ILIKE).
func (e *emitter) emitStage(inner string, st ir.Stage) (string, error) {
	const prev = "_k0"
	from := fmt.Sprintf("(%s) AS %s", inner, prev)
	switch s := st.(type) {
	case *ir.Filter:
		pred, err := e.emitExpr(s.Predicate, prev)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("SELECT * FROM %s WHERE %s", from, pred), nil
	case *ir.Project:
		cols, err := e.emitNamedList(s.Cols, prev)
		if err != nil {
			return "", err
		}
		if len(cols) == 0 {
			cols = []string{"*"}
		}
		return fmt.Sprintf("SELECT %s FROM %s", strings.Join(cols, ", "), from), nil
	case *ir.Extend:
		parts := []string{prev + ".*"}
		for _, c := range s.Cols {
			ex, err := e.emitExpr(c.Expr, prev)
			if err != nil {
				return "", err
			}
			if c.Name != "" {
				parts = append(parts, fmt.Sprintf("%s AS %s", ex, quoteIdent(c.Name)))
			} else {
				parts = append(parts, ex)
			}
		}
		return fmt.Sprintf("SELECT %s FROM %s", strings.Join(parts, ", "), from), nil
	case *ir.Aggregate:
		return e.emitAggregate(from, prev, s)
	case *ir.Sort:
		if len(s.Keys) == 0 {
			return fmt.Sprintf("SELECT * FROM %s", from), nil
		}
		parts := make([]string, 0, len(s.Keys))
		for _, k := range s.Keys {
			ex, err := e.emitExpr(k.Expr, prev)
			if err != nil {
				return "", err
			}
			dir := "ASC"
			if k.Desc {
				dir = "DESC"
			}
			if k.NullsFirst {
				parts = append(parts, fmt.Sprintf("%s %s NULLS FIRST", ex, dir))
			} else {
				parts = append(parts, fmt.Sprintf("%s %s", ex, dir))
			}
		}
		return fmt.Sprintf("SELECT * FROM %s ORDER BY %s", from, strings.Join(parts, ", ")), nil
	case *ir.Limit:
		ex, err := e.emitExpr(s.Count, prev)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("SELECT * FROM %s LIMIT %s", from, ex), nil
	case *ir.Distinct:
		cols := make([]string, 0, len(s.Cols))
		for _, c := range s.Cols {
			ex, err := e.emitExpr(c, prev)
			if err != nil {
				return "", err
			}
			cols = append(cols, ex)
		}
		if len(cols) == 0 {
			cols = []string{"*"}
		}
		return fmt.Sprintf("SELECT DISTINCT %s FROM %s", strings.Join(cols, ", "), from), nil
	case *ir.Join:
		return e.emitJoin(from, prev, s)
	case *ir.Union:
		return e.emitUnion(from, prev, s)
	case *ir.MvExpand, *ir.Parse, *ir.MakeSeries, *ir.MvApply:
		// PostProc stages: client-side in exec; SELECT * on direct Emit.
		return fmt.Sprintf("SELECT * FROM %s", from), nil
	}
	return "", fmt.Errorf("unsupported stage %T", st)
}

func (e *emitter) emitAggregate(from, prev string, s *ir.Aggregate) (string, error) {
	selectParts := make([]string, 0, len(s.Keys)+len(s.Aggregates))
	groupParts := make([]string, 0, len(s.Keys))
	for i, k := range s.Keys {
		ex, err := e.emitExpr(k.Expr, prev)
		if err != nil {
			return "", err
		}
		alias := k.Name
		if alias == "" {
			if col, ok := k.Expr.(*ir.Col); ok && col.Name != "" {
				alias = col.Name
			} else {
				alias = fmt.Sprintf("key%d", i)
			}
		}
		selectParts = append(selectParts, fmt.Sprintf("%s AS %s", ex, quoteIdent(alias)))
		groupParts = append(groupParts, ex)
	}
	for i, a := range s.Aggregates {
		ex, err := e.emitExpr(a.Expr, prev)
		if err != nil {
			return "", err
		}
		alias := a.Name
		if alias == "" {
			alias = fmt.Sprintf("agg%d", i)
		}
		selectParts = append(selectParts, fmt.Sprintf("%s AS %s", ex, quoteIdent(alias)))
	}
	if len(selectParts) == 0 {
		return "", fmt.Errorf("summarize with no aggregates")
	}
	sql := fmt.Sprintf("SELECT %s FROM %s", strings.Join(selectParts, ", "), from)
	if len(groupParts) > 0 {
		sql += " GROUP BY " + strings.Join(groupParts, ", ")
	}
	return sql, nil
}

func (e *emitter) emitJoin(from, prev string, s *ir.Join) (string, error) {
	if s.Right == nil {
		return "", fmt.Errorf("join with no right side")
	}
	rightSQL, err := e.emitPipeline(s.Right)
	if err != nil {
		return "", err
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
	onParts := make([]string, 0, len(s.On))
	leftAlias := prev
	rightAlias := prev + "_j"
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
	// O4: pg_hint_plan comment for the optimizer-chosen join method (same as
	// the CTE path; see emit_cte.go joinHintPG).
	hint := joinHintPG(s.Hint, leftAlias, rightAlias)
	return fmt.Sprintf("%sSELECT * FROM %s %s JOIN (%s) AS %s_j ON %s",
		hint, from, joinType, rightSQL, prev, on), nil
}

func (e *emitter) emitUnion(from, prev string, s *ir.Union) (string, error) {
	if len(s.Inputs) == 0 {
		return fmt.Sprintf("SELECT * FROM %s", from), nil
	}
	parts := []string{fmt.Sprintf("SELECT * FROM %s", from)}
	for _, in := range s.Inputs {
		subSQL, err := e.emitPipeline(in)
		if err != nil {
			return "", err
		}
		parts = append(parts, fmt.Sprintf("SELECT * FROM (%s)", subSQL))
	}
	return strings.Join(parts, " UNION "), nil
}

func (e *emitter) emitNamedList(cols []*ir.NamedExpr, alias string) ([]string, error) {
	out := make([]string, 0, len(cols))
	for _, c := range cols {
		ex, err := e.emitExpr(c.Expr, alias)
		if err != nil {
			return nil, err
		}
		if c.Name != "" {
			out = append(out, fmt.Sprintf("%s AS %s", ex, quoteIdent(c.Name)))
		} else {
			out = append(out, ex)
		}
	}
	return out, nil
}

// pgEmitDatatableValues emits a datatable literal as a SQL VALUES clause for pg.
func pgEmitDatatableValues(s *ir.SourceDatatableLit) string {
	if len(s.Rows) == 0 {
		return "(SELECT NULL AS " + quoteIdent(s.ColNames[0]) + " WHERE 1=0)"
	}
	colNames := make([]string, len(s.ColNames))
	for i, n := range s.ColNames {
		colNames[i] = n
	}
	var parts []string
	for _, row := range s.Rows {
		var cells []string
		for _, cell := range row {
			cells = append(cells, pgEmitDatatableCell(cell))
		}
		parts = append(parts, "("+strings.Join(cells, ", ")+")")
	}
	return "(SELECT * FROM (VALUES " + strings.Join(parts, ", ") + ") AS _dt(" + strings.Join(colNames, ", ") + ")"
}

func pgEmitDatatableCell(e ir.Expr) string {
	if lit, ok := e.(*ir.Lit); ok && lit.HasValue {
		switch v := lit.Value.(type) {
		case string:
			return "'" + strings.ReplaceAll(v, "'", "''") + "'"
		case int64:
			return strconv.FormatInt(v, 10)
		case float64:
			return strconv.FormatFloat(v, 'f', -1, 64)
		case bool:
			if v { return "TRUE" }
			return "FALSE"
		}
	}
	return "NULL"
}
