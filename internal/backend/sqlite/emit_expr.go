package sqlite

import (
	"fmt"
	"strings"

	"nzinfo/kql/internal/frontend/builtin"
	"nzinfo/kql/internal/frontend/token"
	"nzinfo/kql/internal/ir"
)

// emitExpr emits SQL for an IR expression. alias is the table alias to prefix
// column references with (so `colA` → `"_k0"."colA"`), avoiding ambiguity in
// joined/aggregated queries.
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
		// X.field → json_extract(X, '$.field') for dynamic; for simple struct
		// refs fall back to X."field". The minimal loop treats member as a
		// dotted column reference (alias.col).
		inner, err := e.emitExpr(n.X, alias)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s.%s", inner, quoteIdent(n.Field)), nil
	case *ir.Index:
		// X[idx] → dynamic indexing. Minimal loop: emit as a comment-free
		// json_extract approximation. Acceptable for e2e validation.
		inner, err := e.emitExpr(n.X, alias)
		if err != nil {
			return "", err
		}
		idx, err := e.emitExpr(n.Index, alias)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("json_extract(%s, '$.' || %s)", inner, idx), nil
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
		// A bare list (not the RHS of an IN op) — emit as a parenthesised,
		// comma-separated value list. Used when a list appears in a context the
		// minimal emitter doesn't specialise (rare; usually an aggregate arg).
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

// emitLit emits a literal as a NUMBERED SQL bind parameter. Numbered
// placeholders (?N) make arg ordering robust to nested subqueries — see
// emit.go's Emit doc comment.
func (e *emitter) emitLit(n *ir.Lit) (string, error) {
	if !n.HasValue {
		return "NULL", nil
	}
	return e.bind(n.Value), nil
}

// emitBinOp emits a binary operation. String operators map to LIKE/instr
// approximations (case rules differ from KQL — acceptable for e2e; F7 refines).
func (e *emitter) emitBinOp(n *ir.BinOp, alias string) (string, error) {
	// IN-style list operators: X in/!in/in~/!in~/has_any/has_all (a,b,...). The
	// right operand is an *ir.List; emit as X IN (...) (the ~ variants are
	// case-insensitive — approximated as plain IN for the minimal loop; exact
	// case-insensitive IN needs COLLATE NOCASE per element, flagged in NOTES).
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
	// Map KQL operators to SQL. Arithmetic and comparison map 1:1.
	opStr := sqlBinaryOp(n.Op)
	if opStr != "" {
		return fmt.Sprintf("(%s %s %s)", x, opStr, y), nil
	}
	// String operators — LIKE / function approximations.
	if s, ok := sqlStringOp(n.Op, x, y); ok {
		return s, nil
	}
	return "", fmt.Errorf("unsupported binary operator %s", n.Op)
}

// emitInList handles X in (a, b, c) and X !in (...).
func (e *emitter) emitInList(n *ir.BinOp, alias string) (string, error) {
	x, err := e.emitExpr(n.X, alias)
	if err != nil {
		return "", err
	}
	list, ok := n.Y.(*ir.List)
	if !ok {
		// Right side might be a single expr wrapped; emit as IN (single).
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
		// case-insensitive IN: approximate as plain IN. COLLATE NOCASE per elem
		// is the correct form (see NOTES); deferred until it matters.
		return fmt.Sprintf("(%s IN (%s))", x, joined), nil
	default: // IN, HASANY, HASALL
		return fmt.Sprintf("(%s IN (%s))", x, joined), nil
	}
}

// sqlBinaryOp returns the SQL operator for arithmetic/comparison/logical KQL
// operators, or "" if it's a string/list operator (handled separately).
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

// sqlStringOp maps KQL string operators to SQL approximations.
// Case sensitivity: KQL has/contains/startswith are case-INsensitive; SQLite
// LIKE is case-insensitive for ASCII by default — a reasonable match.
//
// NOTE: these build SQL via string concatenation (not fmt.Sprintf) because the
// LIKE patterns contain literal '%' which Sprintf would misread as a verb.
func sqlStringOp(op token.Token, x, y string) (string, bool) {
	switch op {
	case token.HAS, token.CONTAINS:
		// case-insensitive substring (SQLite default LIKE)
		return "(" + x + " LIKE ('%' || " + y + " || '%'))", true
	case token.NOTHAS, token.NOTCONTAINS:
		return "(" + x + " NOT LIKE ('%' || " + y + " || '%'))", true
	case token.STARTSWITH:
		return "(" + x + " LIKE (" + y + " || '%'))", true
	case token.ENDSWITH:
		return "(" + x + " LIKE ('%' || " + y + "))", true
	case token.TILDE: // =~ case-insensitive equality
		return "(" + x + " = " + y + " COLLATE NOCASE)", true
	case token.NTILDE:
		return "(" + x + " <> " + y + " COLLATE NOCASE)", true
	}
	return "", false
}

// emitJoinOnExpr emits a join ON-condition expression, resolving $left/$right
// qualified column references to the correct side's alias. `$left.col` →
// leftAlias."col"; `$right.col` → rightAlias."col"; an unqualified `col` is
// ambiguous and defaults to the LEFT side (KQL's join semantics: an unqualified
// name in the ON clause refers to the left input unless it's right-only).
func (e *emitter) emitJoinOnExpr(expr ir.Expr, leftAlias, rightAlias string) (string, error) {
	switch n := expr.(type) {
	case *ir.BinOp:
		// recurse so each side resolves its own qualification
		x, err := e.emitJoinOnExpr(n.X, leftAlias, rightAlias)
		if err != nil {
			return "", err
		}
		y, err := e.emitJoinOnExpr(n.Y, leftAlias, rightAlias)
		if err != nil {
			return "", err
		}
		n.X, n.Y = nil, nil // avoid reuse; rebuild below
		if op := sqlBinaryOp(n.Op); op != "" {
			return fmt.Sprintf("(%s %s %s)", x, op, y), nil
		}
		return fmt.Sprintf("(%s %s %s)", x, n.Op, y), nil
	case *ir.Member:
		// $left.col / $right.col
		if lc, ok := n.X.(*ir.Col); ok {
			switch lc.Name {
			case "$left":
				return fmt.Sprintf("%s.%s", leftAlias, quoteIdent(n.Field)), nil
			case "$right":
				return fmt.Sprintf("%s.%s", rightAlias, quoteIdent(n.Field)), nil
			}
		}
	case *ir.Col:
		// unqualified column in ON → default to left side
		return fmt.Sprintf("%s.%s", leftAlias, quoteIdent(n.Name)), nil
	case *ir.Lit:
		return e.emitLit(n)
	}
	// fallback: use the generic emitter with the left alias
	return e.emitExpr(expr, leftAlias)
}
// (internal/frontend/builtin) for a per-function SQLite translation; the catalog
// covers the common KQL scalar/aggregate functions so emit produces VALID SQLite
// (ago → datetime('now', -...), tostring → CAST AS TEXT, iff → CASE, etc.)
// instead of a blind UPPER(name) pass-through. Functions not in the catalog or
// marked NeedsPostProc fall back to a best-effort pass-through.
func (e *emitter) emitFuncCall(n *ir.FuncCall, alias string) (string, error) {
	args := make([]string, 0, len(n.Args))
	for _, a := range n.Args {
		s, err := e.emitExpr(a, alias)
		if err != nil {
			return "", err
		}
		args = append(args, s)
	}
	// count() is special (no args → COUNT(*)).
	if strings.EqualFold(n.Name, "count") {
		if len(args) == 0 {
			return "COUNT(*)", nil
		}
		return fmt.Sprintf("COUNT(%s)", args[0]), nil
	}
	// bin(col, span): no native bin in sqlite; numeric bucket approximation
	// (datetime binning deferred — see NOTES).
	if strings.EqualFold(n.Name, "bin") && len(args) == 2 {
		return fmt.Sprintf("(CAST((%s) / (%s) AS INTEGER) * (%s))", args[0], args[1], args[1]), nil
	}
	// Consult the builtin catalog for a SQLite translation.
	if spec := builtin.Lookup(n.Name); spec != nil {
		if spec.SQLite == builtin.StrcatTpl {
			// variadic strcat → "a || b || c ..."
			return "(" + strings.Join(args, " || ") + ")", nil
		}
		if spec.SQLite == "coalesce(%s)" {
			// variadic coalesce
			return fmt.Sprintf("coalesce(%s)", strings.Join(args, ", ")), nil
		}
		if spec.SQLite != "" {
			return applySQLiteTemplate(spec.SQLite, args), nil
		}
		// No SQL translation (NeedsPostProc or simply unmapped): best-effort
		// pass-through so the query at least parses/executes, and record the
		// capability gap for the post-proc layer.
		if spec.NeedsPostProc {
			e.notePostProc(n.Name)
		}
	}
	// cast-style to_<type> (not in the catalog as a single template since KQL
	// spellings vary; kept here for completeness).
	if strings.HasPrefix(n.Name, "to_") && len(args) == 1 {
		t := strings.TrimPrefix(n.Name, "to_")
		return castToSQL(t, args[0])
	}
	// Generic pass-through: NAME(arg1, arg2, ...). For functions the catalog
	// doesn't know, this at least keeps the query runnable on functions that
	// happen to share SQLite's name (abs, length, substr, …).
	return fmt.Sprintf("%s(%s)", strings.ToUpper(n.Name), strings.Join(args, ", ")), nil
}

// applySQLiteTemplate substitutes the emitted arg SQL into a catalog template.
// The template uses %s per argument, in order. Args beyond the template's %s
// count are appended as extra comma-separated arguments (a pragmatic fix for
// functions whose SQLite form takes the same args plus modifiers).
func applySQLiteTemplate(tpl string, args []string) string {
	// Count %s placeholders.
	n := strings.Count(tpl, "%s")
	// fmt with the first n args; append the rest verbatim.
	fill := args
	if len(fill) > n {
		fill = fill[:n]
	}
	out := fmt.Sprintf(tpl, toAny(fill)...)
	if len(args) > n {
		// inject extras: crude — append before the closing paren if present.
		extras := strings.Join(args[n:], ", ")
		if idx := strings.LastIndex(out, ")"); idx >= 0 {
			out = out[:idx] + ", " + extras + out[idx:]
		} else {
			out += ", " + extras
		}
	}
	return out
}

// toAny converts []string to []interface{} for fmt.
func toAny(s []string) []interface{} {
	out := make([]interface{}, len(s))
	for i, v := range s {
		out[i] = v
	}
	return out
}

// notePostProc records (for the minimal loop) that a function used in the query
// needs client-side post-processing. The current emitter doesn't act on this —
// it just lets the call pass through — but the hook is here so the post-proc
// framework (backend/NOTES.md §2.4 / B5) can collect these when it lands.
func (e *emitter) notePostProc(name string) {
	if e.postProc == nil {
		e.postProc = map[string]bool{}
	}
	e.postProc[name] = true
}

// castToSQL maps a to_<type> cast to SQLite's loose typing.
func castToSQL(t, arg string) (string, error) {
	switch t {
	case "string":
		return fmt.Sprintf("CAST(%s AS TEXT)", arg), nil
	case "int", "long":
		return fmt.Sprintf("CAST(%s AS INTEGER)", arg), nil
	case "real":
		return fmt.Sprintf("CAST(%s AS REAL)", arg), nil
	case "bool":
		return fmt.Sprintf("CAST(%s AS INTEGER)", arg), nil
	}
	return fmt.Sprintf("CAST(%s AS TEXT)", arg), nil
}
