package ast

import "nzinfo/kql/internal/frontend/token"

// Ident represents an identifier. It is used both for plain names (column
// references, function names) and, via Tok, for keywords that KQL permits as
// identifiers in certain contexts (e.g. a column literally named "count").
type Ident struct {
	NamePos token.Pos   // Position of the identifier
	Name    string      // Identifier name (original case; KQL identifiers are case-sensitive)
	Tok     token.Token // Token type (IDENT, or a keyword used as a name)
}

// Pos returns the identifier's start position.
func (x *Ident) Pos() token.Pos { return x.NamePos }

// End returns one past the identifier's last character.
func (x *Ident) End() token.Pos { return token.Pos(int(x.NamePos) + len(x.Name)) }

// StarExpr represents the "*" wildcard, used in `project *`, `distinct *`,
// `count(*)`, etc.
type StarExpr struct {
	Star token.Pos // Position of "*"
}

// Pos returns the position of "*".
func (x *StarExpr) Pos() token.Pos { return x.Star }

// End returns one past "*".
func (x *StarExpr) End() token.Pos { return token.Pos(int(x.Star) + 1) }

// NamedExpr represents a named or unnamed scalar binding: `name = expr`,
// `(a, b) = expr` (tuple unpacking), or bare `expr`. It is the common currency
// of `project`, `extend`, `summarize <agg>`, and `summarize … by <key>` lists.
type NamedExpr struct {
	Name   *Ident    // Single column name (nil if unnamed or tuple form)
	Names  []*Ident  // Multiple names for tuple unpacking: (A, B) = expr
	Assign token.Pos // Position of "=" (NoPos if the binding is unnamed)
	Expr   Expr      // The bound expression
}

// Pos returns the start of the named expression (the name(s), or Expr if bare).
func (x *NamedExpr) Pos() token.Pos {
	if x.Name != nil {
		return x.Name.Pos()
	}
	if len(x.Names) > 0 {
		return x.Names[0].Pos()
	}
	return x.Expr.Pos()
}

// End returns one past the end of the bound expression.
func (x *NamedExpr) End() token.Pos { return x.Expr.End() }

// IsNamed reports whether the binding has an explicit name (`name = expr`).
func (x *NamedExpr) IsNamed() bool { return x.Name != nil || len(x.Names) > 0 }

func (*Ident) node()     {}
func (*Ident) expr()     {}
func (*StarExpr) node()  {}
func (*StarExpr) expr()  {}
func (*NamedExpr) node() {}
func (*NamedExpr) expr() {}
