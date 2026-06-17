package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"nzinfo/kql/internal/frontend/binder"
)

// Schema implements binder.SchemaProvider: reads a table's column names via
// PRAGMA table_info. Used by pkg/kql to validate column references at bind
// time (friendly KQL009 "column not found" errors instead of a runtime
// "no such column" from SQLite with no KQL context).
//
// If the table doesn't exist, returns an error so the binder can report it.
func (b *Backend) Schema(table string) (*binder.Schema, error) {
	if b.db == nil {
		return nil, fmt.Errorf("backend not open")
	}
	rows, err := b.db.QueryContext(context.Background(),
		fmt.Sprintf("PRAGMA table_info(%s)", quoteIdent(table)))
	if err != nil {
		return nil, fmt.Errorf("table_info %q: %w", table, err)
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		// PRAGMA table_info columns: cid, name, type, notnull, dflt_value, pk.
		var cid, notnull, pk int
		var name, typ string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return nil, fmt.Errorf("table_info scan %q: %w", table, err)
		}
		cols = append(cols, name)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if cols == nil {
		// PRAGMA returns no rows for a missing table (not an error); surface it.
		return nil, fmt.Errorf("table %q not found", table)
	}
	return &binder.Schema{Cols: cols}, nil
}
