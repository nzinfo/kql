// Package columnar provides a type-aware columnar result format (DESIGN §0).
//
// The row-based `[][]interface{}` format boxes every value in an interface{},
// which is memory-inefficient for large result sets (each int64/float64/bool
// allocates separately on the heap). This package offers a typed columnar
// alternative: each column is a typed slice (Int64s, Float64s, Strings, Bools,
// or a generic Values for mixed/null-heavy columns), stored contiguously.
//
// Backends can optionally produce a *Record instead of [][]interface{}; the
// ToRows/FromRows helpers bridge the two formats. This is the first step toward
// the DESIGN §0 columnar promise — a future migration will have backends emit
// Record directly, and Arrow IPC serialization can layer on top.
package columnar

import (
	"fmt"
	"time"

	"nzinfo/kql/internal/ir"
)

// ColumnKind classifies a column's storage type for compact representation.
type ColumnKind int

const (
	KindUnknown ColumnKind = iota
	KindInt64
	KindFloat64
	KindString
	KindBool
	KindMixed // mixed types or many nulls — fall back to []interface{}
)

// Column is one typed column in a Record.
type Column struct {
	Name    string
	Kind    ColumnKind
	Ints    []int64
	Floats  []float64
	Strings []string
	Bools   []bool
	Values  []interface{} // used when Kind == KindMixed
	NullMask []bool       // NullMask[i] = true → row i is NULL
}

// Record is a columnar result set: Columns × Len rows. This is the columnar
// counterpart to backend.Result{Rows [][]interface{}}.
type Record struct {
	Columns []*Column
	Len     int // number of rows (all columns have this length)
}

// NewRecord creates an empty Record with the given column names and kinds.
func NewRecord(names []string, kinds []ColumnKind) *Record {
	cols := make([]*Column, len(names))
	for i, n := range names {
		k := KindUnknown
		if i < len(kinds) {
			k = kinds[i]
		}
		cols[i] = &Column{Name: n, Kind: k}
	}
	return &Record{Columns: cols}
}

// AppendRow adds one row of values to all columns, inferring/storing each value
// in the column's typed slice. For typed columns, mismatched types fall back
// that cell to NULL and set the NullMask. This is the primary builder method.
func (r *Record) AppendRow(values []interface{}) error {
	if len(values) != len(r.Columns) {
		return fmt.Errorf("columnar: %d values for %d columns", len(values), len(r.Columns))
	}
	for i, v := range values {
		c := r.Columns[i]
		if v == nil {
			c.NullMask = ensureNullMask(c, r.Len)
			c.NullMask[r.Len] = true
			// Still append a zero value to keep the slice length aligned.
			appendZero(c)
			continue
		}
		if !appendTyped(c, r.Len, v) {
			// Type mismatch in a typed column → mark null + warn via fallback.
			c.NullMask = ensureNullMask(c, r.Len)
			c.NullMask[r.Len] = true
			appendZero(c)
		}
	}
	r.Len++
	return nil
}

// Row returns row i as a []interface{} (materializing typed values back into
// interface{} boxes). This is the conversion to the row-based format.
func (r *Record) Row(i int) []interface{} {
	row := make([]interface{}, len(r.Columns))
	for j, c := range r.Columns {
		if c.NullMask != nil && i < len(c.NullMask) && c.NullMask[i] {
			row[j] = nil
			continue
		}
		switch c.Kind {
		case KindInt64:
			if i < len(c.Ints) {
				row[j] = c.Ints[i]
			}
		case KindFloat64:
			if i < len(c.Floats) {
				row[j] = c.Floats[i]
			}
		case KindString:
			if i < len(c.Strings) {
				row[j] = c.Strings[i]
			}
		case KindBool:
			if i < len(c.Bools) {
				row[j] = c.Bools[i]
			}
		case KindMixed, KindUnknown:
			if i < len(c.Values) {
				row[j] = c.Values[i]
			}
		}
	}
	return row
}

// ToRows converts the entire Record to row-based [][]interface{}.
func (r *Record) ToRows() [][]interface{} {
	rows := make([][]interface{}, r.Len)
	for i := 0; i < r.Len; i++ {
		rows[i] = r.Row(i)
	}
	return rows
}

// FromRows builds a Record from row-based data, inferring each column's type
// from the first non-nil value. If a column has mixed types, it stays KindMixed.
func FromRows(colNames []string, rows [][]interface{}) *Record {
	if len(rows) == 0 {
		return NewRecord(colNames, nil)
	}
	ncols := len(colNames)
	kinds := make([]ColumnKind, ncols)
	// Infer kinds from the first non-nil value in each column.
	for j := 0; j < ncols; j++ {
		kinds[j] = KindMixed
		for _, row := range rows {
			if j >= len(row) {
				continue
			}
			if row[j] != nil {
				kinds[j] = inferKind(row[j])
				break
			}
		}
	}
	rec := NewRecord(colNames, kinds)
	for _, row := range rows {
		rec.AppendRow(row)
	}
	return rec
}

// inferKind maps a Go value to a ColumnKind.
func inferKind(v interface{}) ColumnKind {
	switch v.(type) {
	case int, int32, int64:
		return KindInt64
	case float32, float64:
		return KindFloat64
	case string:
		return KindString
	case bool:
		return KindBool
	case time.Time:
		return KindString // datetime serialized as string (ISO format from SQL)
	default:
		return KindMixed
	}
}

// appendTyped stores v in column c's typed slice. Returns false if the type
// doesn't match the column's Kind (caller marks null on mismatch).
func appendTyped(c *Column, idx int, v interface{}) bool {
	switch c.Kind {
	case KindInt64:
		n, ok := toInt64(v)
		if !ok {
			return false
		}
		c.Ints = append(c.Ints, n)
		return true
	case KindFloat64:
		f, ok := toFloat64(v)
		if !ok {
			return false
		}
		c.Floats = append(c.Floats, f)
		return true
	case KindString:
		s, ok := v.(string)
		if !ok {
			return false
		}
		c.Strings = append(c.Strings, s)
		return true
	case KindBool:
		b, ok := v.(bool)
		if !ok {
			return false
		}
		c.Bools = append(c.Bools, b)
		return true
	case KindMixed, KindUnknown:
		c.Kind = KindMixed
		c.Values = append(c.Values, v)
		return true
	}
	return false
}

// appendZero appends a zero value to keep the column slice aligned when a cell
// is null.
func appendZero(c *Column) {
	switch c.Kind {
	case KindInt64:
		c.Ints = append(c.Ints, 0)
	case KindFloat64:
		c.Floats = append(c.Floats, 0)
	case KindString:
		c.Strings = append(c.Strings, "")
	case KindBool:
		c.Bools = append(c.Bools, false)
	case KindMixed, KindUnknown:
		c.Values = append(c.Values, nil)
	}
}

func ensureNullMask(c *Column, idx int) []bool {
	if c.NullMask == nil {
		c.NullMask = make([]bool, idx+1)
	}
	for len(c.NullMask) <= idx {
		c.NullMask = append(c.NullMask, false)
	}
	return c.NullMask
}

func toInt64(v interface{}) (int64, bool) {
	switch x := v.(type) {
	case int:
		return int64(x), true
	case int32:
		return int64(x), true
	case int64:
		return x, true
	}
	return 0, false
}

func toFloat64(v interface{}) (float64, bool) {
	switch x := v.(type) {
	case float32:
		return float64(x), true
	case float64:
		return x, true
	}
	return 0, false
}

// IRTypeToKind maps an ir.Type to the best-fit ColumnKind for storage.
func IRTypeToKind(t ir.Type) ColumnKind {
	switch t {
	case ir.TypeInt, ir.TypeLong:
		return KindInt64
	case ir.TypeReal, ir.TypeDecimal:
		return KindFloat64
	case ir.TypeString, ir.TypeDateTime, ir.TypeTimeSpan:
		return KindString
	case ir.TypeBool:
		return KindBool
	}
	return KindMixed
}
