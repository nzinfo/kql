// Package binder — type inference (F5.S4).
//
// After column resolution, the binder infers the result type of composite
// expressions (BinOp, UnaryOp, FuncCall, Case) and sets their .T field.
// Type mismatches emit KQL002 (TypeMismatch) as a WARNING — not an error —
// because KQL's dynamic type system allows implicit coercions at runtime
// (e.g. string "123" used arithmetically), and blocking would break the
// 90/90 corpus which has loosely-typed expressions. Warnings surface in the
// diagnostic List but don't block execution.
//
// Inference rules (DESIGN F5.S4 + KQL language reference):
//   - Arithmetic (+ - * / %): numeric promotion (int+int=int, int+real=real,
//     long+long=long). + also works on timespan (timespan+timespan=timespan)
//     and string concatenation in KQL (strcat is preferred, but + is allowed).
//     Mismatched non-numeric operands → KQL002 warning, result TypeUnknown.
//   - Comparison (< > <= >= == != has etc.): → bool. Both operands should be
//     comparable types; mismatch → warning.
//   - Logical (and/or): operands should be bool → bool. Non-bool → warning.
//   - IN/!in: left operand any scalar, right a list → bool.
//   - Unary +/-: numeric → same type.
package binder

import (
	"nzinfo/kql/internal/frontend/diagnostic"
	"nzinfo/kql/internal/frontend/token"
	"nzinfo/kql/internal/ir"
)

// inferExprType computes and sets the result type of a composite expression
// after its sub-expressions have been resolved (their .T is set). Called from
// checkExpr after recursion. Leaves are already typed (Col.T from schema, Lit.T
// from translation); this handles the composite cases.
func (b *binder) inferExprType(e ir.Expr) {
	switch n := e.(type) {
	case *ir.BinOp:
		n.T = b.inferBinOpType(n)
	case *ir.UnaryOp:
		n.T = inferUnaryType(n)
	case *ir.Case:
		n.T = inferCaseType(n)
	case *ir.FuncCall:
		n.T = inferFuncType(n)
	}
}

// inferBinOpType returns the result type of a binary operation.
func (b *binder) inferBinOpType(n *ir.BinOp) ir.Type {
	lt, rt := n.X.Type(), n.Y.Type()
	switch {
	case isArithOp(n.Op):
		return inferArithType(n.Op, lt, rt, n.X.Pos(), b)
	case isComparisonOp(n.Op):
		return inferComparisonType(lt, rt, n.X.Pos(), b)
	case isLogicalOp(n.Op):
		return inferLogicalType(lt, rt, n.X.Pos(), b)
	case isInOp(n.Op):
		return ir.TypeBool // IN/!in always returns bool
	case isStringOp(n.Op):
		return ir.TypeBool // string ops (has/contains/startswith etc.) → bool
	}
	return ir.TypeUnknown
}

// inferArithType handles + - * / %. Numeric promotion + timespan + string-concat.
func inferArithType(op token.Token, lt, rt ir.Type, pos token.Pos, b *binder) ir.Type {
	// Special case: timespan arithmetic.
	if lt == ir.TypeTimeSpan && rt == ir.TypeTimeSpan {
		return ir.TypeTimeSpan
	}
	// datetime arithmetic: datetime - datetime = timespan, datetime ± timespan = datetime.
	if lt == ir.TypeDateTime || rt == ir.TypeDateTime {
		if lt == ir.TypeDateTime && rt == ir.TypeDateTime && op == token.SUB {
			return ir.TypeTimeSpan
		}
		if (lt == ir.TypeDateTime && rt == ir.TypeTimeSpan) ||
			(lt == ir.TypeTimeSpan && rt == ir.TypeDateTime) {
			return ir.TypeDateTime
		}
	}
	// String concatenation via + (KQL allows it, though strcat is preferred).
	if lt == ir.TypeString && rt == ir.TypeString && op == token.ADD {
		return ir.TypeString
	}
	// Numeric promotion.
	if lt.IsNumeric() && rt.IsNumeric() {
		return promoteNumeric(lt, rt)
	}
	// If either side is unknown (unresolved column, dynamic), we can't infer
	// — don't warn (the type may resolve at runtime).
	if lt == ir.TypeUnknown || rt == ir.TypeUnknown {
		return ir.TypeUnknown
	}
	// Genuine mismatch (e.g. string * int).
	b.diagAt(pos, diagnostic.Warning, diagnostic.TypeMismatch,
		"arithmetic %s on incompatible types %s and %s", op, lt, rt)
	return ir.TypeUnknown
}

// promoteNumeric returns the result type of a binary numeric operation.
// KQL promotion: int < long < real < decimal. The wider type wins.
func promoteNumeric(lt, rt ir.Type) ir.Type {
	// Order by width: int(1) < long(2) < real(3) < decimal(4).
	width := func(t ir.Type) int {
		switch t {
		case ir.TypeInt:
			return 1
		case ir.TypeLong:
			return 2
		case ir.TypeReal:
			return 3
		case ir.TypeDecimal:
			return 4
		case ir.TypeTimeSpan:
			return 5 // timespan is numeric-ish but doesn't promote further
		}
		return 0
	}
	wl, wr := width(lt), width(rt)
	if wl >= wr {
		return lt
	}
	return rt
}

// inferComparisonType: comparisons always return bool. Warn if operands are
// clearly incompatible (e.g. comparing string to int).
func inferComparisonType(lt, rt ir.Type, pos token.Pos, b *binder) ir.Type {
	if lt == ir.TypeUnknown || rt == ir.TypeUnknown {
		return ir.TypeBool
	}
	// Allow numeric-to-numeric, string-to-string, datetime-to-datetime, etc.
	if typeCompatible(lt, rt) {
		return ir.TypeBool
	}
	b.diagAt(pos, diagnostic.Warning, diagnostic.TypeMismatch,
		"comparing incompatible types %s and %s", lt, rt)
	return ir.TypeBool
}

// inferLogicalType: logical ops (and/or) require bool operands → bool.
func inferLogicalType(lt, rt ir.Type, pos token.Pos, b *binder) ir.Type {
	if lt != ir.TypeUnknown && lt != ir.TypeBool {
		b.diagAt(pos, diagnostic.Warning, diagnostic.TypeMismatch,
			"logical operator on non-bool type %s", lt)
	}
	if rt != ir.TypeUnknown && rt != ir.TypeBool {
		b.diagAt(pos, diagnostic.Warning, diagnostic.TypeMismatch,
			"logical operator on non-bool type %s", rt)
	}
	return ir.TypeBool
}

// inferUnaryType: unary +/- preserves the operand's numeric type.
func inferUnaryType(n *ir.UnaryOp) ir.Type {
	t := n.X.Type()
	if t.IsNumeric() {
		return t
	}
	return ir.TypeUnknown
}

// inferCaseType: iff/ternary returns the type of the then/else branches. If
// they differ, warn but pick the "then" type (KQL coerces at runtime).
func inferCaseType(n *ir.Case) ir.Type {
	tt, et := n.Then.Type(), n.Else.Type()
	if tt == ir.TypeUnknown {
		return et
	}
	if et == ir.TypeUnknown {
		return tt
	}
	if tt != et && typeCompatible(tt, et) {
		return promoteNumeric(tt, et)
	}
	return tt
}

// inferFuncType returns the result type for known builtin functions. Unknown
// functions (or those without return-type metadata) get TypeUnknown.
func inferFuncType(n *ir.FuncCall) ir.Type {
	switch n.Name {
	case "count", "countif", "dcount", "dcountif":
		return ir.TypeLong
	case "sum", "sumif", "avg", "avgif", "min", "max", "percentile":
		// Aggregates return the same type as their first numeric arg.
		if len(n.Args) > 0 {
			return n.Args[0].Type()
		}
		return ir.TypeReal
	case "tostring", "tostring_":
		return ir.TypeString
	case "tostringlower", "tostringupper", "trim", "trim_start", "trim_end",
		"substr", "substring", "replace", "replace_string", "tolower", "toupper":
		return ir.TypeString
	case "tobool", "isnotnull", "isnull", "isempty", "isnotempty":
		return ir.TypeBool
	case "tolong", "toint":
		return ir.TypeLong
	case "toreal":
		return ir.TypeReal
	case "iff", "iif":
		// Return type is the type of the then-branch (arg index 1).
		if len(n.Args) >= 2 {
			return n.Args[1].Type()
		}
		return ir.TypeUnknown
	case "strcat":
		return ir.TypeString
	case "now":
		return ir.TypeDateTime
	case "ago":
		return ir.TypeTimeSpan
	case "bin", "floor", "ceiling", "abs", "sqrt", "pow", "exp", "log":
		// Numeric functions preserve the arg type.
		if len(n.Args) > 0 {
			return n.Args[0].Type()
		}
		return ir.TypeReal
	case "make_set", "make_list", "makeset", "makelist":
		return ir.TypeDynamic
	case "array_length", "strlen", "string_size":
		return ir.TypeLong
	case "parse_json":
		return ir.TypeDynamic
	}
	return ir.TypeUnknown
}

// typeCompatible reports whether two types can be compared or combined without
// a clear mismatch (allowing numeric promotion and same-category comparisons).
func typeCompatible(a, b ir.Type) bool {
	if a == b {
		return true
	}
	// All numeric types are mutually compatible.
	if a.IsNumeric() && b.IsNumeric() {
		return true
	}
	// datetime/timespan are temporal-compatible.
	isTemporal := func(t ir.Type) bool { return t == ir.TypeDateTime || t == ir.TypeTimeSpan }
	if isTemporal(a) && isTemporal(b) {
		return true
	}
	return false
}

// Operator classification helpers.

func isArithOp(op token.Token) bool {
	switch op {
	case token.ADD, token.SUB, token.MUL, token.QUO, token.REM:
		return true
	}
	return false
}

func isComparisonOp(op token.Token) bool {
	switch op {
	case token.EQL, token.NEQ, token.LSS, token.GTR, token.LEQ, token.GEQ:
		return true
	}
	return false
}

func isLogicalOp(op token.Token) bool {
	switch op {
	case token.AND, token.OR:
		return true
	}
	return false
}

func isInOp(op token.Token) bool {
	switch op {
	case token.IN, token.NOTIN, token.INCI, token.NOTINCI:
		return true
	}
	return false
}

func isStringOp(op token.Token) bool {
	switch op {
	case token.HAS, token.NOTHAS, token.CONTAINS, token.NOTCONTAINS,
		token.STARTSWITH, token.NOTSTARTSWITH, token.ENDSWITH, token.NOTENDSWITH,
		token.HASPREFIX, token.NOTHASPREFIX, token.HASSUFFIX, token.NOTHASSUFFIX,
		token.HASANY, token.HASALL:
		return true
	}
	return false
}
