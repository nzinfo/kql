// Package backend — ArrowBackend optional interface (Step 0a).
//
// ArrowBackend is an optional capability interface that backends implement when
// they can return query results as Apache Arrow RecordReaders (zero-copy
// columnar). ExecPipeline type-asserts this to decide whether to use the Arrow
// execution path (Step 1) or the row-based path.
//
// Currently only DuckDB implements this (via its native Arrow C Data Interface,
// behind the duckdb_arrow build tag). pg/sqlite do not — they produce rows.
package backend

import (
	"context"

	"github.com/apache/arrow-go/v18/arrow/array"
)

// ArrowBackend is implemented by backends that can return Arrow columnar results.
// The RecordReader must be Released by the caller when done.
type ArrowBackend interface {
	// ExecArrow executes a query and returns the results as an Arrow
	// RecordReader (streaming columnar batches). The caller MUST call
	// reader.Release() when done.
	ExecArrow(ctx context.Context, q *Query) (array.RecordReader, error)
}
