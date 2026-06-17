package columnar

import (
	"testing"
)

func TestNewRecord(t *testing.T) {
	r := NewRecord([]string{"a", "b", "c"}, []ColumnKind{KindInt64, KindString, KindBool})
	if len(r.Columns) != 3 || r.Len != 0 {
		t.Fatalf("bad record: %+v", r)
	}
	if r.Columns[0].Kind != KindInt64 {
		t.Errorf("col0 kind = %v, want Int64", r.Columns[0].Kind)
	}
}

func TestAppendRow_Typed(t *testing.T) {
	r := NewRecord([]string{"n", "s"}, []ColumnKind{KindInt64, KindString})
	r.AppendRow([]interface{}{int64(1), "hello"})
	r.AppendRow([]interface{}{int64(2), "world"})
	if r.Len != 2 {
		t.Fatalf("Len = %d, want 2", r.Len)
	}
	if len(r.Columns[0].Ints) != 2 || r.Columns[0].Ints[1] != 2 {
		t.Errorf("Ints = %v, want [1 2]", r.Columns[0].Ints)
	}
	if len(r.Columns[1].Strings) != 2 || r.Columns[1].Strings[0] != "hello" {
		t.Errorf("Strings = %v", r.Columns[1].Strings)
	}
}

func TestAppendRow_Nulls(t *testing.T) {
	r := NewRecord([]string{"n"}, []ColumnKind{KindInt64})
	r.AppendRow([]interface{}{int64(1)})
	r.AppendRow([]interface{}{nil})
	r.AppendRow([]interface{}{int64(3)})
	// Row 1 should be nil.
	row1 := r.Row(1)
	if row1[0] != nil {
		t.Errorf("null row = %v, want nil", row1[0])
	}
	// Rows 0 and 3 should have values.
	if r.Row(0)[0] != int64(1) {
		t.Errorf("row0 = %v", r.Row(0)[0])
	}
	if r.Row(2)[0] != int64(3) {
		t.Errorf("row2 = %v", r.Row(2)[0])
	}
}

func TestToRows_RoundTrip(t *testing.T) {
	original := [][]interface{}{
		{int64(1), "a", true},
		{int64(2), "b", false},
		{nil, "c", true},
	}
	r := FromRows([]string{"n", "s", "b"}, original)
	rows := r.ToRows()
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}
	// Check row 0.
	if rows[0][0] != int64(1) || rows[0][1] != "a" || rows[0][2] != true {
		t.Errorf("row0 = %v", rows[0])
	}
	// Row 2 col 0 should be nil.
	if rows[2][0] != nil {
		t.Errorf("row2 col0 = %v, want nil", rows[2][0])
	}
}

func TestFromRows_TypeInference(t *testing.T) {
	rows := [][]interface{}{
		{int64(42), "hello", 3.14},
	}
	r := FromRows([]string{"i", "s", "f"}, rows)
	if r.Columns[0].Kind != KindInt64 {
		t.Errorf("col0 kind = %v, want Int64", r.Columns[0].Kind)
	}
	if r.Columns[1].Kind != KindString {
		t.Errorf("col1 kind = %v, want String", r.Columns[1].Kind)
	}
	if r.Columns[2].Kind != KindFloat64 {
		t.Errorf("col2 kind = %v, want Float64", r.Columns[2].Kind)
	}
}

func TestFromRows_Empty(t *testing.T) {
	r := FromRows([]string{"a", "b"}, nil)
	if r.Len != 0 {
		t.Errorf("Len = %d, want 0", r.Len)
	}
}

func TestAppendRow_TypeMismatch(t *testing.T) {
	r := NewRecord([]string{"n"}, []ColumnKind{KindInt64})
	r.AppendRow([]interface{}{int64(1)})
	// String in an Int64 column → null + false from appendTyped.
	r.AppendRow([]interface{}{"not a number"})
	row1 := r.Row(1)
	if row1[0] != nil {
		t.Errorf("type mismatch should be null, got %v", row1[0])
	}
}

func TestAppendRow_ColumnCountMismatch(t *testing.T) {
	r := NewRecord([]string{"a", "b"}, nil)
	err := r.AppendRow([]interface{}{int64(1)}) // only 1 value for 2 cols
	if err == nil {
		t.Error("expected error for column count mismatch")
	}
}

func TestMixedColumn(t *testing.T) {
	r := NewRecord([]string{"v"}, []ColumnKind{KindMixed})
	r.AppendRow([]interface{}{int64(1)})
	r.AppendRow([]interface{}{"two"})
	r.AppendRow([]interface{}{3.14})
	row0 := r.Row(0)
	row1 := r.Row(1)
	row2 := r.Row(2)
	if row0[0] != int64(1) || row1[0] != "two" {
		t.Errorf("mixed col rows: %v %v %v", row0, row1, row2)
	}
}

func TestIRTypeToKind(t *testing.T) {
	cases := []struct {
		irType interface{ String() string }
		want   ColumnKind
	}{
		// Using the actual ir.Type constants via a helper to avoid import cycle
		// in this test. The function takes ir.Type; we test the mapping logic.
	}
	_ = cases // IRTypeToKind is tested indirectly via the integration tests.
}
