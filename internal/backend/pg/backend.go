package pg

import (
	"context"
	"database/sql"
	"fmt"

	// pgx v5 stdlib adapter registers the "pgx" driver for database/sql.
	_ "github.com/jackc/pgx/v5/stdlib"

	"nzinfo/kql/internal/backend"
	"nzinfo/kql/internal/frontend/binder"
	"nzinfo/kql/internal/ir"
)

// Backend is a backend.Backend backed by PostgreSQL via pgx.
type Backend struct {
	db *sql.DB
}

// New opens a PostgreSQL database. dsn is a pg connection string, e.g.
// "postgres://kql:kql@localhost:5433/kql" or key=value form.
func New(dsn string) (*Backend, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open pg %q: %w", dsn, err)
	}
	if err := db.PingContext(context.Background()); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping pg %q: %w", dsn, err)
	}
	return &Backend{db: db}, nil
}

// NewFromDB wraps an existing *sql.DB (caller-owned lifecycle).
func NewFromDB(db *sql.DB) *Backend { return &Backend{db: db} }

// Dialect returns DialectPostgres.
func (b *Backend) Dialect() backend.Dialect { return backend.DialectPostgres }

// Emit translates an IR Pipeline into a pg Query.
func (b *Backend) Emit(pipe *ir.Pipeline) (*backend.Query, error) { return Emit(pipe) }

// Exec runs the Query and returns the rowset.
func (b *Backend) Exec(ctx context.Context, q *backend.Query) (*backend.Result, error) {
	rows, err := b.db.QueryContext(ctx, q.SQL, q.Args...)
	if err != nil {
		return nil, fmt.Errorf("pg query: %w\nSQL: %s", err, q.SQL)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("pg columns: %w", err)
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
			return nil, fmt.Errorf("pg scan: %w", err)
		}
		result.Rows = append(result.Rows, values)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pg rows: %w", err)
	}
	return result, nil
}

// Close closes the connection (no-op if caller owns the db via NewFromDB).
func (b *Backend) Close() error {
	if b.db == nil {
		return nil
	}
	return b.db.Close()
}

// Schema implements binder.SchemaProvider: reads a table's columns from
// information_schema.columns, returning them as ColBindings. The PhysicalName
// carries pg's stored case — which is LOWERCASE for unquoted identifiers (the
// case-folding that ColID binding exists to fix). The binder resolves KQL
// references case-insensitively against these physical names.
func (b *Backend) Schema(table string) (*binder.Schema, error) {
	if b.db == nil {
		return nil, fmt.Errorf("backend not open")
	}
	rows, err := b.db.QueryContext(context.Background(),
		`SELECT column_name FROM information_schema.columns
		 WHERE table_name = $1 AND table_schema = current_schema()
		 ORDER BY ordinal_position`, table)
	if err != nil {
		return nil, fmt.Errorf("information_schema %q: %w", table, err)
	}
	defer rows.Close()
	var cols []binder.ColBinding
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		// PhysicalName = pg's stored (lowercased) name. ColID allocated by binder.
		cols = append(cols, binder.ColBinding{PhysicalName: name, DisplayName: name})
	}
	if cols == nil {
		return nil, fmt.Errorf("table %q not found in schema %s", table, "current")
	}
	return &binder.Schema{Cols: cols}, nil
}
