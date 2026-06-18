package sqlite

import (
	"strings"
	"context"
	"database/sql"
	"fmt"

	"nzinfo/kql/internal/frontend/binder"
	"nzinfo/kql/internal/ir"
)

// Schema implements binder.SchemaProvider: reads a table's column names via
// PRAGMA table_info, returning them as ColBindings (PhysicalName = the stored
// name, in its original case — sqlite preserves case). Used by pkg/kql to
// validate + bind column references at bind time (KQL001 errors with context).
//
// If the table doesn't exist, returns an error so the binder can report it.
func (b *Backend) Schema(table string) (*binder.Schema, error) {
	// Check cache first.
	cacheKey := strings.ToLower(table)
	if cached, ok := b.schemaCache.Load(cacheKey); ok {
		return cached.(*binder.Schema), nil
	}
	if b.db == nil {
		return nil, fmt.Errorf("backend not open")
	}
	rows, err := b.db.QueryContext(context.Background(),
		fmt.Sprintf("PRAGMA table_info(%s)", quoteIdent(table)))
	if err != nil {
		return nil, fmt.Errorf("table_info %q: %w", table, err)
	}
	defer rows.Close()
	var cols []binder.ColBinding
	for rows.Next() {
		// PRAGMA table_info columns: cid, name, type, notnull, dflt_value, pk.
		var cid, notnull, pk int
		var name, typ string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return nil, fmt.Errorf("table_info scan %q: %w", table, err)
		}
		// PhysicalName = stored name (sqlite preserves the declared case).
		// ColID is allocated by the binder; leave 0 here.
		cols = append(cols, binder.ColBinding{PhysicalName: name, DisplayName: name, Type: mapSQLType(typ)})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if cols == nil {
		return nil, fmt.Errorf("table %q not found", table)
	}
	schema := &binder.Schema{Cols: cols}
	b.schemaCache.Store(cacheKey, schema)
	return schema, nil
}

// mapSQLType maps a SQLite type string to an ir.Type. SQLite's type affinity is
// loose; we use the declared type name to infer the closest KQL type.
func mapSQLType(sqlType string) ir.Type {
	t := strings.ToLower(sqlType)
	switch {
	case strings.Contains(t, "int"):
		return ir.TypeLong
	case strings.Contains(t, "real") || strings.Contains(t, "float") || strings.Contains(t, "double") || strings.Contains(t, "num"):
		return ir.TypeReal
	case strings.Contains(t, "text") || strings.Contains(t, "char") || strings.Contains(t, "clob"):
		return ir.TypeString
	case strings.Contains(t, "bool"):
		return ir.TypeBool
	case strings.Contains(t, "blob") || strings.Contains(t, "binary"):
		return ir.TypeDynamic
	}
	return ir.TypeUnknown
}
