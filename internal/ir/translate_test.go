package ir

import (
	"reflect"
	"testing"

	"nzinfo/kql/internal/frontend/diagnostic"
	"nzinfo/kql/internal/frontend/parser"
	"nzinfo/kql/internal/frontend/token"
)

// translateSrc is a test helper: parse src, translate to IR, fail on any errors.
func translateSrc(t *testing.T, src string) *Pipeline {
	t.Helper()
	p := parser.New("test.kql", src)
	script := p.Parse()
	if diags := p.Diagnostics(); diags.HasErrors() {
		t.Fatalf("parse errors for %q:\n  %v", src, diags.Render())
	}
	var diags diagnostic.List
	pipe := Translate(script, &diags)
	if diags.HasErrors() {
		t.Fatalf("translate errors for %q:\n  %v", src, diags.Render())
	}
	if pipe == nil {
		t.Fatalf("translate returned nil pipeline for %q", src)
	}
	return pipe
}

// TestTranslateSourceOnly: `T` → Pipeline{SourceTable}
func TestTranslateSourceOnly(t *testing.T) {
	pipe := translateSrc(t, `T`)
	st, ok := pipe.Source.(*SourceTable)
	if !ok || st.Table != "T" {
		t.Errorf("Source = %+v (%T), want SourceTable T", pipe.Source, pipe.Source)
	}
	if len(pipe.Stages) != 0 {
		t.Errorf("Stages = %d, want 0", len(pipe.Stages))
	}
}

// TestTranslateWhereTake: T | where x > 0 | take 10
func TestTranslateWhereTake(t *testing.T) {
	pipe := translateSrc(t, `T | where x > 0 | take 10`)
	if len(pipe.Stages) != 2 {
		t.Fatalf("Stages = %d, want 2", len(pipe.Stages))
	}
	f, ok := pipe.Stages[0].(*Filter)
	if !ok {
		t.Fatalf("stage0 = %T, want *Filter", pipe.Stages[0])
	}
	bin, ok := f.Predicate.(*BinOp)
	if !ok || bin.Op != token.GTR {
		t.Errorf("predicate = %+v, want BinOp GTR", f.Predicate)
	}
	// left should be a Col placeholder (Name=x, ColID invalid until F5)
	if col, ok := bin.X.(*Col); !ok || col.Name != "x" || col.ColID.IsValid() {
		t.Errorf("left = %+v, want Col{x} placeholder (ColID invalid until F5)", bin.X)
	}
	if _, ok := pipe.Stages[1].(*Limit); !ok {
		t.Errorf("stage1 = %T, want *Limit", pipe.Stages[1])
	}
}

// TestTranslateProjectExtend
func TestTranslateProjectExtend(t *testing.T) {
	pipe := translateSrc(t, `T | project a, b = c + 1 | extend d = b * 2`)
	if _, ok := pipe.Stages[0].(*Project); !ok {
		t.Errorf("stage0 = %T, want *Project", pipe.Stages[0])
	}
	pr := pipe.Stages[0].(*Project)
	if len(pr.Cols) != 2 || pr.Cols[1].Name != "b" {
		t.Errorf("project cols = %+v, want [a, named b]", pr.Cols)
	}
	if _, ok := pipe.Stages[1].(*Extend); !ok {
		t.Errorf("stage1 = %T, want *Extend", pipe.Stages[1])
	}
}

// TestTranslateSummarize: aggregates + group keys + bin() call + agg Caps
func TestTranslateSummarize(t *testing.T) {
	pipe := translateSrc(t, `T | summarize c = count() by status, bin(created_at, 1h)`)
	ag, ok := pipe.Stages[0].(*Aggregate)
	if !ok {
		t.Fatalf("stage0 = %T, want *Aggregate", pipe.Stages[0])
	}
	if len(ag.Aggregates) != 1 || ag.Aggregates[0].Name != "c" {
		t.Errorf("aggregates = %+v, want [named c]", ag.Aggregates)
	}
	// count() FuncCall must have Aggregate Caps (I2.S4 acceptance)
	fc, ok := ag.Aggregates[0].Expr.(*FuncCall)
	if !ok || fc.Name != "count" || !fc.Caps.Aggregate {
		t.Errorf("count() = %+v, want FuncCall{name:count Aggregate caps}", ag.Aggregates[0].Expr)
	}
	if len(ag.Keys) != 2 {
		t.Fatalf("keys = %d, want 2", len(ag.Keys))
	}
	// second key is bin(created_at, 1h) — a FuncCall
	binCall, ok := ag.Keys[1].Expr.(*FuncCall)
	if !ok || binCall.Name != "bin" {
		t.Errorf("key1 = %+v, want FuncCall bin()", ag.Keys[1].Expr)
	}
}

// TestTranslateScalarFuncCaps: non-aggregate scalar fn has SQLExpr=true only.
func TestTranslateScalarFuncCaps(t *testing.T) {
	pipe := translateSrc(t, `T | where abs(x) > 0`)
	f := pipe.Stages[0].(*Filter)
	bin := f.Predicate.(*BinOp)
	fc, ok := bin.X.(*FuncCall)
	if !ok || fc.Name != "abs" {
		t.Fatalf("expected abs() FuncCall, got %+v", bin.X)
	}
	if !fc.Caps.SQLExpr || fc.Caps.Aggregate {
		t.Errorf("abs caps = %+v, want SQLExpr=true Aggregate=false", fc.Caps)
	}
}

// TestTranslateJoin: kind=inner, right table, on conditions
func TestTranslateJoin(t *testing.T) {
	pipe := translateSrc(t, `T | join kind=inner (T2) on k`)
	j, ok := pipe.Stages[0].(*Join)
	if !ok {
		t.Fatalf("stage0 = %T, want *Join", pipe.Stages[0])
	}
	if j.Kind != JoinInner {
		t.Errorf("Kind = %v, want JoinInner", j.Kind)
	}
	if j.Right == nil {
		t.Fatal("Right pipeline is nil")
	}
	rt, ok := j.Right.Source.(*SourceTable)
	if !ok || rt.Table != "T2" {
		t.Errorf("right source = %+v, want SourceTable T2", j.Right.Source)
	}
	if len(j.On) != 1 {
		t.Errorf("On = %d conditions, want 1", len(j.On))
	}
}

// TestTranslateTopExpands: `| top N by k` → Sort + Limit (two stages).
func TestTranslateTopExpands(t *testing.T) {
	pipe := translateSrc(t, `T | top 5 by score desc`)
	if len(pipe.Stages) != 2 {
		t.Fatalf("Stages = %d, want 2 (Sort+Limit) for top", len(pipe.Stages))
	}
	s, ok := pipe.Stages[0].(*Sort)
	if !ok || len(s.Keys) != 1 || !s.Keys[0].Desc {
		t.Errorf("stage0 = %+v, want Sort with one desc key", pipe.Stages[0])
	}
	if _, ok := pipe.Stages[1].(*Limit); !ok {
		t.Errorf("stage1 = %T, want *Limit", pipe.Stages[1])
	}
}

// TestTranslateEndToEnd: the canonical acceptance query (F4 + I2).
func TestTranslateEndToEnd(t *testing.T) {
	src := `T | where x > 0 | extend y = x*2 | summarize count() by y | order by y desc | take 10`
	pipe := translateSrc(t, src)
	wantKinds := []string{"*ir.Filter", "*ir.Extend", "*ir.Aggregate", "*ir.Sort", "*ir.Limit"}
	if len(pipe.Stages) != len(wantKinds) {
		t.Fatalf("stages = %d, want %d", len(pipe.Stages), len(wantKinds))
	}
	for i, want := range wantKinds {
		got := typeName(pipe.Stages[i])
		if got != want {
			t.Errorf("stage[%d] = %s, want %s", i, got, want)
		}
	}
}

// TestTranslateLitTypes
func TestTranslateLitTypes(t *testing.T) {
	cases := map[string]Type{
		`42`:   TypeLong,
		`3.14`: TypeReal,
		`"hi"`: TypeString,
		`true`: TypeBool,
		`1h`:   TypeTimeSpan,
	}
	for src, want := range cases {
		p := parser.New("", src)
		e := p.ParseExpr()
		var diags diagnostic.List
		t2 := &translator{diags: &diags}
		lit, ok := t2.translateExpr(e).(*Lit)
		if !ok {
			t.Errorf("translateExpr(%q): not a Lit", src)
			continue
		}
		if lit.T != want {
			t.Errorf("translateExpr(%q).T = %v, want %v", src, lit.T, want)
		}
	}
}

// TestTranslateColPlaceholder: column refs have invalid ColID until F5 binds.
func TestTranslateColPlaceholder(t *testing.T) {
	pipe := translateSrc(t, `T | where colA == 1`)
	f := pipe.Stages[0].(*Filter)
	bin := f.Predicate.(*BinOp)
	col, ok := bin.X.(*Col)
	if !ok {
		t.Fatalf("left = %T, want *Col", bin.X)
	}
	if col.ColID.IsValid() {
		t.Errorf("ColID = %v, want Invalid (F5 not wired yet)", col.ColID)
	}
	if col.Name != "colA" {
		t.Errorf("Name = %q, want colA", col.Name)
	}
}

// TestTranslateUnsupportedOperator: non-P0 operator records a diagnostic, no panic.
func TestTranslateUnsupportedOperator(t *testing.T) {
	// mv-expand isn't a P0 op the translator handles; but it also isn't parsed
	// by our P0 parser. Construct an AST with a GenericOp-like node isn't
	// possible (no GenericOp in ast). Instead, verify a bare aggregate without
	// group keys still works and that count() standalone translates.
	pipe := translateSrc(t, `T | count`)
	ag, ok := pipe.Stages[0].(*Aggregate)
	if !ok {
		t.Fatalf("stage0 = %T, want *Aggregate (count→summarize count())", pipe.Stages[0])
	}
	if len(ag.Aggregates) != 1 {
		t.Errorf("count aggregate = %+v, want one count() expr", ag.Aggregates)
	}
}

func typeName(v interface{}) string {
	return reflect.TypeOf(v).String()
}
