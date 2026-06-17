// Package ast declares the types used to represent KQL abstract syntax trees.
//
// Node design mirrors go/ast conventions, adapted from the cloudygreybeard/
// kqlparser template (Apache-2.0):
//   - A Node interface is satisfied by a package-private marker method (node),
//     so only types declared in this package can be AST nodes — a closed set.
//   - Expr / Stmt / Operator are sub-interfaces of Node, each with its own
//     marker (expr / stmt / operator), separating scalar, top-level statement,
//     and tabular-operator node kinds at the type level.
//
// Every node records source positions (Pos = first char, End = one-past-last)
// so diagnostics and pretty-printing can report accurate locations.
package ast

import "nzinfo/kql/internal/frontend/token"

// Node is the interface implemented by all AST nodes.
type Node interface {
	Pos() token.Pos // Position of first character belonging to the node
	End() token.Pos // Position of first character immediately after the node
	node()          // Marker method restricting implementations to this package
}

// Expr is the interface for all expression (scalar) nodes.
type Expr interface {
	Node
	expr()
}

// Stmt is the interface for all top-level statement nodes.
type Stmt interface {
	Node
	stmt()
}

// Operator is the interface for tabular query operators (where, project, …).
// Each Operator is one stage in a pipelined query.
type Operator interface {
	Node
	operator()
}

// Bad is a placeholder node for a syntactically invalid region, allowing the
// parser to keep going after an error (error recovery) instead of aborting.
type Bad struct {
	From, To token.Pos // Byte span of the bad region
}

// Pos returns the start of the bad region.
func (b *Bad) Pos() token.Pos { return b.From }

// End returns the end of the bad region.
func (b *Bad) End() token.Pos { return b.To }

// BadExpr is a placeholder expression for a syntactically invalid expression.
type BadExpr struct {
	From, To token.Pos
}

// Pos returns the start of the bad region.
func (x *BadExpr) Pos() token.Pos { return x.From }

// End returns the end of the bad region.
func (x *BadExpr) End() token.Pos { return x.To }

// Comment records a // line comment. Comments are not currently attached to
// nodes (the lexer skips them), but the type exists so future work can thread
// them through for documentation/golden purposes.
type Comment struct {
	Slash token.Pos // Position of "//"
	Text  string    // Comment text (without trailing newline)
}

// Pos returns the position of "//".
func (c *Comment) Pos() token.Pos { return c.Slash }

// End returns one past the last comment character.
func (c *Comment) End() token.Pos { return token.Pos(int(c.Slash) + len(c.Text)) }

// Marker methods — close the Node/Expr/Stmt/Operator sets to this package.
func (*Bad) node()     {}
func (*BadExpr) node() {}
func (*BadExpr) expr() {}
func (*Comment) node() {}
