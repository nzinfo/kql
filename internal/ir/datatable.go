package ir

import (
	"nzinfo/kql/internal/frontend/token"
)

// SourceDatatableLit is a datatable(...) literal source, materialised from
// the inline data. Unlike the placeholder SourceTable used previously, this
// carries the schema + row data so the backend can emit a VALUES clause
// (or the exec layer can synthesise rows client-side).
//
// Schema is the column names (types are inferred from the data). Rows is a
// flat list of literal expressions: row i occupies positions [i*nCols, (i+1)*nCols).
type SourceDatatableLit struct {
	Position token.Pos
	ColNames []string    // column names from the schema
	Rows     [][]Expr    // each inner slice is one row's cell expressions
}

// Pos returns the datatable position.
func (s *SourceDatatableLit) Pos() token.Pos { return s.Position }

// node + source markers.
func (*SourceDatatableLit) node()   {}
func (*SourceDatatableLit) source() {}
