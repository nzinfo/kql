// sqlite emit: IR Pipeline → SQLite SQL (minimal e2e, P0 operators).
//
// Strategy: emit a SINGLE SQL statement using nested subqueries / CTEs, one per
// stage, reading left-to-right. Each stage wraps the previous as its FROM.
// SQLite supports window functions and CTEs, so summarize (GROUP BY), join,
// sort, limit, project, extend, distinct all map to standard SQL constructs.
//
// Column references are by string NAME (not ColID) for the minimal loop, since
// the F5 binder isn't wired yet (PROGRESS.md §2). SQLite identifiers are quoted
// with double quotes; KQL identifiers are case-sensitive so we quote
// faithfully — collisions from unquoted keywords (order, count) are avoided.
//
// Simplifications for the minimal loop (recorded; revisit with optimizer):
//   - No subquery-folding; each stage is its own SELECT layer. Correctness over
//     prettiness; the optimizer (O-line) will fuse layers later.
//   - String operators (has/contains/startswith/...) map to LIKE / instr()
//     approximations. Case sensitivity differs from KQL but is acceptable for
//     the e2e validation loop; F7 + per-function caps will refine this.
package sqlite

import (
	"fmt"
	"strconv"
	"strings"

	"nzinfo/kql/internal/backend"
	"nzinfo/kql/internal/ir"
)

// Emit translates an IR Pipeline into a SQLite backend.Query.
//
// Bind-arg ordering: SQLite supports NUMBERED placeholders (?1, ?2, …), so we
// assign each literal a stable index as it's emitted and collect args into a
// map keyed by index. This sidesteps the ordering problem that arises when
// stages are nested (a later/wrapping stage's placeholder appears to the LEFT
// of an earlier stage's in the final SQL text) — with numbered placeholders the
// args map is order-independent. The final args slice is built in index order.
func Emit(pipe *ir.Pipeline) (*backend.Query, error) {
	e := newEmitter()
	sql, err := e.emitPipeline(pipe)
	if err != nil {
		return nil, err
	}
	return &backend.Query{SQL: sql, Args: e.orderedArgs()}, nil
}

// newEmitter returns a fresh emitter for a single emit run.
func newEmitter() *emitter {
	return &emitter{args: map[int]interface{}{}, postProc: map[string]bool{}}
}

// emitter tracks numbered placeholder args during a single emit run.
// nextIdx is the next free placeholder index (1-based); args maps idx→value.
// postProc collects function names that the catalog flagged NeedsPostProc
// (hook for the client-side post-proc framework; not acted on in the minimal loop).
type emitter struct {
	nextIdx  int
	args     map[int]interface{}
	postProc map[string]bool
}

// bind assigns the next placeholder index to value and returns "?N".
func (e *emitter) bind(value interface{}) string {
	e.nextIdx++
	e.args[e.nextIdx] = value
	return fmt.Sprintf("?%d", e.nextIdx)
}

// orderedArgs returns the args as a slice ordered by placeholder index.
func (e *emitter) orderedArgs() []interface{} {
	out := make([]interface{}, 0, len(e.args))
	for i := 1; i <= e.nextIdx; i++ {
		out = append(out, e.args[i])
	}
	return out
}

// emitPipeline builds the SQL for a pipeline by nesting subqueries one per
// stage (source innermost). Uses the shared numbered-arg emitter.
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

// emitSource returns the FROM clause for a source (a quoted table name or a
// subquery for nested pipelines). No bind args for a plain table ref.
func emitSource(src ir.Source) (string, error) {
	switch s := src.(type) {
	case *ir.SourceTable:
		return quoteIdent(s.Table), nil
	case *ir.SourceDatatableLit:
		return emitDatatableValues(s), nil
	case nil:
		return "(SELECT 1)", nil
	}
	return "", fmt.Errorf("unsupported source %T", src)
}

// emitDatatableValues emits a datatable literal as a SQL VALUES clause:
// (VALUES (1, 'A'), (2, 'B')) AS _dt(col1, col2)
func emitDatatableValues(s *ir.SourceDatatableLit) string {
	if len(s.Rows) == 0 {
		return "(SELECT NULL AS " + quoteIdent(s.ColNames[0]) + " WHERE 1=0)"
	}
	var parts []string
	for _, row := range s.Rows {
		var cells []string
		for _, cell := range row {
			cells = append(cells, emitDatatableCell(cell))
		}
		parts = append(parts, "("+strings.Join(cells, ", ")+")")
	}
	colNames := make([]string, len(s.ColNames))
	for i, n := range s.ColNames {
		colNames[i] = n
	}
	// SQLite doesn't support column aliases on VALUES AS _dt(Name, Value).
		// Use: SELECT column1 AS Name, column2 AS Value FROM (VALUES ...)
		var selectCols []string
		for i, n := range s.ColNames {
			selectCols = append(selectCols, fmt.Sprintf("column%d AS %s", i+1, quoteIdent(n)))
		}
		return "(SELECT " + strings.Join(selectCols, ", ") + " FROM (VALUES " + strings.Join(parts, ", ") + "))"
}

// emitStage wraps `inner` (the SQL producing the prior stage's rows) in a new
// SELECT applying the given stage's operator.
func (e *emitter) emitStage(inner string, st ir.Stage) (string, error) {
	// "prev" is the alias for the inner query throughout.
	const prev = "_k0"
	fromClause := fmt.Sprintf("(%s) AS %s", inner, prev)
	switch s := st.(type) {
	case *ir.Filter:
		pred, err := e.emitExpr(s.Predicate, prev)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("SELECT * FROM %s WHERE %s", fromClause, pred), nil

	case *ir.Project:
		cols, err := e.emitNamedList(s.Cols, prev)
		if err != nil {
			return "", err
		}
		if len(cols) == 0 {
			cols = []string{"*"}
		}
		return fmt.Sprintf("SELECT %s FROM %s", strings.Join(cols, ", "), fromClause), nil

	case *ir.Extend:
		// Extend keeps all input columns and adds computed ones: emit SELECT
		// _k0.*, <computed> AS name.
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
		return fmt.Sprintf("SELECT %s FROM %s", strings.Join(parts, ", "), fromClause), nil

	case *ir.Aggregate:
		return e.emitAggregate(fromClause, prev, s)

	case *ir.Sort:
		if len(s.Keys) == 0 {
			return fmt.Sprintf("SELECT * FROM %s", fromClause), nil
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
			nulls := ""
			if k.NullsFirst {
				if k.Desc {
					nulls = "" // DESC NULLS FIRST is SQLite's default for DESC? no — be explicit
				}
				_ = nulls
			}
			// SQLite supports NULLS FIRST/LAST since 3.30.
			if k.NullsFirst {
				parts = append(parts, fmt.Sprintf("%s %s NULLS FIRST", ex, dir))
			} else {
				parts = append(parts, fmt.Sprintf("%s %s", ex, dir))
			}
		}
		return fmt.Sprintf("SELECT * FROM %s ORDER BY %s", fromClause, strings.Join(parts, ", ")), nil

	case *ir.Limit:
		ex, err := e.emitExpr(s.Count, prev)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("SELECT * FROM %s LIMIT %s", fromClause, ex), nil

	case *ir.Distinct:
		// distinct over a column set → SELECT DISTINCT <cols>.
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
		return fmt.Sprintf("SELECT DISTINCT %s FROM %s", strings.Join(cols, ", "), fromClause), nil

	case *ir.Join:
		return e.emitJoin(fromClause, prev, s)

	case *ir.Union:
		return e.emitUnion(fromClause, prev, s)
	case *ir.MvExpand, *ir.Parse, *ir.MakeSeries:
		// PostProc stages: executed client-side by exec.applyPostProc. When
		// reached here (direct Emit without the exec split), pass through as
		// SELECT * so the query is structurally valid (the real semantics run
		// in the exec PostProc layer, not in SQL).
		return fmt.Sprintf("SELECT * FROM %s", fromClause), nil
	}
	return "", fmt.Errorf("unsupported stage %T", st)
}

// emitAggregate: summarize <aggs> by <keys> → GROUP BY with the keys, selecting
// both keys and aggregates.
func (e *emitter) emitAggregate(from, prev string, s *ir.Aggregate) (string, error) {
	selectParts := make([]string, 0, len(s.Keys)+len(s.Aggregates))
	groupParts := make([]string, 0, len(s.Keys))
	for i, k := range s.Keys {
		ex, err := e.emitExpr(k.Expr, prev)
		if err != nil {
			return "", err
		}
		// Alias: prefer the explicit name; for a bare column reference, keep
		// the ORIGINAL column name (so `summarize ... by state` projects
		// `state`, and a following `sort by state` still resolves). Only fall
		// back to key%d for computed keys without a name.
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

// emitJoin: join kinds map to INNER/LEFT/RIGHT/FULL JOIN.
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
	return fmt.Sprintf("SELECT * FROM %s %s JOIN (%s) AS %s_j ON %s",
		from, joinType, rightSQL, prev, on), nil
}

// emitPipelineRight is the join's right-side subquery emitter; it reuses the
// shared emitter so arg numbering is globally consistent across left/right.
func (e *emitter) emitPipelineRight(p *ir.Pipeline) (string, error) {
	return e.emitPipeline(p)
}

// emitUnion: union T1, T2 → chain of UNION ALL.
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
	// SQLite dedup vs not: KQL union dedups; use UNION (not UNION ALL).
	return strings.Join(parts, " UNION "), nil
}

// emitNamedList emits a comma-separated list of namedExprs (for project).
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

// emitDatatableCell renders an IR literal expression for use in a VALUES clause.
func emitDatatableCell(e ir.Expr) string {
	if lit, ok := e.(*ir.Lit); ok && lit.HasValue {
		switch v := lit.Value.(type) {
		case string:
			return "'" + strings.ReplaceAll(v, "'", "''") + "'"
		case int64:
			return strconv.FormatInt(v, 10)
		case float64:
			return strconv.FormatFloat(v, 'f', -1, 64)
		case bool:
			if v { return "1" }
			return "0"
		}
	}
	return "NULL"
}
