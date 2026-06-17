package binder

import (
	"testing"

	"nzinfo/kql/internal/frontend/diagnostic"
	"nzinfo/kql/internal/frontend/token"
	"nzinfo/kql/internal/ir"
)

// fakeProvider is a SchemaProvider backed by a static table→columns map.
type fakeProvider struct {
	tables map[string][]ColBinding
}

func (f *fakeProvider) Schema(table string) (*Schema, error) {
	if c, ok := f.tables[table]; ok {
		return &Schema{Cols: c}, nil
	}
	return nil, errNotFound(table)
}

func cols(names ...string) []ColBinding {
	out := make([]ColBinding, len(names))
	for i, n := range names {
		out[i] = ColBinding{PhysicalName: n, DisplayName: n}
	}
	return out
}

func src(table string) *ir.SourceTable { return &ir.SourceTable{Table: table} }
func col(name string) *ir.Col          { return &ir.Col{Name: name} }
func lit(v int64) *ir.Lit              { return &ir.Lit{T: ir.TypeLong, Value: v, HasValue: true} }

func TestBindKnownColumn(t *testing.T) {
	pipe := &ir.Pipeline{
		Source: src("events"),
		Stages: []ir.Stage{
			&ir.Filter{Predicate: &ir.BinOp{Op: token.GTR, X: col("state"), Y: lit(0)}},
		},
	}
	prov := &fakeProvider{tables: map[string][]ColBinding{"events": cols("id", "state")}}
	var diags diagnostic.List
	if _, err := Bind(pipe, prov, &diags); err != nil {
		t.Fatal(err)
	}
	if diags.HasErrors() {
		t.Errorf("unexpected errors: %v", diags.Render())
	}
}

func TestBindUnknownColumn(t *testing.T) {
	pipe := &ir.Pipeline{
		Source: src("events"),
		Stages: []ir.Stage{
			&ir.Filter{Predicate: &ir.BinOp{Op: token.GTR, X: col("nonexistent"), Y: lit(0)}},
		},
	}
	prov := &fakeProvider{tables: map[string][]ColBinding{"events": cols("id", "state")}}
	var diags diagnostic.List
	Bind(pipe, prov, &diags)
	if !diags.HasErrors() {
		t.Fatal("expected unknown-column error, got none")
	}
}

func TestBindMissingTable(t *testing.T) {
	pipe := &ir.Pipeline{Source: src("ghost")}
	prov := &fakeProvider{tables: map[string][]ColBinding{"events": cols("id")}}
	var diags diagnostic.List
	Bind(pipe, prov, &diags)
	if !diags.HasErrors() {
		t.Fatal("expected error for missing table, got none")
	}
}

func TestBindNilProviderPermissive(t *testing.T) {
	pipe := &ir.Pipeline{
		Source: src("events"),
		Stages: []ir.Stage{
			&ir.Filter{Predicate: col("whatever")},
		},
	}
	var diags diagnostic.List
	Bind(pipe, nil, &diags)
	if diags.HasErrors() {
		t.Errorf("nil provider should be permissive, got: %v", diags.Render())
	}
}

func TestBindProjectDropsColumn(t *testing.T) {
	pipe := &ir.Pipeline{
		Source: src("events"),
		Stages: []ir.Stage{
			&ir.Project{Cols: []*ir.NamedExpr{{Name: "state", Expr: col("state")}}},
			&ir.Filter{Predicate: col("id")},
		},
	}
	prov := &fakeProvider{tables: map[string][]ColBinding{"events": cols("id", "state")}}
	var diags diagnostic.List
	Bind(pipe, prov, &diags)
	if !diags.HasErrors() {
		t.Fatal("expected id to be unknown after project-away")
	}
}

func TestBindExtendAddsColumn(t *testing.T) {
	pipe := &ir.Pipeline{
		Source: src("events"),
		Stages: []ir.Stage{
			&ir.Extend{Cols: []*ir.NamedExpr{{Name: "doubled", Expr: col("id")}}},
			&ir.Filter{Predicate: col("doubled")},
		},
	}
	prov := &fakeProvider{tables: map[string][]ColBinding{"events": cols("id")}}
	var diags diagnostic.List
	Bind(pipe, prov, &diags)
	if diags.HasErrors() {
		t.Errorf("extend-added column should be visible: %v", diags.Render())
	}
}

func TestBindSummarizeOutputSchema(t *testing.T) {
	pipe := &ir.Pipeline{
		Source: src("events"),
		Stages: []ir.Stage{
			&ir.Aggregate{
				Keys:       []*ir.NamedExpr{{Name: "state", Expr: col("state")}},
				Aggregates: []*ir.NamedExpr{{Name: "total", Expr: &ir.FuncCall{Name: "sum", Args: []ir.Expr{col("id")}}}},
			},
			&ir.Filter{Predicate: col("total")},
		},
	}
	prov := &fakeProvider{tables: map[string][]ColBinding{"events": cols("id", "state")}}
	var diags diagnostic.List
	Bind(pipe, prov, &diags)
	if diags.HasErrors() {
		t.Errorf("summarize-produced column should be visible: %v", diags.Render())
	}
}

func TestBindSummarizeDropsUnaggregated(t *testing.T) {
	pipe := &ir.Pipeline{
		Source: src("events"),
		Stages: []ir.Stage{
			&ir.Aggregate{
				Keys:       []*ir.NamedExpr{{Name: "state", Expr: col("state")}},
				Aggregates: []*ir.NamedExpr{{Name: "total", Expr: &ir.FuncCall{Name: "sum", Args: []ir.Expr{col("id")}}}},
			},
			&ir.Filter{Predicate: col("id")},
		},
	}
	prov := &fakeProvider{tables: map[string][]ColBinding{"events": cols("id", "state")}}
	var diags diagnostic.List
	Bind(pipe, prov, &diags)
	if !diags.HasErrors() {
		t.Fatal("expected id to be unknown after summarize")
	}
}

// TestLookupCaseInsensitive is the core case-folding fix: a KQL `EventType`
// reference resolves against a pg-lowercased `eventtype` schema, and checkExpr
// rewrites Col.Name to the physical name + stamps a ColID.
func TestLookupCaseInsensitive(t *testing.T) {
	s := &Schema{Cols: []ColBinding{
		{ColID: 1, PhysicalName: "eventtype", DisplayName: "eventtype"},
	}}
	bd, ok := s.Lookup("EventType")
	if !ok {
		t.Fatal("case-insensitive Lookup should hit EventType→eventtype")
	}
	if bd.PhysicalName != "eventtype" {
		t.Errorf("PhysicalName = %q, want eventtype", bd.PhysicalName)
	}

	pipe := &ir.Pipeline{
		Source: src("events"),
		Stages: []ir.Stage{
			&ir.Filter{Predicate: col("EventType")},
		},
	}
	prov := &fakeProvider{tables: map[string][]ColBinding{
		"events": {{PhysicalName: "eventtype", DisplayName: "eventtype"}},
	}}
	var diags diagnostic.List
	Bind(pipe, prov, &diags)
	if diags.HasErrors() {
		t.Fatalf("EventType should resolve to eventtype: %v", diags.Render())
	}
	f := pipe.Stages[0].(*ir.Filter)
	resolved := f.Predicate.(*ir.Col)
	if resolved.Name != "eventtype" {
		t.Errorf("Col.Name = %q, want eventtype (physical)", resolved.Name)
	}
	if !resolved.ColID.IsValid() {
		t.Error("Col.ColID should be valid after bind")
	}
}

func TestBindColIDAllocatedDistinct(t *testing.T) {
	pipe := &ir.Pipeline{Source: src("events")}
	prov := &fakeProvider{tables: map[string][]ColBinding{
		"events": cols("a", "b", "c"),
	}}
	var diags diagnostic.List
	b := &binder{prov: prov, diags: &diags}
	schema := b.sourceSchema(pipe.Source)
	ids := map[ir.ColID]bool{}
	for _, c := range schema.Cols {
		if ids[c.ColID] {
			t.Errorf("ColID %d duplicated", c.ColID)
		}
		ids[c.ColID] = true
	}
	if len(ids) != 3 {
		t.Errorf("got %d distinct ColIDs, want 3", len(ids))
	}
}

func TestSchemaHas(t *testing.T) {
	s := &Schema{Cols: cols("a", "b")}
	if !s.Has("a") || !s.Has("A") || !s.Has("B") {
		t.Error("Has should be case-insensitive")
	}
	if s.Has("c") {
		t.Error("Has should not find absent column")
	}
	var nilS *Schema
	if !nilS.Has("anything") {
		t.Error("nil schema should be permissive")
	}
}

type notFoundErr struct{ table string }

func (e *notFoundErr) Error() string { return "table " + e.table + " not found" }
func errNotFound(table string) error { return &notFoundErr{table: table} }

// --- F5.S6 function call validation (KQL003/KQL004) ---

// hasDiagCode reports whether diags contains an item with the given code.
func hasDiagCode(t *testing.T, diags *diagnostic.List, code diagnostic.Code) bool {
	t.Helper()
	for _, d := range diags.Items() {
		if d.Code == code {
			return true
		}
	}
	return false
}

// fnCall builds an *ir.FuncCall with the given name and arg expressions.
func fnCall(name string, args ...ir.Expr) *ir.FuncCall {
	return &ir.FuncCall{Name: name, Args: args}
}

// TestFuncValidation_KnownGood: a catalogued function with correct arity emits
// NO diagnostics (neither KQL003 nor KQL004).
func TestFuncValidation_KnownGood(t *testing.T) {
	pipe := &ir.Pipeline{
		Source: src("t"),
		Stages: []ir.Stage{
			&ir.Extend{Cols: []*ir.NamedExpr{
				{Name: "s", Expr: fnCall("tostring", col("id"))},
			}},
		},
	}
	prov := &fakeProvider{tables: map[string][]ColBinding{"t": cols("id")}}
	var diags diagnostic.List
	if _, err := Bind(pipe, prov, &diags); err != nil {
		t.Fatal(err)
	}
	if diags.Has() {
		t.Errorf("expected no diagnostics for tostring(id), got: %v", diags.Render())
	}
}

// TestFuncValidation_UnknownFunction: a function not in the catalog emits KQL003
// as a WARNING (not an error - unknown functions are often user-defined).
func TestFuncValidation_UnknownFunction(t *testing.T) {
	pipe := &ir.Pipeline{
		Source: src("t"),
		Stages: []ir.Stage{
			&ir.Extend{Cols: []*ir.NamedExpr{
				{Name: "r", Expr: fnCall("my_custom_udf", col("id"))},
			}},
		},
	}
	prov := &fakeProvider{tables: map[string][]ColBinding{"t": cols("id")}}
	var diags diagnostic.List
	if _, err := Bind(pipe, prov, &diags); err != nil {
		t.Fatal(err)
	}
	if !hasDiagCode(t, &diags, diagnostic.UnknownFunction) {
		t.Errorf("expected KQL003 UnknownFunction for my_custom_udf, got: %v", diags.Render())
	}
	// CRITICAL: it must be a warning, not an error - so HasErrors stays false
	// and execution is not blocked (the corpus uses many pass-through functions).
	if diags.HasErrors() {
		t.Errorf("KQL003 must be a warning, not an error: %v", diags.Render())
	}
}

// TestFuncValidation_TooFewArgs: a catalogued function called with fewer than
// MinArgs emits KQL004 (warning).
func TestFuncValidation_TooFewArgs(t *testing.T) {
	// strcat has MinArgs=1; calling it with 0 args violates arity.
	pipe := &ir.Pipeline{
		Source: src("t"),
		Stages: []ir.Stage{
			&ir.Extend{Cols: []*ir.NamedExpr{
				{Name: "r", Expr: fnCall("strcat")},
			}},
		},
	}
	prov := &fakeProvider{tables: map[string][]ColBinding{"t": cols("id")}}
	var diags diagnostic.List
	if _, err := Bind(pipe, prov, &diags); err != nil {
		t.Fatal(err)
	}
	if !hasDiagCode(t, &diags, diagnostic.ArgCount) {
		t.Errorf("expected KQL004 ArgCount for strcat() with 0 args, got: %v", diags.Render())
	}
	if diags.HasErrors() {
		t.Errorf("KQL004 must be a warning, not an error: %v", diags.Render())
	}
}

// TestFuncValidation_TooManyArgs: a catalogued function called with more than
// MaxArgs emits KQL004 (warning).
func TestFuncValidation_TooManyArgs(t *testing.T) {
	// tostring has MaxArgs=1; calling it with 2 args violates arity.
	pipe := &ir.Pipeline{
		Source: src("t"),
		Stages: []ir.Stage{
			&ir.Extend{Cols: []*ir.NamedExpr{
				{Name: "r", Expr: fnCall("tostring", col("id"), lit(1))},
			}},
		},
	}
	prov := &fakeProvider{tables: map[string][]ColBinding{"t": cols("id")}}
	var diags diagnostic.List
	if _, err := Bind(pipe, prov, &diags); err != nil {
		t.Fatal(err)
	}
	if !hasDiagCode(t, &diags, diagnostic.ArgCount) {
		t.Errorf("expected KQL004 ArgCount for tostring(x,y), got: %v", diags.Render())
	}
	if diags.HasErrors() {
		t.Errorf("KQL004 must be a warning, not an error: %v", diags.Render())
	}
}

// TestFuncValidation_VariadicOk: a variadic function (MaxArgs < 0) with many
// args emits no KQL004. strcat is variadic (MinArgs=1, MaxArgs=-1).
func TestFuncValidation_VariadicOk(t *testing.T) {
	pipe := &ir.Pipeline{
		Source: src("t"),
		Stages: []ir.Stage{
			&ir.Extend{Cols: []*ir.NamedExpr{
				{Name: "r", Expr: fnCall("strcat", col("id"), col("id"), col("id"), col("id"))},
			}},
		},
	}
	prov := &fakeProvider{tables: map[string][]ColBinding{"t": cols("id")}}
	var diags diagnostic.List
	if _, err := Bind(pipe, prov, &diags); err != nil {
		t.Fatal(err)
	}
	if hasDiagCode(t, &diags, diagnostic.ArgCount) {
		t.Errorf("variadic strcat with 4 args should not emit KQL004: %v", diags.Render())
	}
}

// TestFuncValidation_CaseInsensitive: catalog lookup is case-insensitive
// (STRCAT == strcat), so uppercased calls do not false-trigger KQL003.
func TestFuncValidation_CaseInsensitive(t *testing.T) {
	pipe := &ir.Pipeline{
		Source: src("t"),
		Stages: []ir.Stage{
			&ir.Extend{Cols: []*ir.NamedExpr{
				{Name: "r", Expr: fnCall("TOSTRING", col("id"))},
			}},
		},
	}
	prov := &fakeProvider{tables: map[string][]ColBinding{"t": cols("id")}}
	var diags diagnostic.List
	if _, err := Bind(pipe, prov, &diags); err != nil {
		t.Fatal(err)
	}
	if hasDiagCode(t, &diags, diagnostic.UnknownFunction) {
		t.Errorf("TOSTRING should resolve case-insensitively (no KQL003): %v", diags.Render())
	}
}
