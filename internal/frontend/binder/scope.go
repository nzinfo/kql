// Package binder — symbol + scope (F5.S1).
//
// The scope stack resolves names in nested let-bindings: inner scopes shadow
// outer ones. Each scope holds symbols (let-bound scalars/tables + the source
// table's columns). When the binder resolves a column reference, it searches
// from the innermost scope outward.
//
// Symbol kinds:
//   - SymColumn: a column reference (from a table schema).
//   - SymScalar: a let-bound scalar (let x = 42).
//   - SymTable:  a let-bound tabular expression (let T = events | take 10).

package binder

import "nzinfo/kql/internal/ir"

// SymbolKind classifies a symbol-table entry.
type SymbolKind int

const (
	SymColumn SymbolKind = iota
	SymScalar
	SymTable
)

// Symbol is one entry in a scope.
type Symbol struct {
	Name   string
	Kind   SymbolKind
	ColID  ir.ColID // for SymColumn
	Type   ir.Type  // for SymScalar/SymColumn
}

// Scope is one frame in the scope stack. Scopes nest: child.Lookup falls
// through to parent when not found locally.
type Scope struct {
	syms    map[string]*Symbol
	parent  *Scope
}

// NewScope creates a child scope of parent (nil for the global scope).
func NewScope(parent *Scope) *Scope {
	return &Scope{syms: make(map[string]*Symbol), parent: parent}
}

// Bind adds a symbol to this scope. A later Bind with the same name shadows
// the earlier one in this scope frame.
func (s *Scope) Bind(sym *Symbol) {
	s.syms[sym.Name] = sym
}

// Lookup searches this scope and its ancestors (innermost-first) for a name.
func (s *Scope) Lookup(name string) (*Symbol, bool) {
	if sym, ok := s.syms[name]; ok {
		return sym, true
	}
	if s.parent != nil {
		return s.parent.Lookup(name)
	}
	return nil, false
}

// Local returns the symbol only if bound in THIS scope (not ancestors).
func (s *Scope) Local(name string) (*Symbol, bool) {
	sym, ok := s.syms[name]
	return sym, ok
}
