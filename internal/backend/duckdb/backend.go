// Package duckdb is a backend.Backend implementation over DuckDB via the
// official github.com/duckdb/duckdb-go/v2 driver. DuckDB is the analytics-
// acceleration backend (DESIGN.md §7): columnar, in-process, fast aggregates.
//
// The emit structure mirrors pg ($N placeholders, ILIKE, double-quoted
// identifiers) since DuckDB's SQL dialect is largely Postgres-compatible.
// Differences: DuckDB has native list/struct types (better mv-expand potential
// later), and its function names mostly match pg. The first cut reuses pg's
// emitter verbatim — divergence lands as real DuckDB-specific needs arise.
package duckdb

import (
	"context"
	"database/sql"
	"fmt"
	"runtime"
	"sync"

	// duckdb-go v2 registers the "duckdb" driver for database/sql.
	_ "github.com/duckdb/duckdb-go/v2"

	"nzinfo/kql/internal/backend"
	"nzinfo/kql/internal/frontend/binder"
	"nzinfo/kql/internal/ir"
)

// Backend is a backend.Backend backed by DuckDB.
type Backend struct {
	db *sql.DB

	// schemaCache avoids repeated information_schema round-trips.
	schemaCache sync.Map
}

// New opens a DuckDB database. dsn examples: "" (in-memory), "file:path.duckdb".
//
// Performance configuration applied on open:
//   - preserve_insertion_order=false: 1.5-3× aggregate/sort speedup when no
//     ORDER BY is present (DuckDB skips order bookkeeping).
//   - threads=min(8, NumCPU): deterministic parallelism (avoids hogging all
//     cores when co-located with other services).
//   - SetMaxOpenConns(1): DuckDB parallelizes WITHIN a connection; multiple
//     database/sql pool connections contend on the same instance.
func New(dsn string) (*Backend, error) {
	if dsn == "" {
		dsn = ":memory:" // DuckDB in-memory
	}
	db, err := sql.Open("duckdb", dsn)
	if err != nil {
		return nil, fmt.Errorf("open duckdb %q: %w", dsn, err)
	}
	if err := db.PingContext(context.Background()); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping duckdb %q: %w", dsn, err)
	}
	// Apply performance pragmas (ignore errors — older DuckDB may not support all).
	threads := runtime.NumCPU()
	if threads > 8 {
		threads = 8
	}
	for _, pragma := range []string{
		fmt.Sprintf("SET preserve_insertion_order=false"),
		fmt.Sprintf("SET threads=%d", threads),
		"SET enable_object_cache=true",
	} {
		db.ExecContext(context.Background(), pragma) // best-effort
	}
	// DuckDB parallelizes within a single connection; multiple pool connections
	// contend on the same in-process instance.
	db.SetMaxOpenConns(1)
	return &Backend{db: db}, nil
}

// NewFromDB wraps an existing *sql.DB (caller-owned lifecycle).
// Note: performance pragmas are NOT applied (caller manages the DB).
func NewFromDB(db *sql.DB) *Backend { return &Backend{db: db} }

// Dialect returns DialectDuckDB.
func (b *Backend) Dialect() backend.Dialect { return backend.DialectDuckDB }

// Emit translates an IR Pipeline into a DuckDB Query using the independent
// DuckDB emitter (no pg-specific hints/MATERIALIZED; DuckDB-native functions).
func (b *Backend) Emit(pipe *ir.Pipeline) (*backend.Query, error) { return Emit(pipe) }

// Exec runs the Query and returns the rowset.
func (b *Backend) Exec(ctx context.Context, q *backend.Query) (*backend.Result, error) {
	rows, err := b.db.QueryContext(ctx, q.SQL, q.Args...)
	if err != nil {
		return nil, fmt.Errorf("duckdb query: %w\nSQL: %s", err, q.SQL)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("duckdb columns: %w", err)
	}
	result := &backend.Result{Columns: make([]backend.ResultColumn, len(cols))}
	for i, c := range cols {
		result.Columns[i] = backend.ResultColumn{Name: c}
	}
	for rows.Next() {
		values := make([]interface{}, len(cols))
		ptrs := make([]interface{}, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, fmt.Errorf("duckdb scan: %w", err)
		}
		result.Rows = append(result.Rows, values)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("duckdb rows: %w", err)
	}
	return result, nil
}

// Close closes the connection.
func (b *Backend) Close() error {
	if b.db == nil {
		return nil
	}
	return b.db.Close()
}

// Schema implements binder.SchemaProvider: reads columns via information_schema
// (DuckDB supports the standard information_schema). Returns DuckDB's stored
// case (lowercase, like pg).
func (b *Backend) Schema(table string) (*binder.Schema, error) {
	if b.db == nil {
		return nil, fmt.Errorf("backend not open")
	}
	rows, err := b.db.QueryContext(context.Background(),
		`SELECT column_name FROM information_schema.columns
		 WHERE table_name = $1 ORDER BY ordinal_position`, table)
	if err != nil {
		return nil, fmt.Errorf("duckdb information_schema %q: %w", table, err)
	}
	defer rows.Close()
	var cols []binder.ColBinding
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		cols = append(cols, binder.ColBinding{PhysicalName: name, DisplayName: name})
	}
	if cols == nil {
		return nil, fmt.Errorf("table %q not found", table)
	}
	return &binder.Schema{Cols: cols}, nil
}
