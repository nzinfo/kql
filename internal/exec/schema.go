// Package exec — schema description (S2.S2).
//
// Schema describes a result set's columns (name + type), derived from the IR
// pipeline's projection. It's the exec-layer counterpart to the binder's Schema
// (which carries ColIDs + physical names); this one is purely descriptive —
// used by output formatters and the columnar Record builder.
package exec

import "nzinfo/kql/internal/ir"

// ColumnDesc describes one result column.
type ColumnDesc struct {
	Name string
	Type ir.Type
}

// Schema is a list of column descriptors.
type Schema struct {
	Columns []ColumnDesc
}

// Names returns just the column names.
func (s *Schema) Names() []string {
	names := make([]string, len(s.Columns))
	for i, c := range s.Columns {
		names[i] = c.Name
	}
	return names
}

// SchemaFromColumns builds a Schema from the exec.Result's column names (types
// default to TypeUnknown since row-based results don't carry type info).
func SchemaFromColumns(cols []string) *Schema {
	s := &Schema{Columns: make([]ColumnDesc, len(cols))}
	for i, c := range cols {
		s.Columns[i] = ColumnDesc{Name: c, Type: ir.TypeUnknown}
	}
	return s
}

// SchemaFromRecord builds a Schema from a columnar.Record's typed columns.
func SchemaFromRecord(r *columnarRecord) *Schema {
	// This is a forward-declaration hook — the actual Record integration is
	// in the columnar package. For now, build from names + unknown types.
	return nil // implemented when Record is wired into exec
}

// columnarRecord is a placeholder type reference to avoid an import cycle with
// internal/columnar. The real integration passes through columnar.Record.
type columnarRecord struct{}
