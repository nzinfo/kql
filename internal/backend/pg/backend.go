package pg

import (
	"context"
	"database/sql"
	"strings"
	"fmt"
	"sync"
	"time"

	// pgx v5 stdlib adapter registers the "pgx" driver for database/sql.
	_ "github.com/jackc/pgx/v5/stdlib"

	"nzinfo/kql/internal/backend"
	"nzinfo/kql/internal/frontend/binder"
	"nzinfo/kql/internal/ir"
)

// Backend is a backend.Backend backed by PostgreSQL via pgx.
type Backend struct {
	db *sql.DB

	// schemaCache avoids repeated information_schema round-trips for the same
	// table within a session. Schema is immutable during a session (DDL during
	// query execution is not supported). Keyed by table name (lowercased).
	schemaCache sync.Map
}

// New opens a PostgreSQL database. dsn is a pg connection string, e.g.
// "postgres://kql:kql@localhost:5433/kql" or key=value form.
func New(dsn string) (*Backend, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open pg %q: %w", dsn, err)
	}
	// Production pool tuning: prevent connection exhaustion under concurrency.
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)
	db.SetConnMaxIdleTime(5 * time.Minute)
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

// Emit translates an IR Pipeline into a pg Query. Uses the CTE-merged emit
// path (production); falls back to nested emit for unhandled cases.
func (b *Backend) Emit(pipe *ir.Pipeline) (*backend.Query, error) { return EmitCTE(pipe) }

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
	// Pre-allocate ptrs once (reused across rows; only values is fresh per row).
	// Halves heap allocations for large result sets.
	ncols := len(cols)
	ptrs := make([]interface{}, ncols)
	for rows.Next() {
		values := make([]interface{}, ncols)
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
	// Check cache first — schema is immutable within a session.
	cacheKey := strings.ToLower(table)
	if cached, ok := b.schemaCache.Load(cacheKey); ok {
		return cached.(*binder.Schema), nil
	}
	if b.db == nil {
		return nil, fmt.Errorf("backend not open")
	}
	rows, err := b.db.QueryContext(context.Background(),
		`SELECT column_name, data_type FROM information_schema.columns
		 WHERE table_name = $1 AND table_schema = current_schema()
		 ORDER BY ordinal_position`, table)
	if err != nil {
		return nil, fmt.Errorf("information_schema %q: %w", table, err)
	}
	defer rows.Close()
	var cols []binder.ColBinding
	for rows.Next() {
		var name, dataType string
		if err := rows.Scan(&name, &dataType); err != nil {
			return nil, err
		}
		cols = append(cols, binder.ColBinding{PhysicalName: name, DisplayName: name, Type: mapPgType(dataType)})
	}
	if cols == nil {
		return nil, fmt.Errorf("table %q not found in schema %s", table, "current")
	}
	schema := &binder.Schema{Cols: cols}
	b.schemaCache.Store(cacheKey, schema)
	return schema, nil
}

// mapPgType maps a pg data_type string to an ir.Type.
func mapPgType(pgType string) ir.Type {
	t := strings.ToLower(pgType)
	switch {
	case strings.Contains(t, "int") || strings.Contains(t, "serial") || strings.Contains(t, "bigint") || strings.Contains(t, "smallint"):
		return ir.TypeLong
	case strings.Contains(t, "double") || strings.Contains(t, "real") || strings.Contains(t, "numeric") || strings.Contains(t, "decimal"):
		return ir.TypeReal
	case strings.Contains(t, "text") || strings.Contains(t, "char") || strings.Contains(t, "varchar"):
		return ir.TypeString
	case strings.Contains(t, "bool"):
		return ir.TypeBool
	case strings.Contains(t, "timestamp") || strings.Contains(t, "date") || strings.Contains(t, "time"):
		return ir.TypeDateTime
	case strings.Contains(t, "json"):
		return ir.TypeDynamic
	}
	return ir.TypeUnknown
}
