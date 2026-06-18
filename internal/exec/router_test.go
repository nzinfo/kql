package exec

import (
	"context"
	"database/sql"
	"testing"

	"nzinfo/kql/internal/backend/duckdb"
	"nzinfo/kql/internal/ir"
)

// dummyBackend is a minimal backend.Backend for routing tests (no real DB).
type dummyBackend struct{ name string }

func (d *dummyBackend) Dialect() interface{}              { return nil }
func (d *dummyBackend) Emit(pipe *ir.Pipeline) (interface{}, error) { return nil, nil }
func (d *dummyBackend) Exec(ctx context.Context, q interface{}) (interface{}, error) {
	return nil, nil
}
func (d *dummyBackend) Close() error { return nil }

// TestRoute_SingleEngine: with one engine, all stages go to it.
func TestRoute_SingleEngine(t *testing.T) {
	router := &EngineRouter{Engines: []Engine{{Name: "pg"}}}
	pipe := &ir.Pipeline{
		Source: &ir.SourceTable{Table: "t"},
		Stages: []ir.Stage{
			&ir.Filter{Predicate: &ir.Col{Name: "x"}},
			&ir.Aggregate{Aggregates: []*ir.NamedExpr{{Name: "c", Expr: &ir.FuncCall{Name: "count"}}}},
		},
	}
	decisions := router.Route(pipe)
	if len(decisions) != 1 {
		t.Fatalf("decisions = %d, want 1", len(decisions))
	}
	if decisions[0].Engine != "pg" {
		t.Errorf("engine = %q, want pg", decisions[0].Engine)
	}
	if decisions[0].Reason != "single engine available" {
		t.Errorf("reason = %q", decisions[0].Reason)
	}
}

// TestRoute_PgDuckDB_SplitAtAggregate: with pg+DuckDB, pre-aggregate stages
// go to pg, aggregate+post go to DuckDB.
func TestRoute_PgDuckDB_SplitAtAggregate(t *testing.T) {
	router := &EngineRouter{Engines: []Engine{
		{Name: "pg"},
		{Name: "duckdb"},
	}}
	pipe := &ir.Pipeline{
		Source: &ir.SourceTable{Table: "t"},
		Stages: []ir.Stage{
			&ir.Filter{Predicate: &ir.Col{Name: "x"}},     // → pg
			&ir.Sort{Keys: []ir.SortKey{{Expr: &ir.Col{Name: "y"}}}}, // → pg
			&ir.Aggregate{                                      // → duckdb
				Aggregates: []*ir.NamedExpr{{Name: "c", Expr: &ir.FuncCall{Name: "count"}}},
			},
			&ir.Sort{Keys: []ir.SortKey{{Expr: &ir.Col{Name: "c"}, Desc: true}}}, // → duckdb
		},
	}
	decisions := router.Route(pipe)
	if len(decisions) != 2 {
		t.Fatalf("decisions = %d, want 2", len(decisions))
	}
	if decisions[0].Engine != "pg" {
		t.Errorf("decision 0 engine = %q, want pg", decisions[0].Engine)
	}
	if len(decisions[0].Stages) != 2 {
		t.Errorf("decision 0 stages = %d, want 2 (filter+sort)", len(decisions[0].Stages))
	}
	if decisions[1].Engine != "duckdb" {
		t.Errorf("decision 1 engine = %q, want duckdb", decisions[1].Engine)
	}
	if len(decisions[1].Stages) != 2 {
		t.Errorf("decision 1 stages = %d, want 2 (agg+sort)", len(decisions[1].Stages))
	}
	t.Logf("pg: %d stages (%s)", len(decisions[0].Stages), decisions[0].Reason)
	t.Logf("duckdb: %d stages (%s)", len(decisions[1].Stages), decisions[1].Reason)
}

// TestRoute_PgDuckDB_NoAggregate: without an aggregate, everything goes to pg.
func TestRoute_PgDuckDB_NoAggregate(t *testing.T) {
	router := &EngineRouter{Engines: []Engine{{Name: "pg"}, {Name: "duckdb"}}}
	pipe := &ir.Pipeline{
		Source: &ir.SourceTable{Table: "t"},
		Stages: []ir.Stage{
			&ir.Filter{Predicate: &ir.Col{Name: "x"}},
			&ir.Limit{Count: &ir.Lit{Value: int64(10), HasValue: true, T: ir.TypeLong}},
		},
	}
	decisions := router.Route(pipe)
	if len(decisions) != 1 {
		t.Fatalf("decisions = %d, want 1 (no aggregate)", len(decisions))
	}
	if decisions[0].Engine != "pg" {
		t.Errorf("engine = %q, want pg", decisions[0].Engine)
	}
}

// TestExecMulti_SingleEngineFallback: ExecMulti with one engine delegates to
// ExecPipeline.
func TestExecMulti_SingleEngineFallback(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.Exec(`CREATE TABLE t (id INTEGER)`)
	db.Exec(`INSERT INTO t VALUES (1),(2),(3)`)

	bk := duckdb.NewFromDB(db)
	engines := []Engine{{Backend: bk, Name: "duckdb"}}

	pipe := &ir.Pipeline{
		Source: &ir.SourceTable{Table: "t"},
		Stages: []ir.Stage{
			&ir.Limit{Count: &ir.Lit{Value: int64(2), HasValue: true, T: ir.TypeLong}},
		},
	}
	res, err := ExecMulti(context.Background(), engines, pipe)
	if err != nil {
		t.Fatalf("ExecMulti: %v", err)
	}
	if len(res.Rows) != 2 {
		t.Errorf("rows = %d, want 2", len(res.Rows))
	}
}

// TestRoute_NoPgDuckCombo: sqlite+pg (no DuckDB) → single engine.
func TestRoute_NoPgDuckCombo(t *testing.T) {
	router := &EngineRouter{Engines: []Engine{{Name: "sqlite"}, {Name: "pg"}}}
	pipe := &ir.Pipeline{
		Source: &ir.SourceTable{Table: "t"},
		Stages: []ir.Stage{
			&ir.Aggregate{Aggregates: []*ir.NamedExpr{{Name: "c", Expr: &ir.FuncCall{Name: "count"}}}},
		},
	}
	decisions := router.Route(pipe)
	if len(decisions) != 1 {
		t.Fatalf("decisions = %d, want 1 (no duckdb)", len(decisions))
	}
}
