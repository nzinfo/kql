package rules

import (
	"nzinfo/kql/internal/frontend/token"
	"nzinfo/kql/internal/ir"
)

// ConstantFold (O2.S4) simplifies constant expressions and predicates:
//
//   - `where 1 == 1` / `where true` → filter removed (tautology)
//   - `where 1 == 0` / `where false` → whole pipeline yields no rows; the
//     filter becomes a Limit 0 (cheaper than materialising then filtering)
//   - Arithmetic on literals folds: `1 + 2` → `3`
//   - `iff(true, a, b)` → `a`; `iff(false, a, b)` → `b` (via CASE collapse)
//
// It recurses into BinOp/FuncCall/Case sub-expressions. Safety: only folds
// when both operands are provably-constant literals; never drops a column
// reference. Dialect-agnostic (pure IR).
type ConstantFold struct{}

// Name returns the rule identifier.
func (ConstantFold) Name() string { return "ConstantFold" }

// Apply folds constants throughout the pipeline's stages. Returns changed=true
// if any expression was simplified or any filter removed/replaced.
func (ConstantFold) Apply(pipe *ir.Pipeline, _ StatsReader) (*ir.Pipeline, bool) {
	if pipe == nil {
		return pipe, false
	}
	changed := false
	out := make([]ir.Stage, 0, len(pipe.Stages))
	for _, st := range pipe.Stages {
		switch s := st.(type) {
		case *ir.Filter:
			newPred, predChanged := foldExpr(s.Predicate)
			// After folding, check if the predicate is a constant truth value.
			if verdict, ok := constantBool(newPred); ok {
				if verdict {
					// tautology — drop the filter entirely
					changed = true
					continue
				}
				// contradiction — no rows survive. Replace with Limit 0.
				out = append(out, &ir.Limit{Position: s.Position, Count: &ir.Lit{Position: s.Position, T: ir.TypeLong, Value: int64(0), HasValue: true}})
				changed = true
				continue
			}
			if predChanged {
				out = append(out, &ir.Filter{Position: s.Position, Predicate: newPred})
				changed = true
				continue
			}
			out = append(out, s)
		case *ir.Extend:
			stageChanged := false
			for _, c := range s.Cols {
				if e, ch := foldExpr(c.Expr); ch {
					c.Expr = e
					stageChanged = true
				}
			}
			changed = changed || stageChanged
			out = append(out, s)
		case *ir.Project:
			stageChanged := false
			for _, c := range s.Cols {
				if e, ch := foldExpr(c.Expr); ch {
					c.Expr = e
					stageChanged = true
				}
			}
			changed = changed || stageChanged
			out = append(out, s)
		default:
			out = append(out, st)
		}
	}
	pipe.Stages = out
	return pipe, changed
}

// foldExpr recursively folds constant sub-expressions. Returns the (possibly
// new) expression and changed=true if a fold happened.
func foldExpr(e ir.Expr) (ir.Expr, bool) {
	if e == nil {
		return e, false
	}
	switch n := e.(type) {
	case *ir.BinOp:
		x, xch := foldExpr(n.X)
		y, ych := foldExpr(n.Y)
		n.X, n.Y = x, y
		// Try to fold if both sides are now literals.
		if folded, ok := foldBinOpLit(n); ok {
			return folded, true
		}
		return n, xch || ych
	case *ir.UnaryOp:
		x, ch := foldExpr(n.X)
		n.X = x
		if folded, ok := foldUnaryLit(n); ok {
			return folded, true
		}
		return n, ch
	case *ir.FuncCall:
		anyCh := false
		for i, a := range n.Args {
			if f, ch := foldExpr(a); ch {
				n.Args[i] = f
				anyCh = true
			}
		}
		// iff(true,a,b) → a ; iff(false,a,b) → b
		if len(n.Args) == 3 {
			if v, ok := constantBool(n.Args[0]); ok {
				if v {
					return n.Args[1], true // then-branch
				}
				return n.Args[2], true // else-branch
			}
		}
		return n, anyCh
	case *ir.Case:
		cond, cch := foldExpr(n.Cond)
		then, tch := foldExpr(n.Then)
		els, ech := foldExpr(n.Else)
		n.Cond, n.Then, n.Else = cond, then, els
		// CASE WHEN true THEN a ELSE b → a ; CASE WHEN false → b
		if v, ok := constantBool(cond); ok {
			if v {
				return then, true
			}
			return els, true
		}
		return n, cch || tch || ech
	case *ir.List:
		anyCh := false
		for i, el := range n.Elems {
			if f, ch := foldExpr(el); ch {
				n.Elems[i] = f
				anyCh = true
			}
		}
		return n, anyCh
	}
	return e, false
}

// foldBinOpLit folds a BinOp whose both operands are literals.
func foldBinOpLit(b *ir.BinOp) (ir.Expr, bool) {
	x, ok1 := b.X.(*ir.Lit)
	y, ok2 := b.Y.(*ir.Lit)
	if !ok1 || !ok2 || !x.HasValue || !y.HasValue {
		return nil, false
	}
	// Comparison → bool literal.
	switch b.Op {
	case token.EQL:
		return boolLit(litEqual(x, y)), true
	case token.NEQ:
		return boolLit(!litEqual(x, y)), true
	}
	// Arithmetic on numeric literals.
	xn, xok := asNumber(x)
	yn, yok := asNumber(y)
	if !xok || !yok {
		return nil, false
	}
	switch b.Op {
	case token.ADD:
		return numLit(xn+yn), true
	case token.SUB:
		return numLit(xn-yn), true
	case token.MUL:
		return numLit(xn*yn), true
	case token.QUO:
		if yn == 0 {
			return nil, false
		}
		return numLit(xn/yn), true
	}
	return nil, false
}

// foldUnaryLit folds a unary +/- on a literal.
func foldUnaryLit(u *ir.UnaryOp) (ir.Expr, bool) {
	x, ok := u.X.(*ir.Lit)
	if !ok || !x.HasValue {
		return nil, false
	}
	n, ok := asNumber(x)
	if !ok {
		return nil, false
	}
	if u.Op == token.SUB {
		return numLit(-n), true
	}
	return numLit(n), true // unary +
}

// constantBool reports whether e is a literal bool, returning its value.
func constantBool(e ir.Expr) (bool, bool) {
	l, ok := e.(*ir.Lit)
	if !ok || !l.HasValue {
		return false, false
	}
	switch v := l.Value.(type) {
	case bool:
		return v, true
	case int64:
		return v != 0, true
	case float64:
		return v != 0, true
	}
	return false, false
}

// litEqual reports whether two literals hold the same value.
func litEqual(a, b *ir.Lit) bool {
	return asGoValue(a) == asGoValue(b)
}

// asGoValue returns a comparable representation of a literal's value.
func asGoValue(l *ir.Lit) interface{} { return l.Value }

// asNumber extracts a float64 from a numeric literal.
func asNumber(l *ir.Lit) (float64, bool) {
	switch v := l.Value.(type) {
	case int64:
		return float64(v), true
	case float64:
		return v, true
	}
	return 0, false
}

func boolLit(v bool) *ir.Lit {
	return &ir.Lit{T: ir.TypeBool, Value: v, HasValue: true}
}

// numLit wraps a folded number back into a Lit. Integers stay int64 (so the
// driver binds them as integers); non-integers become float64.
func numLit(n float64) *ir.Lit {
	if n == float64(int64(n)) {
		return &ir.Lit{T: ir.TypeLong, Value: int64(n), HasValue: true}
	}
	return &ir.Lit{T: ir.TypeReal, Value: n, HasValue: true}
}
