package ir

import "nzinfo/kql/internal/frontend/token"

// Source implementations. MVP implements SourceTable; the others are reserved
// as placeholders so the Source interface has room for the g4/rust-kql source
// variants (datatable/print/range/union/externaldata/find) without an interface
// reshape later. Each is a minimal stub for now.
//
// NOTE: position fields are named `Position` (not `Pos`) to avoid clashing with
// the Node.Pos() interface method.

// SourceTable is a named table reference: `T` or `cluster("c").database("d").T`.
// Table is the backend-resolved table name (string for MVP; may become a richer
// handle when the catalog lands). Columns are the source's known output schema
// (filled by the binder in F5; nil until then).
type SourceTable struct {
	Position token.Pos
	Cluster  string  // cluster name, "" if default (reserved)
	Database string  // database name, "" if default (reserved)
	Table    string  // table name
	Columns  []Column // output schema (filled by binder; nil pre-bind)
}

// Pos returns the table reference position.
func (s *SourceTable) Pos() token.Pos { return s.Position }

// SourceDatatable is a datatable(...) literal source (reserved; not in MVP).
type SourceDatatable struct {
	Position token.Pos
	Schema   []Column
	Rows     [][]Expr
}

// Pos returns the datatable position.
func (s *SourceDatatable) Pos() token.Pos { return s.Position }

// SourcePrint is a print ... source yielding one row (reserved; not in MVP).
type SourcePrint struct {
	Position token.Pos
	Cols     []*NamedExpr
}

// Pos returns the print position.
func (s *SourcePrint) Pos() token.Pos { return s.Position }

// SourceRange is a range(...) source (reserved; not in MVP).
type SourceRange struct {
	Position token.Pos
	Name     string
	From, To, Step Expr
}

// Pos returns the range position.
func (s *SourceRange) Pos() token.Pos { return s.Position }

// Source markers.
func (*SourceTable) node()     {}
func (*SourceTable) source()   {}
func (*SourceDatatable) node() {}
func (*SourceDatatable) source() {}
func (*SourcePrint) node()     {}
func (*SourcePrint) source()   {}
func (*SourceRange) node()     {}
func (*SourceRange) source()   {}
