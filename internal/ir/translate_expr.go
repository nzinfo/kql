package ir

import (
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
		return &BinOp{Position: n.OpPos, Op: n.Op, X: x, Y: y}
	case *ast.UnaryExpr:
		return &UnaryOp{Position: n.OpPos, Op: n.Op, X: t.translateExpr(n.X)}
	case *ast.ParenExpr:
		// Parentheses are structural only in IR; unwrap.
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
		// An IN-list is handled by the caller (BinaryExpr with IN op); a bare
		// list expr is unusual. Represent as the first element or a diagnostic.
		if len(n.Elems) > 0 {
			return t.translateExpr(n.Elems[0])
		}
		return &Lit{Position: n.Pos(), HasValue: false}
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
// Go value for INT/REAL/STRING/BOOL; DATETIME/TIMESPAN/GUID keep the raw string.
func (t *translator) translateLit(n *ast.BasicLit) *Lit {
	lit := &Lit{Position: n.Pos(), HasValue: true, Value: n.Value}
	switch n.Kind {
	case token.INT:
		lit.T = TypeLong
	case token.REAL:
		lit.T = TypeReal
	case token.STRING:
		lit.T = TypeString
	case token.BOOL:
		lit.T = TypeBool
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
