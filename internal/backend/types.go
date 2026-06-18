// Package backend — KQL type → SQL type mapping (B1.S5).
//
// Each backend maps KQL's scalar types to its native SQL types. This table
// centralizes the mapping so it's consistent across backends and testable
// in isolation. Individual backends override specific entries when they have
// dialect-specific types (e.g. pg uses JSONB for dynamic; sqlite uses TEXT).
package backend

import "nzinfo/kql/internal/ir"

// SQLType maps a KQL ir.Type to its canonical SQL type for a dialect.
// Returns "TEXT" as the safe fallback for unknown types.
func SQLType(t ir.Type, dialect Dialect) string {
	switch dialect {
	case DialectPostgres, DialectDuckDB:
		return pgType(t)
	case DialectSQLite:
		return sqliteType(t)
	}
	return "TEXT"
}

func pgType(t ir.Type) string {
	switch t {
	case ir.TypeBool:
		return "BOOLEAN"
	case ir.TypeInt:
		return "INTEGER"
	case ir.TypeLong:
		return "BIGINT"
	case ir.TypeReal:
		return "DOUBLE PRECISION"
	case ir.TypeDecimal:
		return "NUMERIC"
	case ir.TypeString:
		return "TEXT"
	case ir.TypeDateTime:
		return "TIMESTAMPTZ"
	case ir.TypeTimeSpan:
		return "INTERVAL"
	case ir.TypeDynamic:
		return "JSONB"
	}
	return "TEXT"
}

func sqliteType(t ir.Type) string {
	switch t {
	case ir.TypeBool:
		return "INTEGER" // 0/1
	case ir.TypeInt, ir.TypeLong:
		return "INTEGER"
	case ir.TypeReal, ir.TypeDecimal:
		return "REAL"
	case ir.TypeString, ir.TypeDateTime, ir.TypeTimeSpan:
		return "TEXT"
	case ir.TypeDynamic:
		return "TEXT" // JSON stored as text
	}
	return "TEXT"
}

// ColumnDef is a column definition for DDL (CREATE TABLE) generation.
type ColumnDef struct {
	Name     string
	Type     ir.Type
	Nullable bool
}

// DDL generates a CREATE TABLE statement for a set of columns in a dialect.
func DDL(table string, cols []ColumnDef, dialect Dialect) string {
	out := "CREATE TABLE " + quoteIdent(table, dialect) + " ("
	for i, c := range cols {
		if i > 0 {
			out += ", "
		}
		out += quoteIdent(c.Name, dialect) + " " + SQLType(c.Type, dialect)
		if !c.Nullable {
			out += " NOT NULL"
		}
	}
	out += ")"
	return out
}

// quoteIdent quotes an identifier for a dialect.
func quoteIdent(name string, dialect Dialect) string {
	switch dialect {
	case DialectPostgres, DialectDuckDB:
		return `"` + name + `"`
	case DialectSQLite:
		return `"` + name + `"`
	}
	return name
}
