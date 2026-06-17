package pg

import (
	"fmt"
	"strings"

	"nzinfo/kql/internal/frontend/builtin"
	"nzinfo/kql/internal/frontend/token"
	"nzinfo/kql/internal/ir"
)

// emitJoinOnExpr emits a join ON-condition, resolving $left/$right qualified
// references to the correct side's alias. Unqualified columns default to the
// left side (KQL join semantics).
func (e *emitter) emitJoinOnExpr(expr ir.Expr, leftAlias, rightAlias string) (string, error) {
	switch n := expr.(type) {
	case *ir.BinOp:
		x, err := e.emitJoinOnExpr(n.X, leftAlias, rightAlias)
		if err != nil {
			return "", err
		}
		y, err := e.emitJoinOnExpr(n.Y, leftAlias, rightAlias)
		if err != nil {
			return "", err
		}
		if op := sqlBinaryOp(n.Op); op != "" {
			return fmt.Sprintf("(%s %s %s)", x, op, y), nil
		}
		return fmt.Sprintf("(%s %s %s)", x, n.Op, y), nil
	case *ir.Member:
		if lc, ok := n.X.(*ir.Col); ok {
			switch lc.Name {
			case "$left":
				return fmt.Sprintf("%s.%s", leftAlias, quoteIdent(n.Field)), nil
			case "$right":
				return fmt.Sprintf("%s.%s", rightAlias, quoteIdent(n.Field)), nil
			}
		}
	case *ir.Col:
		return fmt.Sprintf("%s.%s", leftAlias, quoteIdent(n.Name)), nil
	case *ir.Lit:
		return e.emitLit(n)
	}
	return e.emitExpr(expr, leftAlias)
}

// emitExpr emits SQL for an IR expression (pg dialect).
func (e *emitter) emitExpr(expr ir.Expr, alias string) (string, error) {
	if expr == nil {
		return "", fmt.Errorf("nil expression")
	}
	switch n := expr.(type) {
	case *ir.Lit:
		return e.emitLit(n)
	case *ir.Col:
		return fmt.Sprintf("%s.%s", alias, quoteIdent(n.Name)), nil
	case *ir.Star:
		return alias + ".*", nil
	case *ir.BinOp:
		return e.emitBinOp(n, alias)
	case *ir.UnaryOp:
		inner, err := e.emitExpr(n.X, alias)
		if err != nil {
			return "", err
		}
		op := "+"
		if n.Op == token.SUB {
			op = "-"
		}
		return fmt.Sprintf("%s%s", op, inner), nil
	case *ir.FuncCall:
		return e.emitFuncCall(n, alias)
	case *ir.Member:
		inner, err := e.emitExpr(n.X, alias)
		if err != nil {
			return "", err
		}
		// dynamic field access → json/JSONB extraction. pg has ->>.
		return fmt.Sprintf("(%s ->> %s)", inner, "'"+n.Field+"'"), nil
	case *ir.Index:
		inner, err := e.emitExpr(n.X, alias)
		if err != nil {
			return "", err
		}
		idx, err := e.emitExpr(n.Index, alias)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("(%s -> %s)", inner, idx), nil
	case *ir.Case:
		cond, _ := e.emitExpr(n.Cond, alias)
		then, _ := e.emitExpr(n.Then, alias)
		els, _ := e.emitExpr(n.Else, alias)
		return fmt.Sprintf("CASE WHEN %s THEN %s ELSE %s END", cond, then, els), nil
	case *ir.List:
		parts := make([]string, 0, len(n.Elems))
		for _, el := range n.Elems {
			s, err := e.emitExpr(el, alias)
			if err != nil {
				return "", err
			}
			parts = append(parts, s)
		}
		return "(" + strings.Join(parts, ", ") + ")", nil
	}
	return "", fmt.Errorf("unsupported expression %T", expr)
}

func (e *emitter) emitLit(n *ir.Lit) (string, error) {
	if !n.HasValue {
		return "NULL", nil
	}
	// Integers and booleans are emitted INLINE (not as $N parameters) so pg can
	// infer their type from the literal itself. This fixes iff/CASE branches:
	// `CASE WHEN ... THEN $1 ELSE $2 END` with bound int64s makes pg default the
	// result to text (OID 25), then fail encoding int64→text. With inline
	// `1`/`0`, pg infers integer. No injection risk (these are numeric/bool,
	// not user strings); strings still go through $N.
	switch v := n.Value.(type) {
	case int64:
		return formatInt64Lit(v), nil
	case bool:
		if v {
			return "TRUE", nil
		}
		return "FALSE", nil
	case float64:
		// floats are safe inline too; use $N to preserve exact precision, but cast.
		s := e.bind(n.Value)
		return s + "::float8", nil
	}
	return e.bind(n.Value), nil
}

// formatInt64Lit renders an int64 as a SQL integer literal (no sign issues for
// the common positive case; min-int64 handled by the explicit minus).
func formatInt64Lit(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func (e *emitter) emitBinOp(n *ir.BinOp, alias string) (string, error) {
	if n.Op == token.IN || n.Op == token.NOTIN || n.Op == token.INCI ||
		n.Op == token.HASANY || n.Op == token.HASALL {
		return e.emitInList(n, alias)
	}
	x, err := e.emitExpr(n.X, alias)
	if err != nil {
		return "", err
	}
	y, err := e.emitExpr(n.Y, alias)
	if err != nil {
		return "", err
	}
	if opStr := sqlBinaryOp(n.Op); opStr != "" {
		return fmt.Sprintf("(%s %s %s)", x, opStr, y), nil
	}
	if s, ok := sqlStringOp(n.Op, x, y); ok {
		return s, nil
	}
	return "", fmt.Errorf("unsupported binary operator %s", n.Op)
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
	case token.INCI:
		// pg: case-insensitive IN would need citext; approximate plain IN.
		return fmt.Sprintf("(%s IN (%s))", x, joined), nil
	default:
		return fmt.Sprintf("(%s IN (%s))", x, joined), nil
	}
}

func sqlBinaryOp(op token.Token) string {
	switch op {
	case token.ADD:
		return "+"
	case token.SUB:
		return "-"
	case token.MUL:
		return "*"
	case token.QUO:
		return "/"
	case token.REM:
		return "%"
	case token.EQL:
		return "="
	case token.NEQ:
		return "<>"
	case token.LSS:
		return "<"
	case token.GTR:
		return ">"
	case token.LEQ:
		return "<="
	case token.GEQ:
		return ">="
	case token.AND:
		return "AND"
	case token.OR:
		return "OR"
	}
	return ""
}

// sqlStringOp maps KQL string operators to pg. pg has ILIKE (true case-
// insensitive LIKE) — a better match for KQL's case-insensitive semantics than
// sqlite's ASCII-only LIKE. Built via string concat (no Sprintf: '%' literals).
func sqlStringOp(op token.Token, x, y string) (string, bool) {
	switch op {
	case token.HAS, token.CONTAINS:
		return "(" + x + " ILIKE ('%' || " + y + " || '%'))", true
	case token.NOTHAS, token.NOTCONTAINS:
		return "(NOT " + x + " ILIKE ('%' || " + y + " || '%'))", true
	case token.STARTSWITH:
		return "(" + x + " ILIKE (" + y + " || '%'))", true
	case token.ENDSWITH:
		return "(" + x + " ILIKE ('%' || " + y + "))", true
	case token.TILDE:
		return "(" + x + " ILIKE " + y + ")", true
	case token.NTILDE:
		return "(NOT " + x + " ILIKE " + y + ")", true
	}
	return "", false
}

// emitFuncCall consults the builtin catalog. pg-specific overrides for a few
// functions that differ from the catalog's sqlite-default templates (e.g. ago,
// make_set → array_agg) land here.
func (e *emitter) emitFuncCall(n *ir.FuncCall, alias string) (string, error) {
	args := make([]string, 0, len(n.Args))
	for _, a := range n.Args {
		s, err := e.emitExpr(a, alias)
		if err != nil {
			return "", err
		}
		args = append(args, s)
	}
	// count() → COUNT(*)
	if strings.EqualFold(n.Name, "count") {
		if len(args) == 0 {
			return "COUNT(*)", nil
		}
		return fmt.Sprintf("COUNT(%s)", args[0]), nil
	}
	// pg-specific overrides
	switch strings.ToLower(n.Name) {
	case "ago":
		// ago(timespan) → now() - (interval). pg interval syntax differs; approximate.
		return fmt.Sprintf("(now() - (%s)::interval)", args[0]), nil
	case "make_set", "makeset":
		// pg has array_agg (returns array; closer to a set than sqlite's group_concat)
		return fmt.Sprintf("array_agg(DISTINCT %s)", args[0]), nil
	case "make_list", "makelist":
		return fmt.Sprintf("array_agg(%s)", args[0]), nil
	case "array_length":
		// pg array_length(arr, 1)
		return fmt.Sprintf("array_length(%s, 1)", args[0]), nil
	case "strcat":
		return "(" + strings.Join(args, " || ") + ")", nil
	case "coalesce":
		return fmt.Sprintf("coalesce(%s)", strings.Join(args, ", ")), nil
	case "bin":
		if len(args) == 2 {
			return fmt.Sprintf("(CAST((%s) / (%s) AS BIGINT) * (%s))", args[0], args[1], args[1]), nil
		}
	}
	// Catalog-driven translations (tostring→CAST, iff→CASE, dcount→COUNT(DISTINCT), ...)
	if spec := builtin.Lookup(n.Name); spec != nil {
		if spec.SQLite == builtin.StrcatTpl {
			return "(" + strings.Join(args, " || ") + ")", nil
		}
		if spec.SQLite == "coalesce(%s)" {
			return fmt.Sprintf("coalesce(%s)", strings.Join(args, ", ")), nil
		}
		if spec.SQLite != "" {
			return applyTemplate(spec.SQLite, args), nil
		}
	}
	// generic pass-through
	return fmt.Sprintf("%s(%s)", strings.ToUpper(n.Name), strings.Join(args, ", ")), nil
}

// applyTemplate substitutes emitted args into a catalog template (%s each).
func applyTemplate(tpl string, args []string) string {
	n := strings.Count(tpl, "%s")
	fill := args
	if len(fill) > n {
		fill = fill[:n]
	}
	out := fmt.Sprintf(tpl, toAny(fill)...)
	if len(args) > n {
		extras := strings.Join(args[n:], ", ")
		if idx := strings.LastIndex(out, ")"); idx >= 0 {
			out = out[:idx] + ", " + extras + out[idx:]
		}
	}
	return out
}

func toAny(s []string) []interface{} {
	out := make([]interface{}, len(s))
	for i, v := range s {
		out[i] = v
	}
	return out
}

// quoteIdent quotes a pg identifier with double quotes. Same style as sqlite;
// both use standard SQL identifiers.
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
