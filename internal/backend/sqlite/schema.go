package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"nzinfo/kql/internal/frontend/binder"
)

// Schema implements binder.SchemaProvider: reads a table's column names via
// PRAGMA table_info, returning them as ColBindings (PhysicalName = the stored
// name, in its original case — sqlite preserves case). Used by pkg/kql to
// validate + bind column references at bind time (KQL001 errors with context).
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
		cols = append(cols, binder.ColBinding{PhysicalName: name, DisplayName: name})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if cols == nil {
		return nil, fmt.Errorf("table %q not found", table)
	}
	return &binder.Schema{Cols: cols}, nil
}
