// Package sqlite is a backend.Backend implementation over the modernc.org/sqlite
// pure-Go driver. It emits SQLite SQL from an IR Pipeline and executes it.
//
// Driver choice (recorded in backend/NOTES.md): modernc.org/sqlite is pure Go
// (no cgo), making the e2e validation loop cross-compile-clean and toolchain-
// free. DESIGN.md §7 lists mattn/go-sqlite3 (cgo) as the production sqlite
// driver; for the minimal e2e loop the pure-Go driver is preferable and can be
// swapped via build tags later without changing this package's surface.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite" // register the "sqlite" driver

	"nzinfo/kql/internal/backend"
	"nzinfo/kql/internal/ir"
)

// Backend is a backend.Backend backed by a SQLite database.
type Backend struct {
	db *sql.DB
}

// New opens a SQLite database at dsn (e.g. "file:test.db" or ":memory:").
// The connection is pooled per database/sql semantics.
func New(dsn string) (*Backend, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %q: %w", dsn, err)
	}
	// Validate connectivity (modernc driver connects lazily otherwise).
	if err := db.PingContext(context.Background()); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping sqlite %q: %w", dsn, err)
	}
	return &Backend{db: db}, nil
}

// NewFromDB wraps an existing *sql.DB (e.g. a caller-managed in-memory db for
// tests). The Backend does not take ownership of Close.
func NewFromDB(db *sql.DB) *Backend { return &Backend{db: db} }

// Dialect returns DialectSQLite.
func (b *Backend) Dialect() backend.Dialect { return backend.DialectSQLite }

// Emit translates an IR Pipeline into a SQLite Query.
func (b *Backend) Emit(pipe *ir.Pipeline) (*backend.Query, error) { return Emit(pipe) }

// Exec runs the Query and returns the rowset.
func (b *Backend) Exec(ctx context.Context, q *backend.Query) (*backend.Result, error) {
	rows, err := b.db.QueryContext(ctx, q.SQL, q.Args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite query: %w\nSQL: %s", err, q.SQL)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("sqlite columns: %w", err)
	}
	result := &backend.Result{Columns: make([]backend.ResultColumn, len(cols))}
	for i, c := range cols {
		result.Columns[i] = backend.ResultColumn{Name: c}
	}

	for rows.Next() {
		// Scan into a slice of interface{} backed by the driver's native types.
		values := make([]interface{}, len(cols))
		ptrs := make([]interface{}, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, fmt.Errorf("sqlite scan: %w", err)
		}
		result.Rows = append(result.Rows, values)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite rows: %w", err)
	}
	return result, nil
}

// Close closes the underlying database connection (no-op if constructed via
// NewFromDB and the caller owns the db).
func (b *Backend) Close() error {
	if b.db == nil {
		return nil
	}
	return b.db.Close()
}
