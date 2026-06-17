// Package backend defines the interface that SQL-emitting backends implement,
// and the dialect-agnostic types shared across them.
//
// For the minimal e2e loop (per docs/PROGRESS.md / user direction), the backend
// consumes an *ir.Pipeline DIRECTLY and emits SQL — the full PhysicalPlan /
// optimizer coupling from B1 is deferred. This keeps the e2e path short and is
// recorded as an intentional simplification (see backend/NOTES.md). When the
// optimizer lands, backends will accept a PhysicalPlan; the emit logic built
// here composes naturally under that.
package backend

import (
	"context"

	"nzinfo/kql/internal/ir"
)

// Dialect identifies a SQL backend.
type Dialect int

// Supported dialects.
const (
	DialectSQLite Dialect = iota
	DialectPostgres
	DialectDuckDB
)

// String returns the dialect name.
func (d Dialect) String() string {
	switch d {
	case DialectSQLite:
		return "sqlite"
	case DialectPostgres:
		return "postgres"
	case DialectDuckDB:
		return "duckdb"
	}
	return "unknown"
}

// Query is a generated SQL statement with its bind parameters.
type Query struct {
	SQL  string
	Args []interface{}
}

// Result is the rowset returned by executing a Query.
// For the minimal loop, columns carry names + types and rows are []interface{}
// per cell (driver-native values). A richer Arrow-typed result comes later.
type Result struct {
	Columns []ResultColumn
	Rows    [][]interface{}
}

// ResultColumn describes one output column.
type ResultColumn struct {
	Name string
	Type ir.Type
}

// Backend emits SQL for a dialect and executes it against a data source.
type Backend interface {
	// Dialect returns this backend's dialect.
	Dialect() Dialect

	// Emit translates an IR Pipeline into a backend Query (SQL + bind args).
	Emit(pipe *ir.Pipeline) (*Query, error)

	// Exec runs the Query and returns the rowset. The dsn/connection is owned
	// by the concrete backend (e.g. sqliteBackend holds a *sql.DB).
	Exec(ctx context.Context, q *Query) (*Result, error)
}
