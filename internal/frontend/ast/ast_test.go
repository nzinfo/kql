package ast

import (
	"reflect"
	"testing"

	"nzinfo/kql/internal/frontend/token"
)

// --- Compile-time interface assertions (F2.S1 acceptance) ---
// Each node type must satisfy the interface for its kind. These are compile-
// time checks; if a node loses a marker method the package fails to build.
var (
	_ Node = (*Bad)(nil)
	_ Node = (*Comment)(nil)
	_ Node = (*Script)(nil)

	_ Expr = (*BadExpr)(nil)
	_ Expr = (*BasicLit)(nil)
	_ Expr = (*DynamicLit)(nil)
	_ Expr = (*Ident)(nil)
	_ Expr = (*StarExpr)(nil)
	_ Expr = (*NamedExpr)(nil)
	_ Expr = (*BinaryExpr)(nil)
	_ Expr = (*UnaryExpr)(nil)
	_ Expr = (*ParenExpr)(nil)
	_ Expr = (*CallExpr)(nil)
	_ Expr = (*SelectorExpr)(nil)
	_ Expr = (*IndexExpr)(nil)
	_ Expr = (*ListExpr)(nil)
	_ Expr = (*BetweenExpr)(nil)
	_ Expr = (*ConditionalExpr)(nil)
	_ Expr = (*CastExpr)(nil)
	_ Expr = (*Pipeline)(nil) // pipeline is passable as a table expr
	_ Expr = (*SourceExpr)(nil)

	_ Stmt = (*QueryStmt)(nil)
	_ Stmt = (*LetStmt)(nil)
	_ Stmt = (*ExprStmt)(nil)

	_ Operator = (*WhereOp)(nil)
	_ Operator = (*ProjectOp)(nil)
	_ Operator = (*ExtendOp)(nil)
	_ Operator = (*TakeOp)(nil)
	_ Operator = (*SortOp)(nil)
	_ Operator = (*SummarizeOp)(nil)
	_ Operator = (*JoinOp)(nil)
	_ Operator = (*UnionOp)(nil)
	_ Operator = (*DistinctOp)(nil)
	_ Operator = (*CountOp)(nil)
	_ Operator = (*TopOp)(nil)
)

// --- Position tests (F2 acceptance: nodes carry correct Pos/End) ---

func TestBasicLitPosition(t *testing.T) {
	lit := &BasicLit{ValuePos: token.Pos(10), Kind: token.STRING, Value: `"hi"`}
	if lit.Pos() != token.Pos(10) {
		t.Errorf("Pos = %d, want 10", lit.Pos())
	}
	// End = ValuePos + len(Value) = 10 + 4 = 14
	if lit.End() != token.Pos(14) {
		t.Errorf("End = %d, want 14", lit.End())
	}
}

func TestIdentPosition(t *testing.T) {
	id := &Ident{NamePos: token.Pos(5), Name: "col", Tok: token.IDENT}
	if id.Pos() != token.Pos(5) {
		t.Errorf("Pos = %d, want 5", id.Pos())
	}
	if id.End() != token.Pos(8) { // 5 + len("col")
		t.Errorf("End = %d, want 8", id.End())
	}
}

func TestBinaryExprSpans(t *testing.T) {
	// 1 + 2  with operands at positions 1 and 5
	x := &BasicLit{ValuePos: token.Pos(1), Value: "1"}
	y := &BasicLit{ValuePos: token.Pos(5), Value: "2"}
	bin := &BinaryExpr{X: x, OpPos: token.Pos(3), Op: token.ADD, Y: y}
	if bin.Pos() != token.Pos(1) {
		t.Errorf("Pos = %d, want 1 (left operand)", bin.Pos())
	}
	if bin.End() != token.Pos(6) { // y End = 5 + 1
		t.Errorf("End = %d, want 6 (right operand end)", bin.End())
	}
}

func TestStarExprPosition(t *testing.T) {
	s := &StarExpr{Star: token.Pos(7)}
	if s.Pos() != token.Pos(7) || s.End() != token.Pos(8) {
		t.Errorf("StarExpr span %d..%d, want 7..8", s.Pos(), s.End())
	}
}

// --- NamedExpr semantics (F2.S2) ---

func TestNamedExprIsNamed(t *testing.T) {
	// bare expression: `x*2`
	bare := &NamedExpr{Expr: &Ident{NamePos: token.Pos(1), Name: "x"}}
	if bare.IsNamed() {
		t.Error("bare NamedExpr should not be IsNamed")
	}
	// named: `y = x*2`
	named := &NamedExpr{
		Name:   &Ident{NamePos: token.Pos(1), Name: "y"},
		Assign: token.Pos(3),
		Expr:   &Ident{NamePos: token.Pos(5), Name: "x"},
	}
	if !named.IsNamed() {
		t.Error("`y = x` NamedExpr should be IsNamed")
	}
}

// --- Operator structs hold their fields (F2.S4 structural acceptance) ---

func TestOperatorStructShapes(t *testing.T) {
	// Build a representative P0 operator and check field access compiles & works.
	where := &WhereOp{
		Pipe:      token.Pos(1),
		Where:     token.Pos(3),
		Predicate: &Ident{NamePos: token.Pos(9), Name: "flag"},
	}
	if where.Pos() != token.Pos(1) {
		t.Errorf("WhereOp.Pos = %d, want 1", where.Pos())
	}

	summ := &SummarizeOp{
		Pipe:      token.Pos(1),
		Summarize: token.Pos(3),
		Aggregates: []*NamedExpr{
			{Name: &Ident{Name: "cnt"}, Expr: &CallExpr{Fun: &Ident{Name: "count"}}},
		},
		ByPos: token.Pos(20),
		GroupBy: []*NamedExpr{
			{Expr: &Ident{Name: "k"}},
		},
	}
	if summ.Pos() != token.Pos(1) {
		t.Errorf("SummarizeOp.Pos = %d", summ.Pos())
	}
	if len(summ.Aggregates) != 1 || len(summ.GroupBy) != 1 {
		t.Errorf("SummarizeOp fields not retained: %+v", summ)
	}

	join := &JoinOp{
		Pipe: token.Pos(1),
		Join: token.Pos(3),
		Kind: JoinInner,
		Right: &Ident{Name: "T2"},
		OnExpr: []Expr{&Ident{Name: "Key"}},
	}
	if join.Kind != JoinInner {
		t.Error("JoinKind not retained")
	}
}

// --- Visitor walks all node kinds (F2.S6 acceptance) ---

// countingVisitor records every node type visited.
type countingVisitor struct{ seen []string }

func (c *countingVisitor) Visit(node Node) Visitor {
	if node == nil {
		return nil
	}
	c.seen = append(c.seen, reflect.TypeOf(node).Elem().Name())
	return c
}

// TestWalkCoverage builds an AST spanning many node types and confirms Walk
// reaches each one. This guards against visitor.go forgetting a node type.
func TestWalkCoverage(t *testing.T) {
	// T | where x > 0 | extend y = x*2 | summarize cnt = count() by y
	src := &Ident{NamePos: token.Pos(1), Name: "T"}
	pred := &BinaryExpr{
		X:   &Ident{Name: "x"},
		Op:  token.GTR,
		Y:   &BasicLit{Value: "0", Kind: token.INT},
	}
	extendE := &NamedExpr{
		Name: &Ident{Name: "y"},
		Expr: &BinaryExpr{X: &Ident{Name: "x"}, Op: token.MUL, Y: &BasicLit{Value: "2"}},
	}
	summAgg := &NamedExpr{
		Name: &Ident{Name: "cnt"},
		Expr: &CallExpr{Fun: &Ident{Name: "count"}},
	}
	summBy := &NamedExpr{Expr: &Ident{Name: "y"}}

	pipe := &Pipeline{
		Source: src,
		Ops: []Operator{
			&WhereOp{Predicate: pred},
			&ExtendOp{Columns: []*NamedExpr{extendE}},
			&SummarizeOp{Aggregates: []*NamedExpr{summAgg}, GroupBy: []*NamedExpr{summBy}},
		},
	}
	script := &Script{Statements: []Stmt{&QueryStmt{Pipeline: pipe}}}

	cv := &countingVisitor{}
	Walk(cv, script)

	// Must have seen all these node types.
	want := map[string]bool{
		"Script": true, "QueryStmt": true, "Pipeline": true,
		"WhereOp": true, "ExtendOp": true, "SummarizeOp": true,
		"Ident": true, "BinaryExpr": true, "BasicLit": true,
		"NamedExpr": true, "CallExpr": true,
	}
	got := map[string]bool{}
	for _, n := range cv.seen {
		got[n] = true
	}
	for w := range want {
		if !got[w] {
			t.Errorf("Walk did not visit node type %q (visited: %v)", w, cv.seen)
		}
	}
}

// TestBaseVisitorNoPanics confirms the no-op BaseVisitor dispatches every node
// type without panicking (i.e. the type switch is exhaustive over current nodes).
func TestBaseVisitorNoPanics(t *testing.T) {
	bv := &BaseVisitor{}
	nodes := []Node{
		&Script{}, &QueryStmt{Pipeline: &Pipeline{}}, &LetStmt{}, &ExprStmt{},
		&Pipeline{}, &BasicLit{}, &DynamicLit{}, &Ident{}, &StarExpr{}, &NamedExpr{},
		&BinaryExpr{}, &UnaryExpr{}, &ParenExpr{}, &CallExpr{}, &SelectorExpr{},
		&IndexExpr{}, &ListExpr{}, &BetweenExpr{}, &ConditionalExpr{}, &CastExpr{},
		&WhereOp{}, &ProjectOp{}, &ExtendOp{}, &TakeOp{}, &SortOp{}, &SummarizeOp{},
		&JoinOp{}, &UnionOp{}, &DistinctOp{}, &CountOp{}, &TopOp{}, &Bad{}, &BadExpr{},
	}
	for _, n := range nodes {
		Walk(bv, n) // must not panic
	}
}

// TestJoinKindValues confirms the JoinKind enum is distinct & orderable.
func TestJoinKindValues(t *testing.T) {
	kinds := []JoinKind{JoinDefault, JoinInnerUnique, JoinInner, JoinLeftOuter, JoinRightOuter, JoinFullOuter}
	seen := map[JoinKind]bool{}
	for _, k := range kinds {
		if seen[k] {
			t.Errorf("JoinKind value %d duplicated", k)
		}
		seen[k] = true
	}
}
