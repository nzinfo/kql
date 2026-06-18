package ir

import (
	"reflect"
	"strings"
	"testing"

	"nzinfo/kql/internal/frontend/token"
)

// Compile-time interface assertions (I1.S1/S2 acceptance): every concrete node
// satisfies the interface for its kind.
var (
	_ Node = (*Pipeline)(nil)
	_ Node = (*SourceTable)(nil)
	_ Node = (*SourceDatatable)(nil)
	_ Node = (*SourcePrint)(nil)
	_ Node = (*SourceRange)(nil)

	_ Source = (*SourceTable)(nil)
	_ Source = (*SourceDatatable)(nil)
	_ Source = (*SourcePrint)(nil)
	_ Source = (*SourceRange)(nil)

	_ Stage = (*Filter)(nil)
	_ Stage = (*Project)(nil)
	_ Stage = (*Extend)(nil)
	_ Stage = (*Aggregate)(nil)
	_ Stage = (*Join)(nil)
	_ Stage = (*Sort)(nil)
	_ Stage = (*Limit)(nil)
	_ Stage = (*Union)(nil)
	_ Stage = (*Distinct)(nil)

	_ Expr = (*Lit)(nil)
	_ Expr = (*Col)(nil)
	_ Expr = (*Star)(nil)
	_ Expr = (*BinOp)(nil)
	_ Expr = (*UnaryOp)(nil)
	_ Expr = (*FuncCall)(nil)
	_ Expr = (*Member)(nil)
	_ Expr = (*Index)(nil)
	_ Expr = (*Case)(nil)
)

// TestBuildRepresentativePipeline constructs a pipeline mirroring the F4
// acceptance query and checks field access + types compile & work:
//
//	orders | where id > 100 | take 10
func TestBuildRepresentativePipeline(t *testing.T) {
	src := &SourceTable{
		Position: token.Pos(1),
		Table:    "orders",
		Columns: []Column{
			{ColID: 1, Name: "id", Type: TypeLong},
		},
	}
	// where id > 100
	idCol := &Col{Position: token.Pos(15), ColID: 1, Name: "id", T: TypeLong}
	hundred := &Lit{Position: token.Pos(20), T: TypeLong, Value: int64(100), HasValue: true}
	pred := &BinOp{Position: token.Pos(17), Op: token.GTR, X: idCol, Y: hundred, T: TypeBool}
	filter := &Filter{Position: token.Pos(12), Predicate: pred}

	// take 10
	ten := &Lit{Position: token.Pos(30), T: TypeLong, Value: int64(10), HasValue: true}
	limit := &Limit{Position: token.Pos(28), Count: ten}

	pipe := &Pipeline{
		Source:   src,
		Stages:   []Stage{filter, limit},
		Position: token.Pos(1),
	}
	if got := len(pipe.Stages); got != 2 {
		t.Errorf("stages = %d, want 2", got)
	}
	if f, ok := pipe.Stages[0].(*Filter); !ok {
		t.Errorf("stage0 = %T, want *Filter", pipe.Stages[0])
	} else if f.Predicate.Type() != TypeBool {
		t.Errorf("predicate type = %v, want bool", f.Predicate.Type())
	}
}

// TestColIDValidity
func TestColIDValidity(t *testing.T) {
	if Invalid.IsValid() {
		t.Error("Invalid ColID should not be valid")
	}
	if !ColID(1).IsValid() {
		t.Error("ColID(1) should be valid")
	}
}

// TestTypeStrings
func TestTypeStrings(t *testing.T) {
	cases := map[Type]string{
		TypeBool:     "bool",
		TypeLong:     "long",
		TypeReal:     "real",
		TypeDecimal:  "decimal",
		TypeString:   "string",
		TypeDateTime: "datetime",
		TypeTimeSpan: "timespan",
		TypeDynamic:  "dynamic",
		TypeUnknown:  "unknown",
	}
	for ty, want := range cases {
		if got := ty.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", ty, got, want)
		}
	}
}

// TestTypeIsNumeric
func TestTypeIsNumeric(t *testing.T) {
	numeric := []Type{TypeInt, TypeLong, TypeReal, TypeDecimal, TypeTimeSpan}
	for _, ty := range numeric {
		if !ty.IsNumeric() {
			t.Errorf("%v should be numeric", ty)
		}
	}
	nonNumeric := []Type{TypeBool, TypeString, TypeDateTime, TypeDynamic, TypeUnknown}
	for _, ty := range nonNumeric {
		if ty.IsNumeric() {
			t.Errorf("%v should NOT be numeric", ty)
		}
	}
}

// TestLitNullRepresentation: HasValue=false is the null literal (I1.S2).
func TestLitNullRepresentation(t *testing.T) {
	null := &Lit{T: TypeUnknown, HasValue: false}
	if null.HasValue {
		t.Error("null literal should have HasValue=false")
	}
	five := &Lit{T: TypeLong, Value: int64(5), HasValue: true}
	if !five.HasValue {
		t.Error("non-null literal should have HasValue=true")
	}
}

// TestCapsDefaults (I1.S4): default caps are reasonable until F7 lands.
func TestCapsDefaults(t *testing.T) {
	scalarCaps := DefaultCaps("abs", false)
	if !scalarCaps.SQLExpr || scalarCaps.Aggregate {
		t.Errorf("scalar default caps = %+v, want SQLExpr=true Aggregate=false", scalarCaps)
	}
	aggCaps := DefaultCaps("count", true)
	if !aggCaps.Aggregate || !aggCaps.SQLExpr {
		t.Errorf("aggregate default caps = %+v, want Aggregate=true SQLExpr=true", aggCaps)
	}
}

// TestVisitorCoverage: Walk reaches every node type in a representative tree.
type countingVisitor struct{ seen []string }

func (c *countingVisitor) Visit(n Node) Visitor {
	if n == nil {
		return nil
	}
	c.seen = append(c.seen, reflect.TypeOf(n).Elem().Name())
	return c
}

func TestVisitorCoverage(t *testing.T) {
	// orders | where id > 100 | extend d = id * 2
	pipe := &Pipeline{
		Source: &SourceTable{Table: "orders"},
		Stages: []Stage{
			&Filter{Predicate: &BinOp{
				Op: token.GTR,
				X:  &Col{Name: "id"},
				Y:  &Lit{Value: int64(100), HasValue: true},
			}},
			&Extend{Cols: []*NamedExpr{
				{Name: "d", Expr: &BinOp{
					Op: token.MUL,
					X:  &Col{Name: "id"},
					Y:  &Lit{Value: int64(2), HasValue: true},
				}},
			}},
		},
	}
	cv := &countingVisitor{}
	Walk(cv, pipe)
	want := map[string]bool{
		"Pipeline": true, "SourceTable": true, "Filter": true, "Extend": true,
		"BinOp": true, "Col": true, "Lit": true, "NamedExpr": true,
	}
	got := map[string]bool{}
	for _, n := range cv.seen {
		got[n] = true
	}
	for w := range want {
		if !got[w] {
			t.Errorf("Walk missed node type %q (visited: %v)", w, cv.seen)
		}
	}
}

// TestBaseVisitorNoPanics: BaseVisitor dispatches every node type.
func TestBaseVisitorNoPanics(t *testing.T) {
	bv := &BaseVisitor{}
	nodes := []Node{
		&Pipeline{}, &SourceTable{}, &SourceDatatable{}, &SourcePrint{}, &SourceRange{},
		&Filter{}, &Project{}, &Extend{}, &Aggregate{}, &Join{}, &Sort{}, &Limit{},
		&Union{}, &Distinct{}, &NamedExpr{}, &Lit{}, &Col{}, &Star{}, &BinOp{},
		&UnaryOp{}, &FuncCall{}, &Member{}, &Index{}, &Case{},
	}
	for _, n := range nodes {
		Walk(bv, n) // must not panic
	}
}

// TestJoinKindValues distinct.
func TestJoinKindValues(t *testing.T) {
	kinds := []JoinKind{JoinDefault, JoinInnerUnique, JoinInner, JoinLeftOuter, JoinRightOuter, JoinFullOuter}
	seen := map[JoinKind]bool{}
	for _, k := range kinds {
		if seen[k] {
			t.Errorf("JoinKind %d duplicated", k)
		}
		seen[k] = true
	}
}

// TestJoinHintValues confirms the JoinHint enum is distinct + zero-value is
// JoinHintNone (the no-regression guarantee: an unset Hint = "let backend
// decide" = current behaviour).
func TestJoinHintValues(t *testing.T) {
	hints := []JoinHint{JoinHintNone, JoinHintHash, JoinHintNestLoop, JoinHintMerge, JoinHintIndexLookup}
	if JoinHintNone != 0 {
		t.Errorf("JoinHintNone = %d, want 0 (zero-value must be the safe default)", JoinHintNone)
	}
	seen := map[JoinHint]bool{}
	for _, h := range hints {
		if seen[h] {
			t.Errorf("JoinHint %d duplicated", h)
		}
		seen[h] = true
	}
}

// TestJoinHintString confirms the String() rendering used in Explain + emit.
func TestJoinHintString(t *testing.T) {
	cases := map[JoinHint]string{
		JoinHintNone:        "none",
		JoinHintHash:        "hash",
		JoinHintNestLoop:    "nestloop",
		JoinHintMerge:       "merge",
		JoinHintIndexLookup: "indexlookup",
	}
	for h, want := range cases {
		if got := h.String(); got != want {
			t.Errorf("JoinHint(%d).String() = %q, want %q", h, got, want)
		}
	}
}


// TestPrint_SimplePipeline verifies the library-level IR pretty-printer
// produces the expected tree shape (I4.S1).
func TestPrint_SimplePipeline(t *testing.T) {
	pipe := &Pipeline{
		Source: &SourceTable{Table: "events"},
		Stages: []Stage{
			&Filter{Predicate: &BinOp{
				Op: token.EQL,
				X:  &Col{Name: "state"},
				Y:  &Lit{Value: "TX", HasValue: true, T: TypeString},
			}},
			&Limit{Count: &Lit{Value: int64(10), HasValue: true, T: TypeLong}},
		},
	}
	out := Sprint(pipe)
	// Should mention Pipeline, Source table, Filter, Limit.
	for _, want := range []string{"Pipeline", `Table "events"`, "Filter", "Limit", "state", "take"} {
		if !strings.Contains(out, want) {
			t.Errorf("Print output missing %q:\n%s", want, out)
		}
	}
}

// TestPrint_JoinWithHint verifies the Join line includes the hint (O4).
func TestPrint_JoinWithHint(t *testing.T) {
	pipe := &Pipeline{
		Source: &SourceTable{Table: "T1"},
		Stages: []Stage{
			&Join{
				Kind: JoinInner,
				Hint: JoinHintHash,
				Right: &Pipeline{Source: &SourceTable{Table: "T2"}},
				On: []Expr{&BinOp{Op: token.EQL, X: &Col{Name: "a"}, Y: &Col{Name: "b"}}},
			},
		},
	}
	out := Sprint(pipe)
	if !strings.Contains(out, "hint=hash") {
		t.Errorf("Print should show hint=hash for hinted join:\n%s", out)
	}
}

// TestPrint_NilPipeline doesn't panic.
func TestPrint_NilPipeline(t *testing.T) {
	out := Sprint(nil)
	if !strings.Contains(out, "nil") {
		t.Errorf("nil pipeline should mention nil: %q", out)
	}
}


// --- I5 IR equivalence tests ---

// TestEquiv_ReorderedPredicates: `where a > 1 and b < 2` == `where b < 2 and a > 1`.
func TestEquiv_ReorderedPredicates(t *testing.T) {
	mkPipe := func(first, second Expr) *Pipeline {
		return &Pipeline{
			Source: &SourceTable{Table: "T"},
			Stages: []Stage{
				&Filter{Predicate: &BinOp{Op: token.AND, X: first, Y: second}},
			},
		}
	}
	a := &BinOp{Op: token.GTR, X: &Col{Name: "a"}, Y: &Lit{Value: int64(1), HasValue: true, T: TypeLong}}
	b := &BinOp{Op: token.LSS, X: &Col{Name: "b"}, Y: &Lit{Value: int64(2), HasValue: true, T: TypeLong}}
	pipeA := mkPipe(a, b)
	pipeB := mkPipe(b, a) // reordered
	if !Equivalent(pipeA, pipeB) {
		t.Error("reordered AND predicates should be equivalent")
	}
}

// TestEquiv_DifferentPredicates: `where a > 1` != `where a > 2`.
func TestEquiv_DifferentPredicates(t *testing.T) {
	mkPipe := func(val int64) *Pipeline {
		return &Pipeline{
			Source: &SourceTable{Table: "T"},
			Stages: []Stage{
				&Filter{Predicate: &BinOp{Op: token.GTR, X: &Col{Name: "a"}, Y: &Lit{Value: val, HasValue: true, T: TypeLong}}},
			},
		}
	}
	if Equivalent(mkPipe(1), mkPipe(2)) {
		t.Error("different predicates should NOT be equivalent")
	}
}

// TestEquiv_SamePipeline: identical pipelines are equivalent.
func TestEquiv_SamePipeline(t *testing.T) {
	pipe := &Pipeline{
		Source: &SourceTable{Table: "T"},
		Stages: []Stage{
			&Filter{Predicate: &Col{Name: "x"}},
			&Limit{Count: &Lit{Value: int64(10), HasValue: true, T: TypeLong}},
		},
	}
	if !Equivalent(pipe, pipe) {
		t.Error("identical pipelines should be equivalent")
	}
}

// TestEquiv_DifferentSource: different tables are NOT equivalent.
func TestEquiv_DifferentSource(t *testing.T) {
	pipeA := &Pipeline{Source: &SourceTable{Table: "T1"}}
	pipeB := &Pipeline{Source: &SourceTable{Table: "T2"}}
	if Equivalent(pipeA, pipeB) {
		t.Error("different source tables should NOT be equivalent")
	}
}

// TestEquiv_SortKeysCanonicalized: `sort by a, b` == `sort by a, b` (same order).
func TestEquiv_SortKeysCanonicalized(t *testing.T) {
	mkPipe := func() *Pipeline {
		return &Pipeline{
			Source: &SourceTable{Table: "T"},
			Stages: []Stage{
				&Sort{Keys: []SortKey{
					{Expr: &Col{Name: "a"}, Desc: false},
					{Expr: &Col{Name: "b"}, Desc: true},
				}},
			},
		}
	}
	if !Equivalent(mkPipe(), mkPipe()) {
		t.Error("same sort should be equivalent")
	}
}

// TestEquiv_NilPipelines: nil == nil is true.
func TestEquiv_NilPipelines(t *testing.T) {
	if !Equivalent(nil, nil) {
		t.Error("nil == nil should be true")
	}
	if Equivalent(nil, &Pipeline{}) {
		t.Error("nil != non-nil should be false")
	}
}

// TestCanonicalize_ProducesString: canonicalize produces a stable string.
func TestCanonicalize_ProducesString(t *testing.T) {
	pipe := &Pipeline{
		Source: &SourceTable{Table: "events"},
		Stages: []Stage{
			&Filter{Predicate: &Col{Name: "x"}},
		},
	}
	cp := Canonicalize(pipe)
	s := canonicalString(cp)
	if !strings.Contains(s, "events") || !strings.Contains(s, "filter") {
		t.Errorf("canonical string missing expected parts: %q", s)
	}
}
