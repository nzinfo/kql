package ir

import (
	"strconv"
	"strings"

	"nzinfo/kql/internal/frontend/ast"
	"nzinfo/kql/internal/frontend/token"
)

// translateExpr converts an AST expression node to an IR Expr. Column references
// are produced with ColID=Invalid and Name set (string placeholder) until the
// F5 binder is wired in (PROGRESS.md §2). Types are TypeUnknown until the
// binder infers them; the translator sets obvious literal types only.
func (t *translator) translateExpr(e ast.Expr) Expr {
	if e == nil {
		return nil
	}
	switch n := e.(type) {
	case *ast.BasicLit:
		return t.translateLit(n)
	case *ast.Ident:
		// Column reference (placeholder ColID until F5). Keywords-as-names are
		// also Ident, so a bare `count` reference becomes a Col too.
		return &Col{Position: n.Pos(), Name: n.Name}
	case *ast.StarExpr:
		return &Star{Position: n.Pos()}
	case *ast.BinaryExpr:
		x := t.translateExpr(n.X)
		y := t.translateExpr(n.Y)
		op := n.Op
		// The `:` string operator (g4 stringBinaryOperation) is semantically
		// identical to =~ (case-insensitive equality). Normalize at translation
		// so all existing =~ emit paths handle it.
		if op == token.COLON {
			op = token.TILDE
		}
		return &BinOp{Position: n.OpPos, Op: op, X: x, Y: y}
	case *ast.UnaryExpr:
		return &UnaryOp{Position: n.OpPos, Op: n.Op, X: t.translateExpr(n.X)}
	case *ast.ParenExpr:
		// Parentheses are structural only in IR; unwrap the scalar expression.
		// (A parenthesised sub-pipeline appears only as a join's right side,
		// handled by translateJoin's ParenExpr unwrap — never as a scalar expr.)
		return t.translateExpr(n.X)
	case *ast.CallExpr:
		return t.translateCall(n)
	case *ast.SelectorExpr:
		// X.field — member access. For Table.Col dotted references the binder
		// (F5) will resolve; here we keep it as a Member.
		return &Member{Position: n.Pos(), X: t.translateExpr(n.X), Field: n.Sel.Name}
	case *ast.IndexExpr:
		return &Index{Position: n.Pos(), X: t.translateExpr(n.X), Index: t.translateExpr(n.Index)}
	case *ast.ListExpr:
		// An IN-list: emit an ir.List so the backend can produce IN (…).
		elems := make([]Expr, len(n.Elems))
		for i, el := range n.Elems {
			elems[i] = t.translateExpr(el)
		}
		return &List{Position: n.Pos(), Elems: elems}
	case *ast.BetweenExpr:
		// Represented at the BinaryExpr level normally; if reached directly,
		// build a Case-like construct. For MVP, translate as a comparison pair
		// wrapped — simplest is to return the X and let caller handle. We
		// rebuild using BinOp AND of two comparisons.
		x := t.translateExpr(n.X)
		low := t.translateExpr(n.Low)
		high := t.translateExpr(n.High)
		geq := &BinOp{Position: n.OpPos, Op: token.GEQ, X: x, Y: low}
		leq := &BinOp{Position: n.OpPos, Op: token.LEQ, X: t.translateExpr(n.X), Y: high}
		return &BinOp{Position: n.OpPos, Op: token.AND, X: geq, Y: leq}
	case *ast.ConditionalExpr:
		return &Case{
			Position: n.Pos(),
			Cond:     t.translateExpr(n.Cond),
			Then:     t.translateExpr(n.Then),
			Else:     t.translateExpr(n.Else),
		}
	case *ast.CastExpr:
		// X to <type> — represent as a FuncCall named "to_<type>" so backends
		// can emit a cast. Type set to the target.
		return &FuncCall{
			Position: n.Pos(),
			Name:     "to_" + n.Type.String(),
			Args:     []Expr{t.translateExpr(n.X)},
			Caps:     DefaultCaps("to_"+n.Type.String(), false),
			T:        tokenToType(n.Type),
		}
	case *ast.DynamicLit:
		// dynamic(json) — represent the inner value; full JSON handling is F4+.
		return t.translateExpr(n.Value)
	case *ast.BadExpr:
		return &Lit{Position: n.Pos(), HasValue: false}
	}
	t.errorf(e.Pos(), "KQL010: unsupported expression %T", e)
	return &Lit{Position: e.Pos(), HasValue: false}
}

// translateLit maps an AST BasicLit to an IR Lit, setting the obvious type and
// Go value for INT/REAL/STRING/BOOL. String literals are UNQUOTED here so the
// bound value is the raw text "TEXAS" (not the source form `"TEXAS"` with
// embedded quotes). DATETIME/TIMESPAN/GUID keep the raw source string for the
// backend to parse.
func (t *translator) translateLit(n *ast.BasicLit) *Lit {
	lit := &Lit{Position: n.Pos(), HasValue: true, Value: n.Value}
	switch n.Kind {
	case token.INT:
		lit.T = TypeLong
		// Parse to int64 so the driver binds a proper integer. Hex (0x..) and
		// plain decimals both handled; failure leaves the raw string (binder
		// will catch malformed literals later).
		if v, err := strconv.ParseInt(strings.TrimPrefix(n.Value, "0x"), 0, 64); err == nil {
			lit.Value = v
		} else {
			// fallback: keep raw; non-fatal until binder validates.
			lit.Value = n.Value
		}
	case token.REAL:
		lit.T = TypeReal
		if v, err := strconv.ParseFloat(n.Value, 64); err == nil {
			lit.Value = v
		} else {
			lit.Value = n.Value
		}
	case token.STRING:
		lit.T = TypeString
		lit.Value = unquoteString(n.Value)
	case token.BOOL:
		lit.T = TypeBool
		// "true"/"false" of any case → bool.
		lit.Value = strings.EqualFold(n.Value, "true")
	case token.DATETIME:
		lit.T = TypeDateTime
	case token.TIMESPAN:
		lit.T = TypeTimeSpan
	case token.GUID:
		lit.T = TypeString
	default:
		lit.T = TypeUnknown
	}
	return lit
}

// unquoteString strips the surrounding quotes from a KQL string literal and
// resolves basic escape sequences. Handles "..." / '...' / @"..." / @'...' / h"...".
// For verbatim strings ("@") the only escape is doubled quotes; for normal
// strings, standard backslash escapes apply. The full unescape follows the
// lexer's scanString logic — here we handle the common cases correctly.
func unquoteString(raw string) string {
	s := raw
	// strip optional h/H hash-prefix
	if len(s) >= 1 && (s[0] == 'h' || s[0] == 'H') {
		s = s[1:]
	}
	// strip optional @ verbatim marker
	verbatim := false
	if len(s) >= 1 && s[0] == '@' {
		verbatim = true
		s = s[1:]
	}
	if len(s) < 2 {
		return raw
	}
	quote := s[0]
	body := s[1 : len(s)-1] // drop both quotes
	if verbatim {
		// only doubled-quote escape
		return strings.ReplaceAll(body, string(quote)+string(quote), string(quote))
	}
	// normal: resolve common backslash escapes
	out := make([]byte, 0, len(body))
	for i := 0; i < len(body); i++ {
		if body[i] == '\\' && i+1 < len(body) {
			i++
			switch body[i] {
			case 'n':
				out = append(out, '\n')
			case 't':
				out = append(out, '\t')
			case 'r':
				out = append(out, '\r')
			case '\\':
				out = append(out, '\\')
			case '"':
				out = append(out, '"')
			case '\'':
				out = append(out, '\'')
			default:
				out = append(out, body[i])
			}
		} else {
			out = append(out, body[i])
		}
	}
	return string(out)
}

// translateCall converts an AST CallExpr to an IR FuncCall, detecting aggregate
// functions by name (count/sum/avg/min/max/...) so Caps.Aggregate is set.
func (t *translator) translateCall(n *ast.CallExpr) *FuncCall {
	name := callName(n.Fun)
	isAgg := isAggregateName(name)
	args := make([]Expr, len(n.Args))
	for i, a := range n.Args {
		// NamedExpr-wrapped args: unwrap to the bound expression.
		args[i] = t.translateExpr(unwrapNamed(a))
	}
	return &FuncCall{
		Position: n.Pos(),
		Name:     name,
		Args:     args,
		Caps:     DefaultCaps(name, isAgg),
	}
}

// callName extracts the function name from a call's Fun expression (typically
// an *ast.Ident, but a SelectorExpr like cluster.db.fn flattens to a dotted name).
func callName(fun ast.Expr) string {
	switch f := fun.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		return callName(f.X) + "." + f.Sel.Name
	}
	return "<expr>"
}

// isAggregateName reports whether name is a known KQL aggregate function.
// This is a minimal set for MVP; the full table comes from F7 (builtin).
func isAggregateName(name string) bool {
	switch name {
	case "count", "sum", "avg", "min", "max", "stdev", "variance",
		"countif", "sumif", "avgif", "makeset", "makelist", "dcount",
		"percentile", "percentiles", "percentilew", "percentilesw":
		return true
	}
	return false
}

// unwrapNamed returns the bound expression of a NamedExpr, or e itself.
func unwrapNamed(e ast.Expr) ast.Expr {
	if n, ok := e.(*ast.NamedExpr); ok {
		return n.Expr
	}
	return e
}

// tokenToType maps a scalar-type keyword token to an IR Type.
func tokenToType(tk token.Token) Type {
	switch tk {
	case token.BOOLTYPE:
		return TypeBool
	case token.INTTYPE:
		return TypeInt
	case token.LONGTYPE:
		return TypeLong
	case token.REALTYPE:
		return TypeReal
	case token.DECIMALTYPE:
		return TypeDecimal
	case token.STRINGTYPE:
		return TypeString
	case token.DATETIMETYPE:
		return TypeDateTime
	case token.TIMESPANTYPE:
		return TypeTimeSpan
	case token.DYNAMICTYPE:
		return TypeDynamic
	}
	return TypeUnknown
}
