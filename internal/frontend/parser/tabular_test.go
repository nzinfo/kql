package parser

import (
	"reflect"
	"testing"

	"nzinfo/kql/internal/frontend/ast"
	"nzinfo/kql/internal/frontend/token"
)

// parsePipelineStr is a test helper: parse src as a single pipeline, failing
// the test if diagnostics were produced. Returns the pipeline.
func parsePipelineStr(t *testing.T, src string) *ast.Pipeline {
	t.Helper()
	p := New("test.kql", src)
	pipe := p.parsePipeline()
	if diags := p.Diagnostics(); diags.HasErrors() {
		t.Fatalf("parse %q produced errors:\n  %v", src, diags.Render())
	}
	return pipe
}

// TestPipelineSourceOnly: `T` → Pipeline{Source: T}
func TestPipelineSourceOnly(t *testing.T) {
	pipe := parsePipelineStr(t, `T`)
	id, ok := pipe.Source.(*ast.Ident)
	if !ok || id.Name != "T" {
		t.Errorf("Source = %T %+v, want Ident T", pipe.Source, pipe.Source)
	}
	if len(pipe.Ops) != 0 {
		t.Errorf("Ops = %d, want 0", len(pipe.Ops))
	}
}

// TestPipelineMultiStage: `T | where x | take 10` → 2 ops
func TestPipelineMultiStage(t *testing.T) {
	pipe := parsePipelineStr(t, `T | where x > 0 | take 10`)
	if len(pipe.Ops) != 2 {
		t.Fatalf("Ops = %d, want 2", len(pipe.Ops))
	}
	if _, ok := pipe.Ops[0].(*ast.WhereOp); !ok {
		t.Errorf("op0 = %T, want *WhereOp", pipe.Ops[0])
	}
	if _, ok := pipe.Ops[1].(*ast.TakeOp); !ok {
		t.Errorf("op1 = %T, want *TakeOp", pipe.Ops[1])
	}
}

// TestWhereOp
func TestWhereOp(t *testing.T) {
	pipe := parsePipelineStr(t, `T | where State == "TX" and Count > 5`)
	w := pipe.Ops[0].(*ast.WhereOp)
	bin, ok := w.Predicate.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("predicate = %T, want *BinaryExpr", w.Predicate)
	}
	if bin.Op != token.AND {
		t.Errorf("top op = %s, want AND", bin.Op)
	}
}

// TestProjectOp: project a, b = c + 1
func TestProjectOp(t *testing.T) {
	pipe := parsePipelineStr(t, `T | project a, b = c + 1`)
	pr := pipe.Ops[0].(*ast.ProjectOp)
	if len(pr.Columns) != 2 {
		t.Fatalf("columns = %d, want 2", len(pr.Columns))
	}
	if pr.Columns[0].IsNamed() {
		t.Error("first column should be bare")
	}
	if !pr.Columns[1].IsNamed() || pr.Columns[1].Name.Name != "b" {
		t.Errorf("second column = %+v, want named 'b'", pr.Columns[1])
	}
}

// TestExtendOp
func TestExtendOp(t *testing.T) {
	pipe := parsePipelineStr(t, `T | extend r = x * 2, s = "lit"`)
	ex := pipe.Ops[0].(*ast.ExtendOp)
	if len(ex.Columns) != 2 {
		t.Fatalf("columns = %d, want 2", len(ex.Columns))
	}
}

// TestTakeOp
func TestTakeOp(t *testing.T) {
	pipe := parsePipelineStr(t, `T | take 10`)
	tk := pipe.Ops[0].(*ast.TakeOp)
	lit, ok := tk.Count.(*ast.BasicLit)
	if !ok || lit.Value != "10" {
		t.Errorf("Count = %+v, want lit 10", tk.Count)
	}
	// alias: limit
	pipe = parsePipelineStr(t, `T | limit 5`)
	tk = pipe.Ops[0].(*ast.TakeOp)
	lit = tk.Count.(*ast.BasicLit)
	if lit.Value != "5" {
		t.Errorf("limit Count = %s, want 5", lit.Value)
	}
}

// TestSortOp: order by k desc nulls first, sort by k2 asc
func TestSortOp(t *testing.T) {
	pipe := parsePipelineStr(t, `T | order by k desc nulls first, k2 asc`)
	s := pipe.Ops[0].(*ast.SortOp)
	if len(s.Orders) != 2 {
		t.Fatalf("orders = %d, want 2", len(s.Orders))
	}
	if s.Orders[0].Order != token.DESC || s.Orders[0].Nulls != token.FIRST {
		t.Errorf("order0 = %s/%s, want desc/first", s.Orders[0].Order, s.Orders[0].Nulls)
	}
	if s.Orders[1].Order != token.ASC {
		t.Errorf("order1 = %s, want asc", s.Orders[1].Order)
	}
	// alias: sort
	pipe = parsePipelineStr(t, `T | sort by k`)
	_ = pipe.Ops[0].(*ast.SortOp)
}

// TestSummarizeOp: summarize c = count() by status, bin(created_at, 1h)
func TestSummarizeOp(t *testing.T) {
	pipe := parsePipelineStr(t, `T | summarize c = count() by status, bin(created_at, 1h)`)
	sm := pipe.Ops[0].(*ast.SummarizeOp)
	if len(sm.Aggregates) != 1 {
		t.Fatalf("aggregates = %d, want 1", len(sm.Aggregates))
	}
	if !sm.Aggregates[0].IsNamed() || sm.Aggregates[0].Name.Name != "c" {
		t.Errorf("agg0 = %+v, want named 'c'", sm.Aggregates[0])
	}
	if len(sm.GroupBy) != 2 {
		t.Fatalf("groupBy = %d, want 2", len(sm.GroupBy))
	}
	// second group key is bin(created_at, 1h) — a CallExpr
	binCall, ok := sm.GroupBy[1].Expr.(*ast.CallExpr)
	if !ok {
		t.Fatalf("groupBy1 = %T, want *CallExpr (bin)", sm.GroupBy[1].Expr)
	}
	if id, ok := binCall.Fun.(*ast.Ident); !ok || id.Name != "bin" {
		t.Errorf("groupBy1 fun = %+v, want bin", binCall.Fun)
	}
}

// TestJoinOp: join kind=inner (T2) on k1, k2
func TestJoinOp(t *testing.T) {
	pipe := parsePipelineStr(t, `T | join kind=inner (T2) on k1, k2`)
	j := pipe.Ops[0].(*ast.JoinOp)
	if j.Kind != ast.JoinInner {
		t.Errorf("kind = %v, want JoinInner", j.Kind)
	}
	if len(j.OnExpr) != 2 {
		t.Errorf("on conditions = %d, want 2", len(j.OnExpr))
	}
}

// TestJoinKinds: each kind value maps correctly.
func TestJoinKinds(t *testing.T) {
	cases := map[string]ast.JoinKind{
		"innerunique": ast.JoinInnerUnique,
		"inner":       ast.JoinInner,
		"left":        ast.JoinLeftOuter,
		"leftouter":   ast.JoinLeftOuter,
		"rightouter":  ast.JoinRightOuter,
		"fullouter":   ast.JoinFullOuter,
	}
	for kv, want := range cases {
		pipe := parsePipelineStr(t, `T | join kind=`+kv+` (T2) on k`)
		j := pipe.Ops[0].(*ast.JoinOp)
		if j.Kind != want {
			t.Errorf("kind=%s → %v, want %v", kv, j.Kind, want)
		}
	}
}

// TestUnionOp
func TestUnionOp(t *testing.T) {
	pipe := parsePipelineStr(t, `T | union T2, T3`)
	u := pipe.Ops[0].(*ast.UnionOp)
	if len(u.Tables) != 2 {
		t.Errorf("tables = %d, want 2", len(u.Tables))
	}
}

// TestDistinctOp
func TestDistinctOp(t *testing.T) {
	pipe := parsePipelineStr(t, `T | distinct a, b`)
	d := pipe.Ops[0].(*ast.DistinctOp)
	if len(d.Columns) != 2 {
		t.Errorf("columns = %d, want 2", len(d.Columns))
	}
	// distinct *
	pipe = parsePipelineStr(t, `T | distinct *`)
	d = pipe.Ops[0].(*ast.DistinctOp)
	if _, ok := d.Columns[0].(*ast.StarExpr); !ok {
		t.Errorf("distinct * column = %T, want *StarExpr", d.Columns[0])
	}
}

// TestCountOp
func TestCountOp(t *testing.T) {
	pipe := parsePipelineStr(t, `T | count`)
	if _, ok := pipe.Ops[0].(*ast.CountOp); !ok {
		t.Errorf("op = %T, want *CountOp", pipe.Ops[0])
	}
}

// TestTopOp
func TestTopOp(t *testing.T) {
	pipe := parsePipelineStr(t, `T | top 5 by score desc`)
	top := pipe.Ops[0].(*ast.TopOp)
	if len(top.Orders) != 1 || top.Orders[0].Order != token.DESC {
		t.Errorf("orders = %+v, want [desc]", top.Orders)
	}
}

// TestLetStmt: scalar let and tabular let
func TestLetStmt(t *testing.T) {
	// scalar let
	p := New("", `let n = 5`)
	script := p.Parse()
	if diags := p.Diagnostics(); diags.HasErrors() {
		t.Fatalf("scalar let errors: %v", diags.Render())
	}
	if len(script.Statements) != 1 {
		t.Fatalf("stmts = %d, want 1", len(script.Statements))
	}
	let, ok := script.Statements[0].(*ast.LetStmt)
	if !ok {
		t.Fatalf("stmt0 = %T, want *LetStmt", script.Statements[0])
	}
	if let.Name.Name != "n" {
		t.Errorf("name = %q, want n", let.Name.Name)
	}

	// tabular let: let X = T | where x > 0
	p = New("", `let X = T | where x > 0`)
	script = p.Parse()
	if diags := p.Diagnostics(); diags.HasErrors() {
		t.Fatalf("tabular let errors: %v", diags.Render())
	}
	let = script.Statements[0].(*ast.LetStmt)
	pipe, ok := let.Expr.(*ast.Pipeline)
	if !ok {
		t.Fatalf("tabular let expr = %T, want *Pipeline", let.Expr)
	}
	if len(pipe.Ops) != 1 {
		t.Errorf("tabular let pipeline ops = %d, want 1", len(pipe.Ops))
	}
}

// TestEndToEndFullQuery: the canonical F4 acceptance query.
func TestEndToEndFullQuery(t *testing.T) {
	src := `T | where x > 0 | extend y = x*2 | summarize count() by y | order by y desc | take 10`
	p := New("q.kql", src)
	script := p.Parse()
	if diags := p.Diagnostics(); diags.HasErrors() {
		t.Fatalf("errors: %v", diags.Render())
	}
	if len(script.Statements) != 1 {
		t.Fatalf("stmts = %d, want 1", len(script.Statements))
	}
	q, ok := script.Statements[0].(*ast.QueryStmt)
	if !ok {
		t.Fatalf("stmt0 = %T, want *QueryStmt", script.Statements[0])
	}
	// expect: Source T + 5 ops (where, extend, summarize, order, take)
	if len(q.Pipeline.Ops) != 5 {
		t.Fatalf("pipeline ops = %d, want 5", len(q.Pipeline.Ops))
	}
	wantKinds := []string{"*ast.WhereOp", "*ast.ExtendOp", "*ast.SummarizeOp", "*ast.SortOp", "*ast.TakeOp"}
	for i, want := range wantKinds {
		got := typeName(q.Pipeline.Ops[i])
		if got != want {
			t.Errorf("op[%d] = %s, want %s", i, got, want)
		}
	}
}

// TestMultiStatementScript: let + query separated by ;
func TestMultiStatementScript(t *testing.T) {
	src := `let N = 10; T | top N by Score`
	p := New("", src)
	script := p.Parse()
	if diags := p.Diagnostics(); diags.HasErrors() {
		t.Fatalf("errors: %v", diags.Render())
	}
	if len(script.Statements) != 2 {
		t.Fatalf("stmts = %d, want 2", len(script.Statements))
	}
	if _, ok := script.Statements[0].(*ast.LetStmt); !ok {
		t.Errorf("stmt0 = %T, want *LetStmt", script.Statements[0])
	}
	if _, ok := script.Statements[1].(*ast.QueryStmt); !ok {
		t.Errorf("stmt1 = %T, want *QueryStmt", script.Statements[1])
	}
}

// TestOperatorErrorRecovery: bad operator doesn't abort the whole script.
func TestOperatorErrorRecovery(t *testing.T) {
	p := New("", `T | bogus_op | take 10`)
	_ = p.Parse()
	if !p.Diagnostics().HasErrors() {
		t.Error("expected error for unknown operator")
	}
}

func typeName(v interface{}) string {
	// v is an interface value whose dynamic type is already a pointer (e.g.
	// *ast.WhereOp), so reflect directly — do NOT call .Elem().
	return reflect.TypeOf(v).String()
}
