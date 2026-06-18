// Package kql — Engine struct (S1.S3).
//
// Engine is the session-level entry point for repeated KQL execution. It holds
// the backend connection + optional optimizer config, avoiding per-query DSN
// parsing and catalog loading. Designed for dashboards and services that run
// many queries against the same database.
//
// Usage:
//
//	eng, _ := kql.NewEngine(kql.EngineOpts{
//	    DSN:       "postgres://...",
//	    Policy:    kql.PolicyAggressive,
//	    StatsPath: "stats.yaml",
//	})
//	defer eng.Close()
//	res, _ := eng.Exec(ctx, `events | where state == "TX" | count`)
package kql

import (
	"context"
	"fmt"

	"nzinfo/kql/internal/backend"
)

// EngineOpts configures an Engine.
type EngineOpts struct {
	// DSN is the database connection string (required).
	DSN string
	// Policy enables cost-based optimization (empty = always-safe rules only).
	Policy Policy
	// StatsPath is the path to a stats catalog YAML (optional, for cost-based).
	StatsPath string
}

// Engine holds a backend connection + optimizer config for repeated execution.
type Engine struct {
	bk     backend.Backend
	opts   EngineOpts
	closed bool
}

// NewEngine opens a backend connection and returns an Engine ready for
// repeated Exec calls. Parses the DSN once, loads the stats catalog once.
func NewEngine(opts EngineOpts) (*Engine, error) {
	if opts.DSN == "" {
		return nil, fmt.Errorf("EngineOpts.DSN is required")
	}
	bk, err := openBackend(opts.DSN)
	if err != nil {
		return nil, fmt.Errorf("open backend: %w", err)
	}
	return &Engine{bk: bk, opts: opts}, nil
}

// Close releases the backend connection.
func (e *Engine) Close() error {
	if e.closed {
		return nil
	}
	e.closed = true
	return e.bk.Close()
}

// Exec runs a KQL query and returns the result. Uses the Engine's Policy +
// StatsPath for cost-based optimization if configured.
func (e *Engine) Exec(ctx context.Context, query string) (*Result, error) {
	if e.closed {
		return nil, fmt.Errorf("engine closed")
	}
	return ExecOnOpt(ctx, e.bk, query, ExecOpt{
		Policy:    e.opts.Policy,
		StatsPath: e.opts.StatsPath,
	})
}
