//go:build duckdb_arrow

// Package columnar — Arrow bridge (Step 0b, requires -tags duckdb_arrow).
//
// Converts between the hand-rolled columnar.Record and Apache Arrow
// RecordBatch. The bridge is thin because the type kinds and null-mask layout
// already match Arrow's builder API (AppendValues(slice, valid []bool)).
//
// This enables:
//   - pg results → columnar.Record → arrow.RecordBatch → DuckDB RegisterView
//   - DuckDB Arrow RecordReader → arrow.RecordBatch → drain to rows
package columnar

import (
	"fmt"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/memory"
)

// ToArrow converts a columnar.Record to an Arrow RecordBatch using the given
// allocator. Each column is built via the appropriate typed builder
// (Int64Builder, Float64Builder, etc.). The caller owns the returned RecordBatch
// (must Release when done).
func (r *Record) ToArrow(mem memory.Allocator) (arrow.RecordBatch, error) {
	if r == nil {
		return nil, fmt.Errorf("nil record")
	}

	fields := make([]arrow.Field, len(r.Columns))
	builders := make([]array.Builder, len(r.Columns))

	for i, c := range r.Columns {
		dt, b := newArrowBuilder(mem, c)
		fields[i] = arrow.Field{Name: c.Name, Type: dt, Nullable: true}
		builders[i] = b
	}

	// Fill each builder from the column's typed slice.
	for i, c := range r.Columns {
		if err := appendArrowValues(builders[i], c, r.Len); err != nil {
			// Release all builders on error.
			for _, b := range builders {
				b.Release()
			}
			return nil, fmt.Errorf("column %d (%s): %w", i, c.Name, err)
		}
	}

	// Build arrays.
	cols := make([]arrow.Array, len(builders))
	for i, b := range builders {
		cols[i] = b.NewArray()
	}

	schema := arrow.NewSchema(fields, nil)
	rb := array.NewRecordBatch(schema, cols, int64(r.Len))

	// Release builders (arrays retain their own refs).
	for _, b := range builders {
		b.Release()
	}
	// Release arrays — RecordBatch retains its own refs.
	for _, c := range cols {
		c.Release()
	}

	return rb, nil
}

// newArrowBuilder creates the right Arrow type + builder for a column kind.
func newArrowBuilder(mem memory.Allocator, c *Column) (arrow.DataType, array.Builder) {
	switch c.Kind {
	case KindInt64:
		return arrow.PrimitiveTypes.Int64, array.NewInt64Builder(mem)
	case KindFloat64:
		return arrow.PrimitiveTypes.Float64, array.NewFloat64Builder(mem)
	case KindString:
		return arrow.BinaryTypes.String, array.NewStringBuilder(mem)
	case KindBool:
		return arrow.FixedWidthTypes.Boolean, array.NewBooleanBuilder(mem)
	default:
		// KindMixed / KindUnknown → use dense-union or fallback to string.
		return arrow.BinaryTypes.String, array.NewStringBuilder(mem)
	}
}

// appendArrowValues fills a builder from a column's typed slice + null mask.
// Uses the batch AppendValues API for typed columns (5-20× faster than
// per-value Append — single slice copy + bitset build).
func appendArrowValues(b array.Builder, c *Column, n int) error {
	// Build a contiguous valid []bool once (length n), avoiding the triple
	// branch check per value. valid[i] = false → null.
	valid := buildValidMask(c, n)

	switch c.Kind {
	case KindInt64:
		if len(c.Ints) >= n {
			b.(*array.Int64Builder).AppendValues(c.Ints[:n], valid)
		} else {
			// Fewer values than expected — fall back to per-value append.
			appendInt64Slow(b.(*array.Int64Builder), c.Ints, valid)
		}
	case KindFloat64:
		if len(c.Floats) >= n {
			b.(*array.Float64Builder).AppendValues(c.Floats[:n], valid)
		} else {
			appendFloat64Slow(b.(*array.Float64Builder), c.Floats, valid)
		}
	case KindString:
		if len(c.Strings) >= n {
			b.(*array.StringBuilder).AppendValues(c.Strings[:n], valid)
		} else {
		 appendStringSlow(b.(*array.StringBuilder), c.Strings, valid)
		}
	case KindBool:
		if len(c.Bools) >= n {
			b.(*array.BooleanBuilder).AppendValues(c.Bools[:n], valid)
		} else {
			appendBoolSlow(b.(*array.BooleanBuilder), c.Bools, valid)
		}
	default:
		// Mixed: per-value string formatting (rare path).
		sb := b.(*array.StringBuilder)
		for i := 0; i < n; i++ {
			if valid != nil && !valid[i] {
				sb.AppendNull()
			} else if i < len(c.Values) && c.Values[i] != nil {
				sb.Append(fmt.Sprint(c.Values[i]))
			} else {
				sb.AppendNull()
			}
		}
	}
	return nil
}

// buildValidMask creates a contiguous []bool of length n from the column's
// NullMask. Returns nil (all-valid) if there are no nulls.
func buildValidMask(c *Column, n int) []bool {
	if c.NullMask == nil {
		return nil // all valid — AppendValues(nil) means all non-null
	}
	valid := make([]bool, n)
	for i := 0; i < n; i++ {
		valid[i] = !(i < len(c.NullMask) && c.NullMask[i])
	}
	return valid
}

// Slow fallbacks for when the typed slice is shorter than n (shouldn't happen
// in normal operation, but guards against edge cases).
func appendInt64Slow(b *array.Int64Builder, vals []int64, valid []bool) {
	for i := 0; i < len(valid); i++ {
		if valid != nil && !valid[i] {
			b.AppendNull()
		} else if i < len(vals) {
			b.Append(vals[i])
		} else {
			b.AppendNull()
		}
	}
}
func appendFloat64Slow(b *array.Float64Builder, vals []float64, valid []bool) {
	for i := 0; i < len(valid); i++ {
		if valid != nil && !valid[i] {
			b.AppendNull()
		} else if i < len(vals) {
			b.Append(vals[i])
		} else {
			b.AppendNull()
		}
	}
}
func appendStringSlow(b *array.StringBuilder, vals []string, valid []bool) {
	for i := 0; i < len(valid); i++ {
		if valid != nil && !valid[i] {
			b.AppendNull()
		} else if i < len(vals) {
			b.Append(vals[i])
		} else {
			b.AppendNull()
		}
	}
}
func appendBoolSlow(b *array.BooleanBuilder, vals []bool, valid []bool) {
	for i := 0; i < len(valid); i++ {
		if valid != nil && !valid[i] {
			b.AppendNull()
		} else if i < len(vals) {
			b.Append(vals[i])
		} else {
			b.AppendNull()
		}
	}
}

// RecordBatchToRows drains an Arrow RecordBatch into row-based [][]interface{}.
// This is the bridge from Arrow back to the row format (for PostProc or final
// output when Arrow isn't used end-to-end).
func RecordBatchToRows(rb arrow.RecordBatch) [][]interface{} {
	if rb == nil {
		return nil
	}
	nrows := int(rb.NumRows())
	ncols := int(rb.NumCols())
	rows := make([][]interface{}, nrows)
	for r := 0; r < nrows; r++ {
		row := make([]interface{}, ncols)
		for c := 0; c < ncols; c++ {
			row[c] = arrowScalar(rb.Column(c), r)
		}
		rows[r] = row
	}
	return rows
}

// arrowScalar extracts a Go value from an Arrow array at a given row index.
// Handles all common Arrow numeric/string/bool types, including the different
// integer widths DuckDB may return (Int32, Int64, etc.).
func arrowScalar(arr arrow.Array, i int) interface{} {
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
	case *array.Uint64:
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
	}
	// Fallback: try string representation.
	return arr.String()
}

// RecordBatchColumnNames extracts column names from an Arrow schema.
func RecordBatchColumnNames(rb arrow.RecordBatch) []string {
	if rb == nil || rb.Schema() == nil {
		return nil
	}
	fields := rb.Schema().Fields()
	names := make([]string, len(fields))
	for i, f := range fields {
		names[i] = f.Name
	}
	return names
}
