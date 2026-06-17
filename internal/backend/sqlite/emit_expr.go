package sqlite

import (
	"fmt"
	"strings"

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
	// IN-list: right side is a ListExpr → emit as X IN (?, ?, ...).
	if n.Op == token.IN || n.Op == token.NOTIN {
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
	if n.Op == token.NOTIN {
		return fmt.Sprintf("(%s NOT IN (%s))", x, joined), nil
	}
	return fmt.Sprintf("(%s IN (%s))", x, joined), nil
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

// emitFuncCall emits a function call. Aggregate functions map to SQL aggregates;
// common scalar fns map 1:1; unknown fns fall through to a best-effort pass.
func (e *emitter) emitFuncCall(n *ir.FuncCall, alias string) (string, error) {
	args := make([]string, 0, len(n.Args))
	for _, a := range n.Args {
		s, err := e.emitExpr(a, alias)
		if err != nil {
			return "", err
		}
		args = append(args, s)
	}
	// count() → COUNT(*); count(*) maps naturally too.
	if strings.EqualFold(n.Name, "count") {
		if len(args) == 0 {
			return "COUNT(*)", nil
		}
		return fmt.Sprintf("COUNT(%s)", args[0]), nil
	}
	// bin(col, span): SQLite has no native bin; emulate with strftime for
	// datetime columns. Minimal loop: emit a best-effort bucket via
	// (col / span) * span for numeric, leaving datetime binning as a known
	// approximation (T-series will refine with real data).
	if strings.EqualFold(n.Name, "bin") && len(args) == 2 {
		return fmt.Sprintf("(CAST((%s) / (%s) AS INTEGER) * (%s))", args[0], args[1], args[1]), nil
	}
	// iff(cond, a, b) → CASE.
	if strings.EqualFold(n.Name, "iff") && len(args) == 3 {
		return fmt.Sprintf("CASE WHEN %s THEN %s ELSE %s END", args[0], args[1], args[2]), nil
	}
	// cast-style to_<type>
	if strings.HasPrefix(n.Name, "to_") && len(args) == 1 {
		t := strings.TrimPrefix(n.Name, "to_")
		return castToSQL(t, args[0])
	}
	// Generic pass-through: NAME(arg1, arg2, ...). Most SQL fns (abs, length,
	// substr, coalesce, now, sum, avg, min, max, …) match by name.
	return fmt.Sprintf("%s(%s)", strings.ToUpper(n.Name), strings.Join(args, ", ")), nil
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
