//go:build duckdb_arrow

// Package exec — Arrow execution path (Step 1, requires -tags duckdb_arrow).
//
// When the backend implements backend.ArrowBackend, ExecPipeline uses the Arrow
// path: ExecArrow returns a streaming RecordReader, which is drained into rows
// at the exec boundary. This enables DuckDB's zero-copy columnar output to
// flow directly into the exec layer without intermediate [][]interface{}
// boxing at the backend level.
//
// For PostProc stages (client-side processing), the Arrow data is drained to
// rows once, then processed as before. Future Arrow-native PostProc operators
// could keep data in columnar form throughout.
package exec

import (
	"context"
	"fmt"

	"nzinfo/kql/internal/backend"
	"nzinfo/kql/internal/columnar"
	"nzinfo/kql/internal/ir"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
)

// tryArrowExec attempts the Arrow execution path. Returns (result, true, nil)
// if the Arrow path was used; (nil, false, nil) to signal "use row path".
func tryArrowExec(ctx context.Context, bk backend.Backend, pipe *ir.Pipeline) (*Result, bool, error) {
	ab, ok := bk.(backend.ArrowBackend)
	if !ok {
		return nil, false, nil // no ArrowBackend → row path
	}

	// Check for IndexLookup (two-phase strategy doesn't benefit from Arrow yet).
	if findIndexLookupJoin(pipe.Stages) >= 0 {
		return nil, false, nil // IndexLookup handles its own execution
	}

	splitIdx := findPostProcBoundary(pipe.Stages)
	if splitIdx < 0 {
		// No PostProc: emit the whole pipeline and execute via Arrow.
		q, err := bk.Emit(pipe)
		if err != nil {
			return nil, true, fmt.Errorf("emit: %w", err)
		}
		reader, err := ab.ExecArrow(ctx, q)
		if err != nil {
			return nil, true, fmt.Errorf("exec arrow: %w", err)
		}
		defer reader.Release()
		return drainArrowReader(reader), true, nil
	}

	// Split: pre-stages → Arrow SQL, post-stages → client-side on drained rows.
	prePipe := &ir.Pipeline{
		Source:   pipe.Source,
		Stages:   pipe.Stages[:splitIdx],
		Position: pipe.Position,
	}
	q, err := bk.Emit(prePipe)
	if err != nil {
		return nil, true, fmt.Errorf("emit (arrow pre-postproc): %w", err)
	}
	reader, err := ab.ExecArrow(ctx, q)
	if err != nil {
		return nil, true, fmt.Errorf("exec arrow (pre-postproc): %w", err)
	}
	defer reader.Release()

	// Drain Arrow → rows for PostProc.
	result := drainArrowReader(reader)
	for _, st := range pipe.Stages[splitIdx:] {
		result, err = applyPostProc(result, st)
		if err != nil {
			return nil, true, fmt.Errorf("postproc %T: %w", st, err)
		}
	}
	return result, true, nil
}

// drainArrowReader reads all RecordBatches from a RecordReader and converts
// them to row-based Result. Values are extracted while each RecordBatch is
// alive (DuckDB's C Data Interface may reuse buffers across Next() calls).
func drainArrowReader(reader array.RecordReader) *Result {
	if reader == nil {
		return &Result{}
	}

	var columns []string
	var rows [][]interface{}

	for reader.Next() {
		rec := reader.Record()
		// CRITICAL: DuckDB's Arrow C Data Interface reuses internal buffers
		// (especially string data buffers) across Next() calls. We MUST
		// Retain the RecordBatch to prevent the next Next() from overwriting
		// our data before we finish extracting values.
		rec.Retain()

		// Extract column names from the first batch's schema.
		if columns == nil && rec.Schema() != nil {
			for _, f := range rec.Schema().Fields() {
				columns = append(columns, f.Name)
			}
		}
		// Extract all rows from this batch immediately (before Release).
		nrows := int(rec.NumRows())
		ncols := int(rec.NumCols())
		for r := 0; r < nrows; r++ {
			row := make([]interface{}, ncols)
			for c := 0; c < ncols; c++ {
				row[c] = arrowValue(rec.Column(c), r)
			}
			rows = append(rows, row)
		}

		rec.Release() // safe to release now — all values extracted
	}

	if columns == nil {
		columns = []string{}
	}
	return &Result{Columns: columns, Rows: rows}
}

// arrowValue extracts a Go scalar from an Arrow array at a given row index.
// Handles all common types DuckDB/pg produce, with null checking.
func arrowValue(arr arrow.Array, i int) interface{} {
	if arr.IsNull(i) {
		return nil
	}
	switch a := arr.(type) {
	case *array.Int64:
		return a.Value(i)
	case *array.Int32:
		return int64(a.Value(i))
	case *array.Int16:
		return int64(a.Value(i))
	case *array.Int8:
		return int64(a.Value(i))
	case *array.Uint32:
		return int64(a.Value(i))
	case *array.Float64:
		return a.Value(i)
	case *array.Float32:
		return float64(a.Value(i))
	case *array.String:
		return a.Value(i)
	case *array.LargeString:
		return a.Value(i)
	case *array.Boolean:
		return a.Value(i)
	case *array.Binary:
		return string(a.Value(i))
	case *array.LargeBinary:
		return string(a.Value(i))
	case *array.Timestamp:
		return a.Value(i)
	}
	return nil // unknown type → nil (safe)
}

// keep columnar import alive (used in future Arrow-native PostProc).
var _ = columnar.KindInt64

// init registers the Arrow execution hook. This runs only when built with
// -tags duckdb_arrow. Without the tag, arrowExecHook stays nil and ExecPipeline
// uses the row path.
func init() {
	arrowExecHook = tryArrowExec
}
