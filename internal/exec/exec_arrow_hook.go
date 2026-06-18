// Package exec — Arrow hook (no build tag, always compiled).
//
// This file provides the hook point for the Arrow execution path. When built
// with -tags duckdb_arrow, exec_arrow.go overrides arrowExecHook. Without the
// tag, arrowExecHook stays nil and ExecPipeline uses the row path.
package exec

import (
	"context"

	"nzinfo/kql/internal/backend"
	"nzinfo/kql/internal/ir"
)

// arrowExecHook is nil unless -tags duckdb_arrow is active. When non-nil,
// ExecPipeline calls it first; if it returns true, the row path is skipped.
var arrowExecHook func(ctx context.Context, bk backend.Backend, pipe *ir.Pipeline) (*Result, bool, error)
