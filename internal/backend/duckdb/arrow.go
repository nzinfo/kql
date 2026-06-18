//go:build duckdb_arrow

// Package duckdb — Arrow zero-copy execution (Step 0a, requires -tags duckdb_arrow).
//
// This file implements the ArrowBackend interface using DuckDB's native Arrow
// C Data Interface. When built with -tags duckdb_arrow, the DuckDB backend
// returns query results as arrow-go RecordReaders — true zero-copy from
// DuckDB's columnar buffers.
//
// Usage: build with `go build -tags duckdb_arrow` to enable. Without the tag,
// this file is excluded and the backend uses the row-based Exec path (no
// regression).
package duckdb

import (
	"context"
	"fmt"

	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/duckdb/duckdb-go/v2"

	"nzinfo/kql/internal/backend"
)

// Compile-time assertion that Backend implements ArrowBackend under the build tag.
var _ backend.ArrowBackend = (*Backend)(nil)

// ExecArrow implements backend.ArrowBackend. It grabs a raw *duckdb.Conn from
// the connection pool, wraps it with NewArrowFromConn, and executes the query
// via the Arrow C Data Interface — zero-copy from DuckDB's columnar storage.
//
// The returned RecordReader MUST be Released by the caller.
func (b *Backend) ExecArrow(ctx context.Context, q *backend.Query) (array.RecordReader, error) {
	if b == nil || b.db == nil {
		return nil, fmt.Errorf("duckdb: nil backend or db")
	}

	// Grab a dedicated connection from the pool.
	conn, err := b.db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("duckdb arrow: get conn: %w", err)
	}
	defer conn.Close()

	// Access the raw *duckdb.Conn via Raw.
	var driverConn *duckdb.Conn
	if err := conn.Raw(func(dc any) error {
		c, ok := dc.(*duckdb.Conn)
		if !ok {
			return fmt.Errorf("duckdb arrow: unexpected conn type %T", dc)
		}
		driverConn = c
		return nil
	}); err != nil {
		return nil, fmt.Errorf("duckdb arrow: raw conn: %w", err)
	}

	// Create the Arrow bridge.
	ar, err := duckdb.NewArrowFromConn(driverConn)
	if err != nil {
		return nil, fmt.Errorf("duckdb arrow: new arrow: %w", err)
	}

	// Execute the query via the Arrow path — returns a RecordReader.
	reader, err := ar.QueryContext(ctx, q.SQL, q.Args...)
	if err != nil {
		return nil, fmt.Errorf("duckdb arrow: query: %w", err)
	}

	return reader, nil
}

// RegisterArrowView registers an Arrow RecordReader as a named view in DuckDB.
// This enables the multi-engine pipeline: pg results → Arrow → DuckDB view →
// DuckDB SQL query. The returned release function MUST be called when done.
func (b *Backend) RegisterArrowView(ctx context.Context, name string, reader array.RecordReader) (func(), error) {
	if b == nil || b.db == nil {
		return nil, fmt.Errorf("duckdb: nil backend or db")
	}

	conn, err := b.db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("duckdb arrow register: get conn: %w", err)
	}

	var driverConn *duckdb.Conn
	if err := conn.Raw(func(dc any) error {
		c, ok := dc.(*duckdb.Conn)
		if !ok {
			return fmt.Errorf("unexpected conn type %T", dc)
		}
		driverConn = c
		return nil
	}); err != nil {
		conn.Close()
		return nil, err
	}

	ar, err := duckdb.NewArrowFromConn(driverConn)
	if err != nil {
		conn.Close()
		return nil, err
	}

	release, err := ar.RegisterView(reader, name)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("duckdb arrow register: %w", err)
	}

	// Return a cleanup that releases the view AND closes the conn.
	return func() {
		release()
		conn.Close()
	}, nil
}
