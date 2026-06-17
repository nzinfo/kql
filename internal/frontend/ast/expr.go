package ast

import "nzinfo/kql/internal/frontend/token"

// BinaryExpr represents a binary expression: X Op Y.
// Op covers arithmetic (+, -, *, /, %), comparison (==, !=, <, …, =~, !~),
// logical (and, or), string operators (has, contains, startswith, …, matches regex),
// and list/range operators (in, between). See token.Precedence for the precedence ladder.
type BinaryExpr struct {
	X     Expr        // Left operand
	OpPos token.Pos   // Position of the operator
	Op    token.Token // Operator
	Y     Expr        // Right operand
}

// Pos returns the start of the left operand.
func (x *BinaryExpr) Pos() token.Pos { return x.X.Pos() }

// End returns one past the end of the right operand.
func (x *BinaryExpr) End() token.Pos { return x.Y.End() }

// UnaryExpr represents a unary expression: Op X.
// Op is one of: ADD (+), SUB (-), NOT (logical not).
type UnaryExpr struct {
	OpPos token.Pos   // Position of the operator
	Op    token.Token // Operator: ADD, SUB, or NOT
	X     Expr        // Operand
}

// Pos returns the operator position.
func (x *UnaryExpr) Pos() token.Pos { return x.OpPos }

// End returns one past the end of the operand.
func (x *UnaryExpr) End() token.Pos { return x.X.End() }

// ParenExpr represents a parenthesized expression: ( X ).
type ParenExpr struct {
	Lparen token.Pos
	X      Expr
	Rparen token.Pos
}

// Pos returns the position of "(".
func (x *ParenExpr) Pos() token.Pos { return x.Lparen }

// End returns one past ")".
func (x *ParenExpr) End() token.Pos { return token.Pos(int(x.Rparen) + 1) }

// CallExpr represents a function call: Fun ( Args ).
// Fun is typically an *Ident, but KQL permits member access on call results,
// so Fun is kept as a general Expr.
type CallExpr struct {
	Fun    Expr      // Function expression (usually *Ident)
	Lparen token.Pos // Position of "("
	Args   []Expr    // Arguments
	Rparen token.Pos // Position of ")"
}

// Pos returns the function expression's start.
func (x *CallExpr) Pos() token.Pos { return x.Fun.Pos() }

// End returns one past ")".
func (x *CallExpr) End() token.Pos { return token.Pos(int(x.Rparen) + 1) }

// SelectorExpr represents a member access: X.Sel (e.g. Column.SubField, or
// Table.Column in a dotted reference).
type SelectorExpr struct {
	X   Expr      // Receiver expression
	Dot token.Pos // Position of "."
	Sel *Ident    // Selector identifier
}

// Pos returns the receiver's start.
func (x *SelectorExpr) Pos() token.Pos { return x.X.Pos() }

// End returns one past the selector's last character.
func (x *SelectorExpr) End() token.Pos { return x.Sel.End() }

// IndexExpr represents an index access: X [ Index ] (dynamic/JSON indexing).
type IndexExpr struct {
	X        Expr      // Expression being indexed
	Lbracket token.Pos // Position of "["
	Index    Expr      // Index expression
	Rbracket token.Pos // Position of "]"
}

// Pos returns the indexed expression's start.
func (x *IndexExpr) Pos() token.Pos { return x.X.Pos() }

// End returns one past "]".
func (x *IndexExpr) End() token.Pos { return token.Pos(int(x.Rbracket) + 1) }

// ListExpr represents a parenthesised comma-separated list: ( e1, e2, … ).
// Used for IN lists and multi-value expressions.
type ListExpr struct {
	Lparen token.Pos
	Elems  []Expr
	Rparen token.Pos
}

// Pos returns the position of "(".
func (x *ListExpr) Pos() token.Pos { return x.Lparen }

// End returns one past ")".
func (x *ListExpr) End() token.Pos { return token.Pos(int(x.Rparen) + 1) }

// BetweenExpr represents a range test: X between ( Low , High ) or X !between (…).
type BetweenExpr struct {
	X      Expr      // Value to test
	OpPos  token.Pos // Position of "between" or "!between"
	Not    bool      // True for !between
	Lparen token.Pos // Position of "("
	Low    Expr      // Low bound (inclusive)
	High   Expr      // High bound (inclusive)
	Rparen token.Pos // Position of ")"
}

// Pos returns the tested value's start.
func (x *BetweenExpr) Pos() token.Pos { return x.X.Pos() }

// End returns one past ")".
func (x *BetweenExpr) End() token.Pos { return token.Pos(int(x.Rparen) + 1) }

// ConditionalExpr represents a ternary conditional: Cond ? Then : Else.
type ConditionalExpr struct {
	Cond     Expr
	Question token.Pos // Position of "?"
	Then     Expr
	Colon    token.Pos // Position of ":"
	Else     Expr
}

// Pos returns the condition's start.
func (x *ConditionalExpr) Pos() token.Pos { return x.Cond.Pos() }

// End returns one past the end of the else branch.
func (x *ConditionalExpr) End() token.Pos { return x.Else.End() }

// CastExpr represents an explicit type cast: X to <type> (e.g. `str to string`).
type CastExpr struct {
	X      Expr        // Value to cast
	ToPos  token.Pos   // Position of "to"
	Type   token.Token // Target type keyword (STRINGTYPE, LONGTYPE, …)
	TypeAt token.Pos   // Position of the type keyword
}

// Pos returns the value's start.
func (x *CastExpr) Pos() token.Pos { return x.X.Pos() }

// End returns one past the end of the type keyword.
func (x *CastExpr) End() token.Pos { return token.Pos(int(x.TypeAt) + len(x.Type.String())) }

// Expression node markers.
func (*BinaryExpr) node()      {}
func (*BinaryExpr) expr()      {}
func (*UnaryExpr) node()       {}
func (*UnaryExpr) expr()       {}
func (*ParenExpr) node()       {}
func (*ParenExpr) expr()       {}
func (*CallExpr) node()        {}
func (*CallExpr) expr()        {}
func (*SelectorExpr) node()    {}
func (*SelectorExpr) expr()    {}
func (*IndexExpr) node()       {}
func (*IndexExpr) expr()       {}
func (*ListExpr) node()        {}
func (*ListExpr) expr()        {}
func (*BetweenExpr) node()     {}
func (*BetweenExpr) expr()     {}
func (*ConditionalExpr) node() {}
func (*ConditionalExpr) expr() {}
func (*CastExpr) node()        {}
func (*CastExpr) expr()        {}
