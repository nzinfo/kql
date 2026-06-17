package ast

import "nzinfo/kql/internal/frontend/token"

// BasicLit represents a literal of a primitive kind.
// Kind is one of: INT, REAL, STRING, DATETIME, TIMESPAN, GUID, BOOL.
// Value is the raw source text of the literal (including quotes for strings,
// including the datetime(...)/guid(...) wrapper for type literals).
type BasicLit struct {
	ValuePos token.Pos   // Position of the literal's first character
	Kind     token.Token // INT, REAL, STRING, DATETIME, TIMESPAN, GUID, BOOL
	Value    string      // Raw source text of the literal
}

// Pos returns the literal's start position.
func (x *BasicLit) Pos() token.Pos { return x.ValuePos }

// End returns one past the literal's last character.
func (x *BasicLit) End() token.Pos { return token.Pos(int(x.ValuePos) + len(x.Value)) }

// DynamicLit represents a dynamic(...) literal. Per the authoritative grammar
// (Kusto-Query-Language/grammar/Kql.g4:1501 dynamicLiteralExpression), this is
// built at the *parser* layer (DYNAMIC '(' jsonValue ')'), not the lexer — the
// lexer emits the DYNAMIC keyword token and the parser assembles Value from
// the JSON-like content. See internal/frontend/NOTES.md §2.3.
type DynamicLit struct {
	Dynamic token.Pos // Position of "dynamic"
	Lparen  token.Pos // Position of "("
	Value   Expr      // JSON value (ListExpr for arrays, DynamicObject for objects, BasicLit for scalars)
	Rparen  token.Pos // Position of ")"
}

// Pos returns the position of "dynamic".
func (x *DynamicLit) Pos() token.Pos { return x.Dynamic }

// End returns one past ")".
func (x *DynamicLit) End() token.Pos { return token.Pos(int(x.Rparen) + 1) }

func (*BasicLit) node()   {}
func (*BasicLit) expr()   {}
func (*DynamicLit) node() {}
func (*DynamicLit) expr() {}
