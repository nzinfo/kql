package binder

import (
	"testing"

	"nzinfo/kql/internal/frontend/diagnostic"
	"nzinfo/kql/internal/frontend/token"
	"nzinfo/kql/internal/ir"
)

// fakeProvider is a SchemaProvider backed by a static table→columns map.
type fakeProvider struct {
	tables map[string][]string
}

func (f *fakeProvider) Schema(table string) (*Schema, error) {
	if cols, ok := f.tables[table]; ok {
		return &Schema{Cols: cols}, nil
	}
	return nil, errNotFound(table)
}

// Build a pipeline programmatically for testing (avoiding the parser).
func src(table string) *ir.SourceTable { return &ir.SourceTable{Table: table} }
func col(name string) *ir.Col          { return &ir.Col{Name: name} }
func lit(v int64) *ir.Lit              { return &ir.Lit{T: ir.TypeLong, Value: v, HasValue: true} }

// TestBindKnownColumn: a column in the source schema binds cleanly (no diag).
func TestBindKnownColumn(t *testing.T) {
	pipe := &ir.Pipeline{
		Source: src("events"),
		Stages: []ir.Stage{
			&ir.Filter{Predicate: &ir.BinOp{Op: token.GTR, X: col("state"), Y: lit(0)}},
		},
	}
	prov := &fakeProvider{tables: map[string][]string{"events": {"id", "state"}}}
	var diags diagnostic.List
	if _, err := Bind(pipe, prov, &diags); err != nil {
		t.Fatal(err)
	}
	if diags.HasErrors() {
		t.Errorf("unexpected errors: %v", diags.Render())
	}
}

// TestBindUnknownColumn: a column NOT in the schema produces KQL001.
func TestBindUnknownColumn(t *testing.T) {
	pipe := &ir.Pipeline{
		Source: src("events"),
		Stages: []ir.Stage{
			&ir.Filter{Predicate: &ir.BinOp{Op: token.GTR, X: col("nonexistent"), Y: lit(0)}},
		},
	}
	prov := &fakeProvider{tables: map[string][]string{"events": {"id", "state"}}}
	var diags diagnostic.List
	Bind(pipe, prov, &diags)
	if !diags.HasErrors() {
		t.Fatal("expected unknown-column error, got none")
	}
	msgs := diags.Render()
	found := false
	for _, m := range msgs {
		if contains(m, "nonexistent") && contains(m, "KQL001") {
			found = true
		}
	}
	if !found {
		t.Errorf("error didn't name the column / code: %v", msgs)
	}
}

// TestBindMissingTable: a source table the provider doesn't know errors.
func TestBindMissingTable(t *testing.T) {
	pipe := &ir.Pipeline{Source: src("ghost")}
	prov := &fakeProvider{tables: map[string][]string{"events": {"id"}}}
	var diags diagnostic.List
	Bind(pipe, prov, &diags)
	if !diags.HasErrors() {
		t.Fatal("expected error for missing table, got none")
	}
}

// TestBindNilProviderPermissive: with no provider, the binder is permissive
// (no unknown-column errors) — so it can run against unintrospectable sources.
func TestBindNilProviderPermissive(t *testing.T) {
	pipe := &ir.Pipeline{
		Source: src("events"),
		Stages: []ir.Stage{
			&ir.Filter{Predicate: col("whatever")},
		},
	}
	var diags diagnostic.List
	Bind(pipe, nil, &diags) // nil provider
	if diags.HasErrors() {
		t.Errorf("nil provider should be permissive, got: %v", diags.Render())
	}
}

// TestBindProjectOutputSchema: after project, only projected columns are valid.
func TestBindProjectOutputSchema(t *testing.T) {
	// project state → next stage can use state but NOT id.
	pipe := &ir.Pipeline{
		Source: src("events"),
		Stages: []ir.Stage{
			&ir.Project{Cols: []*ir.NamedExpr{{Name: "state", Expr: col("state")}}},
			&ir.Filter{Predicate: col("id")}, // id was projected away → unknown
		},
	}
	prov := &fakeProvider{tables: map[string][]string{"events": {"id", "state"}}}
	var diags diagnostic.List
	Bind(pipe, prov, &diags)
	if !diags.HasErrors() {
		t.Fatal("expected id to be unknown after project-away, got no error")
	}
}

// TestBindExtendAddsColumn: extend adds a column visible to later stages.
func TestBindExtendAddsColumn(t *testing.T) {
	pipe := &ir.Pipeline{
		Source: src("events"),
		Stages: []ir.Stage{
			&ir.Extend{Cols: []*ir.NamedExpr{{Name: "doubled", Expr: col("id")}}},
			&ir.Filter{Predicate: col("doubled")}, // OK — extend added it
		},
	}
	prov := &fakeProvider{tables: map[string][]string{"events": {"id"}}}
	var diags diagnostic.List
	Bind(pipe, prov, &diags)
	if diags.HasErrors() {
		t.Errorf("extend-added column should be visible: %v", diags.Render())
	}
}

// TestBindSummarizeOutputSchema: summarize's output is keys + named aggs.
func TestBindSummarizeOutputSchema(t *testing.T) {
	// summarize total = sum(id) by state → next stage sees {state, total}.
	pipe := &ir.Pipeline{
		Source: src("events"),
		Stages: []ir.Stage{
			&ir.Aggregate{
				Keys:       []*ir.NamedExpr{{Name: "state", Expr: col("state")}},
				Aggregates: []*ir.NamedExpr{{Name: "total", Expr: &ir.FuncCall{Name: "sum", Args: []ir.Expr{col("id")}}}},
			},
			&ir.Filter{Predicate: col("total")}, // OK — produced by summarize
		},
	}
	prov := &fakeProvider{tables: map[string][]string{"events": {"id", "state"}}}
	var diags diagnostic.List
	Bind(pipe, prov, &diags)
	if diags.HasErrors() {
		t.Errorf("summarize-produced column should be visible: %v", diags.Render())
	}
}

// TestBindSummarizeDropsUnaggregated: a column neither grouped nor aggregated
// is NOT visible after summarize.
func TestBindSummarizeDropsUnaggregated(t *testing.T) {
	pipe := &ir.Pipeline{
		Source: src("events"),
		Stages: []ir.Stage{
			&ir.Aggregate{
				Keys:       []*ir.NamedExpr{{Name: "state", Expr: col("state")}},
				Aggregates: []*ir.NamedExpr{{Name: "total", Expr: &ir.FuncCall{Name: "sum", Args: []ir.Expr{col("id")}}}},
			},
			&ir.Filter{Predicate: col("id")}, // id dropped by summarize → unknown
		},
	}
	prov := &fakeProvider{tables: map[string][]string{"events": {"id", "state"}}}
	var diags diagnostic.List
	Bind(pipe, prov, &diags)
	if !diags.HasErrors() {
		t.Fatal("expected id to be unknown after summarize (not grouped/agg'd), got no error")
	}
}

// TestSchemaHas
func TestSchemaHas(t *testing.T) {
	s := &Schema{Cols: []string{"a", "b"}}
	if !s.Has("a") || !s.Has("b") {
		t.Error("Has should find present columns")
	}
	if s.Has("c") {
		t.Error("Has should not find absent column")
	}
	var nilS *Schema
	if !nilS.Has("anything") {
		t.Error("nil schema should be permissive (Has=true)")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// errNotFound is a minimal error for the fake provider's missing-table case.
type notFoundErr struct{ table string }
func (e *notFoundErr) Error() string { return "table " + e.table + " not found" }
func errNotFound(table string) error { return &notFoundErr{table: table} }
