// Package duckdb — independent DuckDB emitter (decoupled from pg.Emit).
//
// DuckDB's SQL dialect is Postgres-compatible but has important differences that
// affect performance:
//
//   - No pg_hint_plan: join method hints are meaningless and add planner noise.
//     This emitter skips hint comments entirely.
//   - No MATERIALIZED/NOT MATERIALIZED CTE hint (DuckDB always materializes CTEs).
//   - Native UNNEST for mv-expand (future: replace client-side PostProc).
//   - Native list/struct types for dynamic columns.
//   - GENERATE_SERIES for range/datatable.
//
// The emitter shares the IR → SQL translation logic with pg (CTE splitting,
// expression emission, placeholder numbering) but removes pg-specific artifacts
// and can add DuckDB-specific optimizations.
package duckdb

import (
	"fmt"
	"strings"

	"nzinfo/kql/internal/backend"
	"nzinfo/kql/internal/frontend/token"
	"nzinfo/kql/internal/ir"
)

// emitter is the DuckDB SQL builder. It mirrors pg's emitter structure but
// omits pg-specific features (hints, MATERIALIZED, ILIKE→LIKE normalization).
type emitter struct {
	args []interface{}
}

// Emit translates an IR Pipeline into a DuckDB Query. Uses CTE-based emit
// (same breakpoint splitting as pg) but without pg-specific hints.
func Emit(pipe *ir.Pipeline) (*backend.Query, error) {
	if pipe == nil {
		return nil, fmt.Errorf("nil pipeline")
	}
	e := &emitter{}
	sql, err := e.emitPipelineCTE(pipe)
	if err != nil {
		// Fallback: simple nested emit.
		e2 := &emitter{}
		sql, err = e2.emitPipeline(pipe)
		if err != nil {
			return nil, err
		}
		e = e2
	}
	return &backend.Query{SQL: sql, Args: e.args}, nil
}

// emitPipelineCTE emits the pipeline as a chain of CTEs (WITH _s0 AS (...), ...).
// This is the production emit path — DuckDB handles CTEs well and the stage
// splitting enables clear SQL structure.
func (e *emitter) emitPipelineCTE(pipe *ir.Pipeline) (string, error) {
	segments := splitSegments(pipe.Stages)
	if len(segments) == 0 {
		// No stages — just SELECT * FROM source.
		from, err := e.emitSource(pipe.Source)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("SELECT * FROM %s", from), nil
	}

	var cteParts []string
	from, err := e.emitSource(pipe.Source)
	if err != nil {
		return "", err
	}

	for i, seg := range segments {
		cteName := fmt.Sprintf("_s%d", i)
		var segSQL string
		if len(seg.stages) == 1 && isBreakpoint(seg.stages[0]) {
			// Single breakpoint stage → direct emit.
			segSQL, err = e.emitBreakpointDirect(seg.stages[0], from, cteName)
		} else {
			// Merged stages → single SELECT with WHERE/ORDER BY/LIMIT.
			segSQL, err = e.emitMergedSelect(seg.stages, from, cteName)
		}
		if err != nil {
			return "", err
		}
		// DuckDB: no MATERIALIZED hint (always materialized).
		cteParts = append(cteParts, fmt.Sprintf("%s AS (%s)", cteName, segSQL))
		from = cteName
	}

	if len(cteParts) == 1 {
		return fmt.Sprintf("WITH %s SELECT * FROM %s", cteParts[0], from), nil
	}
	return fmt.Sprintf("WITH %s SELECT * FROM %s", strings.Join(cteParts, ", "), from), nil
}

// emitPipeline is the fallback nested-subquery emit path.
func (e *emitter) emitPipeline(pipe *ir.Pipeline) (string, error) {
	from, err := e.emitSource(pipe.Source)
	if err != nil {
		return "", err
	}
	for _, st := range pipe.Stages {
		from, err = e.emitStage(st, from)
		if err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("SELECT * FROM %s", from), nil
}

// emitSource emits the FROM clause for a source node.
func (e *emitter) emitSource(src ir.Source) (string, error) {
	if src == nil {
		return "(SELECT 1)", nil // dummy
	}
	switch s := src.(type) {
	case *ir.SourceTable:
		return quoteIdent(s.Table), nil
	case *ir.SourceDatatable:
		return e.emitDatatable(s)
	}
	return fmt.Sprintf("%T", src), fmt.Errorf("unsupported source type")
}

// emitDatatable emits a datatable literal as a VALUES clause.
func (e *emitter) emitDatatable(dt *ir.SourceDatatable) (string, error) {
	if dt == nil || len(dt.Schema) == 0 {
		return "(SELECT 1 WHERE 1=0)", nil
	}
	var parts []string
	parts = append(parts, "(SELECT")
	for i := range dt.Schema {
		if i > 0 {
			parts = append(parts, ",")
		}
		parts = append(parts, fmt.Sprintf("column%d AS col_%d", i+1, i))
	}
	parts = append(parts, "FROM (VALUES")
	for ri, row := range dt.Rows {
		if ri > 0 {
			parts = append(parts, ",")
		}
		var vals []string
		for _, v := range row {
			s, err := e.emitExpr(v, "")
			if err != nil {
				return "", err
			}
			vals = append(vals, s)
		}
		parts = append(parts, fmt.Sprintf("(%s)", strings.Join(vals, ",")))
	}
	parts = append(parts, "))")
	return strings.Join(parts, " "), nil
}

// --- Stage emission ---

func (e *emitter) emitStage(st ir.Stage, from string) (string, error) {
	switch s := st.(type) {
	case *ir.Filter:
		cond, err := e.emitExpr(s.Predicate, from)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("SELECT * FROM %s WHERE %s", from, cond), nil
	case *ir.Project:
		return e.emitProject(s, from)
	case *ir.Extend:
		return e.emitExtend(s, from)
	case *ir.Aggregate:
		return e.emitAggregate(s, from)
	case *ir.Sort:
		return e.emitSort(s, from)
	case *ir.Limit:
		return e.emitLimit(s, from)
	case *ir.Join:
		return e.emitJoin(s, from)
	case *ir.Distinct:
		return e.emitDistinct(s, from)
	case *ir.Union:
		return e.emitUnion(s, from)
	}
	// Passthrough for unhandled stages.
	return fmt.Sprintf("SELECT * FROM %s", from), nil
}

func (e *emitter) emitProject(s *ir.Project, from string) (string, error) {
	var cols []string
	for _, c := range s.Cols {
		if _, ok := c.Expr.(*ir.Star); ok {
			cols = append(cols, "*")
			continue
		}
		expr, err := e.emitExpr(c.Expr, from)
		if err != nil {
			return "", err
		}
		if c.Name != "" {
			cols = append(cols, fmt.Sprintf("%s AS %s", expr, quoteIdent(c.Name)))
		} else {
			cols = append(cols, expr)
		}
	}
	if len(cols) == 0 {
		cols = []string{"*"}
	}
	return fmt.Sprintf("SELECT %s FROM %s", strings.Join(cols, ", "), from), nil
}

func (e *emitter) emitExtend(s *ir.Extend, from string) (string, error) {
	var cols []string
	cols = append(cols, "*")
	for _, c := range s.Cols {
		expr, err := e.emitExpr(c.Expr, from)
		if err != nil {
			return "", err
		}
		cols = append(cols, fmt.Sprintf("%s AS %s", expr, quoteIdent(c.Name)))
	}
	return fmt.Sprintf("SELECT %s FROM %s", strings.Join(cols, ", "), from), nil
}

func (e *emitter) emitAggregate(s *ir.Aggregate, from string) (string, error) {
	var selectParts []string
	// Group-by keys first.
	for _, k := range s.Keys {
		expr, err := e.emitExpr(k.Expr, from)
		if err != nil {
			return "", err
		}
		if k.Name != "" {
			selectParts = append(selectParts, fmt.Sprintf("%s AS %s", expr, quoteIdent(k.Name)))
		} else {
			selectParts = append(selectParts, expr)
		}
	}
	// Aggregates.
	for _, a := range s.Aggregates {
		expr, err := e.emitExpr(a.Expr, from)
		if err != nil {
			return "", err
		}
		if a.Name != "" {
			selectParts = append(selectParts, fmt.Sprintf("%s AS %s", expr, quoteIdent(a.Name)))
		} else {
			selectParts = append(selectParts, expr)
		}
	}
	var groupBy string
	if len(s.Keys) > 0 {
		var gb []string
		for i := 1; i <= len(s.Keys); i++ {
			gb = append(gb, fmt.Sprintf("%d", i))
		}
		groupBy = fmt.Sprintf(" GROUP BY %s", strings.Join(gb, ", "))
	}
	return fmt.Sprintf("SELECT %s FROM %s%s", strings.Join(selectParts, ", "), from, groupBy), nil
}

func (e *emitter) emitSort(s *ir.Sort, from string) (string, error) {
	var parts []string
	for _, k := range s.Keys {
		expr, err := e.emitExpr(k.Expr, from)
		if err != nil {
			return "", err
		}
		dir := "ASC"
		if k.Desc {
			dir = "DESC"
		}
		parts = append(parts, fmt.Sprintf("%s %s", expr, dir))
	}
	return fmt.Sprintf("SELECT * FROM %s ORDER BY %s", from, strings.Join(parts, ", ")), nil
}

func (e *emitter) emitLimit(s *ir.Limit, from string) (string, error) {
	count, err := e.emitExpr(s.Count, from)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("SELECT * FROM %s LIMIT %s", from, count), nil
}

func (e *emitter) emitJoin(s *ir.Join, from string) (string, error) {
	rightSQL, err := e.emitPipeline(s.Right)
	if err != nil {
		return "", err
	}
	rightAlias := "j"
	joinType := "INNER"
	switch s.Kind {
	case ir.JoinLeftOuter:
		joinType = "LEFT"
	case ir.JoinRightOuter:
		joinType = "RIGHT"
	case ir.JoinFullOuter:
		joinType = "FULL"
	}
	// DuckDB: no join method hints (no pg_hint_plan). The hint field is
	// ignored — DuckDB's own planner chooses the join method.
	var onParts []string
	for _, cond := range s.On {
		on, err := e.emitJoinOnExpr(cond, from, rightAlias)
		if err != nil {
			return "", err
		}
		onParts = append(onParts, on)
	}
	on := "1=1"
	if len(onParts) > 0 {
		on = strings.Join(onParts, " AND ")
	}
	return fmt.Sprintf("SELECT %s.*, %s.* FROM %s %s JOIN (%s) AS %s ON %s",
		from, rightAlias, from, joinType, rightSQL, rightAlias, on), nil
}

func (e *emitter) emitDistinct(s *ir.Distinct, from string) (string, error) {
	if len(s.Cols) == 0 {
		return fmt.Sprintf("SELECT DISTINCT * FROM %s", from), nil
	}
	var cols []string
	for _, c := range s.Cols {
		expr, err := e.emitExpr(c, from)
		if err != nil {
			return "", err
		}
		cols = append(cols, expr)
	}
	return fmt.Sprintf("SELECT DISTINCT %s FROM %s", strings.Join(cols, ", "), from), nil
}

func (e *emitter) emitUnion(s *ir.Union, from string) (string, error) {
	var parts []string
	parts = append(parts, fmt.Sprintf("SELECT * FROM %s", from))
	for _, input := range s.Inputs {
		subSQL, err := e.emitPipeline(input)
		if err != nil {
			return "", err
		}
		parts = append(parts, fmt.Sprintf("UNION SELECT * FROM (%s)", subSQL))
	}
	return "(" + strings.Join(parts, " ") + ")", nil
}

// --- Expression emission ---

func (e *emitter) emitExpr(expr ir.Expr, alias string) (string, error) {
	switch n := expr.(type) {
	case *ir.Lit:
		return e.emitLit(n)
	case *ir.Col:
		if alias != "" {
			return fmt.Sprintf("%s.%s", alias, quoteIdent(n.Name)), nil
		}
		return quoteIdent(n.Name), nil
	case *ir.Star:
		return "*", nil
	case *ir.BinOp:
		return e.emitBinOp(n, alias)
	case *ir.UnaryOp:
		x, err := e.emitExpr(n.X, alias)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s%s", n.Op, x), nil
	case *ir.FuncCall:
		return e.emitFuncCall(n, alias)
	case *ir.Member:
		x, err := e.emitExpr(n.X, alias)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s.%s", x, n.Field), nil
	case *ir.Case:
		cond, err := e.emitExpr(n.Cond, alias)
		if err != nil {
			return "", err
		}
		then, err := e.emitExpr(n.Then, alias)
		if err != nil {
			return "", err
		}
		els, err := e.emitExpr(n.Else, alias)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("CASE WHEN %s THEN %s ELSE %s END", cond, then, els), nil
	case *ir.List:
		var parts []string
		for _, el := range n.Elems {
			s, err := e.emitExpr(el, alias)
			if err != nil {
				return "", err
			}
			parts = append(parts, s)
		}
		return "(" + strings.Join(parts, ", ") + ")", nil
	}
	return "", fmt.Errorf("unsupported expr type %T", expr)
}

func (e *emitter) emitLit(n *ir.Lit) (string, error) {
	if !n.HasValue {
		return "NULL", nil
	}
	switch n.T {
	case ir.TypeInt, ir.TypeLong:
		// Inline integers (DuckDB infers type from literal).
		return fmt.Sprintf("%v", n.Value), nil
	case ir.TypeBool:
		if n.Value == true {
			return "TRUE", nil
		}
		return "FALSE", nil
	}
	// All other types: bind as parameter ($N).
	ph := e.bind(n.Value)
	return ph, nil
}

func (e *emitter) emitBinOp(n *ir.BinOp, alias string) (string, error) {
	// IN-list handling.
	if n.Op == token.IN || n.Op == token.NOTIN || n.Op == token.INCI || n.Op == token.NOTINCI {
		return e.emitInList(n, alias)
	}
	// String operators — DuckDB supports ILIKE (same as pg).
	x, err := e.emitExpr(n.X, alias)
	if err != nil {
		return "", err
	}
	y, err := e.emitExpr(n.Y, alias)
	if err != nil {
		return "", err
	}
	switch n.Op {
	case token.HAS, token.CONTAINS:
		return fmt.Sprintf("(%s ILIKE '%%' || %s || '%%')", x, y), nil
	case token.NOTHAS, token.NOTCONTAINS:
		return fmt.Sprintf("NOT (%s ILIKE '%%' || %s || '%%')", x, y), nil
	case token.STARTSWITH, token.HASPREFIX:
		return fmt.Sprintf("(%s ILIKE %s || '%%')", x, y), nil
	case token.ENDSWITH, token.HASSUFFIX:
		return fmt.Sprintf("(%s ILIKE '%%' || %s)", x, y), nil
	case token.TILDE:
		return fmt.Sprintf("(%s ILIKE %s)", x, y), nil // case-insensitive eq
	case token.NTILDE:
		return fmt.Sprintf("NOT (%s ILIKE %s)", x, y), nil
	case token.MATCHESREGEX:
		return fmt.Sprintf("regexp_matches(%s, %s)", x, y), nil
	// like / !like: case-INsensitive → ILIKE. like_cs / !like_cs: case-sensitive → LIKE.
	case token.LIKE:
		return fmt.Sprintf("(%s ILIKE %s)", x, y), nil
	case token.NOTLIKE:
		return fmt.Sprintf("NOT (%s ILIKE %s)", x, y), nil
	case token.LIKECS:
		return fmt.Sprintf("(%s LIKE %s)", x, y), nil
	case token.NOTLIKECS:
		return fmt.Sprintf("NOT (%s LIKE %s)", x, y), nil
	}
	return fmt.Sprintf("(%s %s %s)", x, n.Op, y), nil
}

func (e *emitter) emitInList(n *ir.BinOp, alias string) (string, error) {
	x, err := e.emitExpr(n.X, alias)
	if err != nil {
		return "", err
	}
	list, ok := n.Y.(*ir.List)
	if !ok {
		y, err := e.emitExpr(n.Y, alias)
		if err != nil {
			return "", err
		}
		if n.Op == token.NOTIN {
			return fmt.Sprintf("(%s NOT IN (%s))", x, y), nil
		}
		return fmt.Sprintf("(%s IN (%s))", x, y), nil
	}
	// DuckDB: expand IN-list as individual placeholders (no = ANY array).
	phs := make([]string, 0, len(list.Elems))
	for _, el := range list.Elems {
		ph, err := e.emitExpr(el, alias)
		if err != nil {
			return "", err
		}
		phs = append(phs, ph)
	}
	joined := strings.Join(phs, ", ")
	switch n.Op {
	case token.NOTIN, token.NOTINCI:
		return fmt.Sprintf("(%s NOT IN (%s))", x, joined), nil
	default:
		return fmt.Sprintf("(%s IN (%s))", x, joined), nil
	}
}

func (e *emitter) emitFuncCall(n *ir.FuncCall, alias string) (string, error) {
	// DuckDB-specific function overrides (where DuckDB differs from pg).
	name := n.Name
	switch n.Name {
	case "iff", "iif":
		name = "if" // DuckDB uses `if(cond, then, else)`, not `iff`/`iif`
	case "tostring":
		if len(n.Args) > 0 {
			arg, err := e.emitExpr(n.Args[0], alias)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("CAST(%s AS VARCHAR)", arg), nil
		}
	case "tobool":
		if len(n.Args) > 0 {
			arg, err := e.emitExpr(n.Args[0], alias)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("CAST(%s AS BOOLEAN)", arg), nil
		}
	case "tolong", "toint":
		if len(n.Args) > 0 {
			arg, err := e.emitExpr(n.Args[0], alias)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("CAST(%s AS BIGINT)", arg), nil
		}
	case "toreal", "todouble":
		if len(n.Args) > 0 {
			arg, err := e.emitExpr(n.Args[0], alias)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("CAST(%s AS DOUBLE)", arg), nil
		}
	case "isnull":
		if len(n.Args) > 0 {
			arg, err := e.emitExpr(n.Args[0], alias)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("(%s IS NULL)", arg), nil
		}
	case "isnotnull":
		if len(n.Args) > 0 {
			arg, err := e.emitExpr(n.Args[0], alias)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("(%s IS NOT NULL)", arg), nil
		}
	case "isempty":
		if len(n.Args) > 0 {
			arg, err := e.emitExpr(n.Args[0], alias)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("(%s = '')", arg), nil
		}
	case "isnotempty", "notempty":
		if len(n.Args) > 0 {
			arg, err := e.emitExpr(n.Args[0], alias)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("(%s != '')", arg), nil
		}
	case "strcat":
		// DuckDB: use string concatenation with ||.
		var parts []string
		for _, a := range n.Args {
			s, err := e.emitExpr(a, alias)
			if err != nil {
				return "", err
			}
			parts = append(parts, fmt.Sprintf("CAST(%s AS VARCHAR)", s))
		}
		return strings.Join(parts, " || "), nil
	case "tolower":
		name = "lower"
	case "toupper":
		name = "upper"
	case "strlen", "string_size":
		name = "length"
	case "make_set":
		// DuckDB: list aggregation.
		if len(n.Args) > 0 {
			arg, err := e.emitExpr(n.Args[0], alias)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("list(%s)", arg), nil
		}
	case "make_list":
		if len(n.Args) > 0 {
			arg, err := e.emitExpr(n.Args[0], alias)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("list(%s)", arg), nil
		}
	case "split":
		if len(n.Args) == 2 {
			s, err := e.emitExpr(n.Args[0], alias)
			if err != nil {
				return "", err
			}
			d, err := e.emitExpr(n.Args[1], alias)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("string_split(%s, %s)", s, d), nil
		}
	case "extract":
		if len(n.Args) == 2 {
			s, err := e.emitExpr(n.Args[0], alias)
			if err != nil {
				return "", err
			}
			r, err := e.emitExpr(n.Args[1], alias)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("regexp_extract(%s, %s, 0)", s, r), nil
		}
	case "parse_json":
		if len(n.Args) == 1 {
			s, err := e.emitExpr(n.Args[0], alias)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("%s::JSON", s), nil
		}
	// --- DuckDB-specific aggregate rewrites (CROSS-PROJECT-COMPARISON.md §2.3) ---
	case "count":
		if len(n.Args) == 0 {
			return "COUNT(*)", nil
		}
		name = "count" // DuckDB: count(col)
	case "any", "take_any", "anyif", "take_anyif":
		// DuckDB has `any_value(col)`.
		name = "any_value"
	case "arg_max":
		if len(n.Args) >= 2 {
			x, e1 := e.emitExpr(n.Args[0], alias)
			y, e2 := e.emitExpr(n.Args[1], alias)
			if e1 != nil {
				return "", e1
			}
			if e2 != nil {
				return "", e2
			}
			return fmt.Sprintf("list(%s ORDER BY %s DESC)[1]", x, y), nil
		}
	case "arg_min":
		if len(n.Args) >= 2 {
			x, e1 := e.emitExpr(n.Args[0], alias)
			y, e2 := e.emitExpr(n.Args[1], alias)
			if e1 != nil {
				return "", e1
			}
			if e2 != nil {
				return "", e2
			}
			return fmt.Sprintf("list(%s ORDER BY %s ASC)[1]", x, y), nil
		}
	case "dcountif":
		if len(n.Args) >= 2 {
			x, e1 := e.emitExpr(n.Args[0], alias)
			y, e2 := e.emitExpr(n.Args[1], alias)
			if e1 != nil {
				return "", e1
			}
			if e2 != nil {
				return "", e2
			}
			return fmt.Sprintf("count(DISTINCT CASE WHEN %s THEN %s END)", y, x), nil
		}
	case "sumif":
		if len(n.Args) >= 2 {
			x, e1 := e.emitExpr(n.Args[0], alias)
			y, e2 := e.emitExpr(n.Args[1], alias)
			if e1 != nil {
				return "", e1
			}
			if e2 != nil {
				return "", e2
			}
			return fmt.Sprintf("sum(CASE WHEN %s THEN %s ELSE 0 END)", y, x), nil
		}
	case "avgif":
		if len(n.Args) >= 2 {
			x, e1 := e.emitExpr(n.Args[0], alias)
			y, e2 := e.emitExpr(n.Args[1], alias)
			if e1 != nil {
				return "", e1
			}
			if e2 != nil {
				return "", e2
			}
			return fmt.Sprintf("avg(CASE WHEN %s THEN %s END)", y, x), nil
		}
	case "maxif":
		if len(n.Args) >= 2 {
			x, e1 := e.emitExpr(n.Args[0], alias)
			y, e2 := e.emitExpr(n.Args[1], alias)
			if e1 != nil {
				return "", e1
			}
			if e2 != nil {
				return "", e2
			}
			return fmt.Sprintf("max(CASE WHEN %s THEN %s END)", y, x), nil
		}
	case "minif":
		if len(n.Args) >= 2 {
			x, e1 := e.emitExpr(n.Args[0], alias)
			y, e2 := e.emitExpr(n.Args[1], alias)
			if e1 != nil {
				return "", e1
			}
			if e2 != nil {
				return "", e2
			}
			return fmt.Sprintf("min(CASE WHEN %s THEN %s END)", y, x), nil
		}
	case "make_list_if":
		name = "list" // best-effort: list(CASE WHEN pred THEN col END)
	case "make_set_if":
		name = "list"
	case "make_bag", "make_bag_if":
		name = "list" // best-effort JSON bag
	case "stdevp":
		name = "stddev_pop"
	case "variancep":
		name = "var_pop"
	case "stdev":
		name = "stddev_samp"
	case "variance":
		name = "var_samp"
	case "binary_all_and":
		name = "bit_and"
	case "binary_all_or":
		name = "bit_or"
	case "binary_all_xor":
		name = "bit_xor"
	// --- Round 4 DuckDB scalar overrides (names differ) ---
	case "max_of":
		name = "greatest"
	case "min_of":
		name = "least"
	case "notnull":
		name = "coalesce"
	case "binary_and":
		name = "bit_and"
	case "binary_or":
		name = "bit_or"
	case "binary_xor":
		name = "bit_xor"
	case "binary_not":
		name = "bit_not"
	case "binary_shift_left":
		name = "shift_left"
	case "binary_shift_right":
		name = "shift_right"
	case "cot":
		// DuckDB has no cot; 1/tan.
		if len(n.Args) > 0 {
			arg, err := e.emitExpr(n.Args[0], alias)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("(1.0 / tan(%s))", arg), nil
		}
	case "datetime", "todatetime":
		if len(n.Args) > 0 {
			arg, err := e.emitExpr(n.Args[0], alias)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("%s::TIMESTAMP", arg), nil
		}
	case "unixtime_seconds_todatetime":
		if len(n.Args) > 0 {
			arg, err := e.emitExpr(n.Args[0], alias)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("to_timestamp(%s)", arg), nil
		}
	case "gettype":
		name = "typeof"
	// --- IPv4/IPv6: DuckDB has native IP types via ipv4/ipv6 extension or built-ins ---
	case "ipv4_is_match", "ipv4_compare":
		if len(n.Args) >= 2 {
			x, e1 := e.emitExpr(n.Args[0], alias)
			y, e2 := e.emitExpr(n.Args[1], alias)
			if e1 != nil {
				return "", e1
			}
			if e2 != nil {
				return "", e2
			}
			return fmt.Sprintf("(%s = %s OR %s <<= %s)", x, y, x, y), nil
		}
	case "ipv4_is_in_range":
		if len(n.Args) >= 2 {
			x, e1 := e.emitExpr(n.Args[0], alias)
			y, e2 := e.emitExpr(n.Args[1], alias)
			if e1 != nil {
				return "", e1
			}
			if e2 != nil {
				return "", e2
			}
			return fmt.Sprintf("(%s <<= %s)", x, y), nil
		}
	case "has_ipv4":
		if len(n.Args) >= 2 {
			x, e1 := e.emitExpr(n.Args[0], alias)
			y, e2 := e.emitExpr(n.Args[1], alias)
			if e1 != nil {
				return "", e1
			}
			if e2 != nil {
				return "", e2
			}
			return fmt.Sprintf("(position(%s in %s) > 0)", y, x), nil
		}
	}
	// Generic: emit as name(args...).
	var args []string
	for _, a := range n.Args {
		s, err := e.emitExpr(a, alias)
		if err != nil {
			return "", err
		}
		args = append(args, s)
	}
	return fmt.Sprintf("%s(%s)", name, strings.Join(args, ", ")), nil
}

func (e *emitter) emitJoinOnExpr(expr ir.Expr, leftAlias, rightAlias string) (string, error) {
	switch n := expr.(type) {
	case *ir.BinOp:
		x := e.emitJoinCol(n.X, leftAlias, rightAlias)
		y := e.emitJoinCol(n.Y, leftAlias, rightAlias)
		return fmt.Sprintf("%s %s %s", x, n.Op, y), nil
	}
	return e.emitExpr(expr, leftAlias)
}

// emitJoinCol resolves a join ON column reference to the correct alias.
// Handles:
//   - *ir.Col: bare column name → defaults to left alias
//   - *ir.Member with X=$left:  → leftAlias.field
//   - *ir.Member with X=$right: → rightAlias.field
func (e *emitter) emitJoinCol(expr ir.Expr, leftAlias, rightAlias string) string {
	if m, ok := expr.(*ir.Member); ok {
		// $left.field or $right.field — X is a Col or lit representing $left/$right.
		if c, ok := m.X.(*ir.Col); ok {
			alias := leftAlias
			if c.Name == "$right" || c.Name == "right" {
				alias = rightAlias
			}
			return fmt.Sprintf("%s.%s", alias, quoteIdent(m.Field))
		}
	}
	if c, ok := expr.(*ir.Col); ok {
		// Default to left alias (KQL join ON defaults left).
		return fmt.Sprintf("%s.%s", leftAlias, quoteIdent(c.Name))
	}
	s, _ := e.emitExpr(expr, leftAlias)
	return s
}

// --- Helpers ---

// bind adds a parameter and returns its placeholder ($N).
func (e *emitter) bind(value interface{}) string {
	e.args = append(e.args, value)
	return fmt.Sprintf("$%d", len(e.args))
}

func (e *emitter) orderedArgs() []interface{} {
	return e.args
}

func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// --- CTE segment splitting (shared logic with pg) ---

type segment struct {
	stages []ir.Stage
}

func splitSegments(stages []ir.Stage) []segment {
	var segs []segment
	var current segment
	for _, st := range stages {
		if isBreakpoint(st) {
			if len(current.stages) > 0 {
				segs = append(segs, current)
				current = segment{}
			}
			segs = append(segs, segment{stages: []ir.Stage{st}})
			continue
		}
		current.stages = append(current.stages, st)
	}
	if len(current.stages) > 0 {
		segs = append(segs, current)
	}
	return segs
}

func isBreakpoint(st ir.Stage) bool {
	switch st.(type) {
	case *ir.Aggregate, *ir.Join, *ir.Distinct, *ir.Union,
		*ir.Extend, *ir.Project:
		return true
	}
	return false
}

// emitBreakpointDirect emits a single breakpoint stage as a CTE SELECT.
func (e *emitter) emitBreakpointDirect(st ir.Stage, from, alias string) (string, error) {
	return e.emitStage(st, from)
}

// emitMergedSelect emits a set of mergeable stages (Filter/Sort/Limit) as a
// single SELECT.
func (e *emitter) emitMergedSelect(stages []ir.Stage, from, alias string) (string, error) {
	var whereParts, orderParts []string
	var limitExpr string
	for _, st := range stages {
		switch s := st.(type) {
		case *ir.Filter:
			cond, err := e.emitExpr(s.Predicate, from)
			if err != nil {
				return "", err
			}
			whereParts = append(whereParts, cond)
		case *ir.Sort:
			for _, k := range s.Keys {
				expr, err := e.emitExpr(k.Expr, from)
				if err != nil {
					return "", err
				}
				dir := "ASC"
				if k.Desc {
					dir = "DESC"
				}
				orderParts = append(orderParts, fmt.Sprintf("%s %s", expr, dir))
			}
		case *ir.Limit:
			limitExpr, _ = e.emitExpr(s.Count, from)
		}
	}
	sql := fmt.Sprintf("SELECT * FROM %s", from)
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
