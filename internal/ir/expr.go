package ir

import "nzinfo/kql/internal/frontend/token"

// Expr is the interface for all IR scalar expressions. IR Expr trees are
// simplified, type-inferred counterparts of the AST's expression nodes — they
// drop source-literal trivia and carry resolved Type + (for columns) ColID.
//
// NOTE on naming: concrete structs use exported fields named `Position` and
// `T` (not `Pos`/`Type`) to avoid clashing with the Node.Pos() and Expr.Type()
// interface methods. Builders/readers use those field names.
type Expr interface {
	Node
	expr()
	// Type returns the inferred type (TypeUnknown until the binder fills it).
	Type() Type
}

// NamedExpr is a named scalar binding: `name = expr` or bare `expr`. It is the
// common currency of Project/Extend/Aggregate columns.
type NamedExpr struct {
	Position token.Pos
	Name     string // "" if unnamed
	Expr     Expr
}

// Pos returns the binding's start.
func (n *NamedExpr) Pos() token.Pos { return n.Position }

// ---- Leaf expressions ----

// Lit is a literal value. HasValue=false represents KQL's null literal (per
// I1.S2: aligns with rust-kql's Option-wrapped Literal, so `iff(x, null, 1)`
// has an explicit null representation).
type Lit struct {
	Position token.Pos
	T        Type
	Value    interface{} // Go value: int64/float64/string/bool/time.Duration
	HasValue bool        // false = null literal
}

// Type returns the literal's type.
func (l *Lit) Type() Type { return l.T }

// Pos returns the literal's position.
func (l *Lit) Pos() token.Pos { return l.Position }

// Col is a column reference bound to a physical ColID. Name is kept for
// diagnostics/pretty-print only; backends emit by ColID (I1.S3).
type Col struct {
	Position token.Pos
	ColID    ColID // bound by F5 binder; Invalid pre-bind
	Name     string
	T        Type
}

// Type returns the column's type.
func (c *Col) Type() Type { return c.T }

// Pos returns the column reference position.
func (c *Col) Pos() token.Pos { return c.Position }

// Star is the `*` wildcard (distinct from a Col with empty name).
type Star struct {
	Position token.Pos
}

// Type returns TypeUnknown (a star has no single type).
func (s *Star) Type() Type { return TypeUnknown }

// Pos returns the star position.
func (s *Star) Pos() token.Pos { return s.Position }

// ---- Composite expressions ----

// BinOp is a binary operation: X Op Y. Op is the KQL operator token (from
// internal/frontend/token) — arithmetic, comparison, logical, string ops.
type BinOp struct {
	Position token.Pos
	Op       token.Token
	X, Y     Expr
	T        Type // result type (inferred)
}

// Type returns the result type.
func (b *BinOp) Type() Type { return b.T }

// Pos returns the operation position.
func (b *BinOp) Pos() token.Pos { return b.Position }

// UnaryOp is a unary operation: Op X (only + / -; KQL has no unary not —
// see frontend NOTES.md §2.9).
type UnaryOp struct {
	Position token.Pos
	Op       token.Token // ADD or SUB
	X        Expr
	T        Type
}

// Type returns the result type.
func (u *UnaryOp) Type() Type { return u.T }

// Pos returns the operation position.
func (u *UnaryOp) Pos() token.Pos { return u.Position }

// FuncCall is a scalar function call. Caps tells each backend how to emit it.
type FuncCall struct {
	Position token.Pos
	Name     string // function name (e.g. "count", "sum", "bin", "iff")
	Args     []Expr
	Caps     Caps // capability bits (filled by F7 builtin table; defaults until then)
	T        Type
}

// Type returns the result type.
func (f *FuncCall) Type() Type { return f.T }

// Pos returns the call position.
func (f *FuncCall) Pos() token.Pos { return f.Position }

// Member is X.field (dynamic field access) or Table.Col dotted reference.
type Member struct {
	Position token.Pos
	X        Expr
	Field    string
	T        Type
}

// Type returns the result type.
func (m *Member) Type() Type { return m.T }

// Pos returns the member position.
func (m *Member) Pos() token.Pos { return m.Position }

// Index is X[idx] (dynamic/JSON indexing).
type Index struct {
	Position token.Pos
	X        Expr
	Index    Expr
	T        Type
}

// Type returns the result type.
func (i *Index) Type() Type { return i.T }

// Pos returns the index position.
func (i *Index) Pos() token.Pos { return i.Position }

// Case is a ternary conditional: cond ? then : else. KQL's iff() maps here.
type Case struct {
	Position             token.Pos
	Cond, Then, Else     Expr
	T                    Type
}

// Type returns the result type (of then/else branches).
func (c *Case) Type() Type { return c.T }

// Pos returns the conditional position.
func (c *Case) Pos() token.Pos { return c.Position }

// Expr + Node markers.
func (l *Lit) node()      {}
func (l *Lit) expr()      {}
func (c *Col) node()      {}
func (c *Col) expr()      {}
func (s *Star) node()     {}
func (s *Star) expr()     {}
func (b *BinOp) node()    {}
func (b *BinOp) expr()    {}
func (u *UnaryOp) node()  {}
func (u *UnaryOp) expr()  {}
func (f *FuncCall) node() {}
func (f *FuncCall) expr() {}
func (m *Member) node()   {}
func (m *Member) expr()   {}
func (i *Index) node()    {}
func (i *Index) expr()    {}
func (c *Case) node()     {}
func (c *Case) expr()     {}
func (n *NamedExpr) node() {} // NamedExpr is a Node but not an Expr
