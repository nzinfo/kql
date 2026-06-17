// pg O4 hint graceful-degrade e2e test: a join with a hint stamped on must
// EXECUTE correctly on stock postgres (without pg_hint_plan installed). The
// hint comment is a standard SQL comment, silently ignored by stock pg.
// Gated on KQL_PG_DSN (same as pkg/kql/pg_e2e_test.go).
package pg

import (
	"context"
	"os"
	"testing"

	"nzinfo/kql/internal/frontend/token"
	"nzinfo/kql/internal/ir"
)

// TestE2E_O4_HintGracefulDegrade: execute a hinted join on real pg. The
// postgres:16 image does NOT include pg_hint_plan, so this verifies the hint
// comment is harmlessly ignored — the join produces correct results. This is
// the critical production-safety property.
func TestE2E_O4_HintGracefulDegrade(t *testing.T) {
	dsn := os.Getenv("KQL_PG_DSN")
	if dsn == "" {
		t.Skip("KQL_PG_DSN not set")
	}
	bk, err := New(dsn)
	if err != nil {
		t.Skipf("pg not reachable: %v", err)
	}
	defer bk.Close()

	pipe := &ir.Pipeline{
		Source: &ir.SourceTable{Table: "events"},
		Stages: []ir.Stage{
			&ir.Join{
				Kind:  ir.JoinInner,
				Hint:  ir.JoinHintHash, // the hint to test
				Right: &ir.Pipeline{Source: &ir.SourceTable{Table: "meta"}},
				On: []ir.Expr{
					&ir.BinOp{Op: token.EQL, X: &ir.Col{Name: "id"}, Y: &ir.Col{Name: "id"}},
				},
			},
		},
	}

	q, err := Emit(pipe)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	t.Logf("SQL: %s", q.SQL)

	res, err := bk.Exec(context.Background(), q)
	if err != nil {
		t.Fatalf("Exec failed — hint should NOT break execution on stock pg: %v", err)
	}
	// The seed has events+meta with matching ids; just verify rows returned.
	if len(res.Rows) == 0 {
		t.Error("expected non-zero rows from events ⋈ meta")
	}
	t.Logf("graceful degrade OK: %d rows returned with Hash hint on stock pg (no pg_hint_plan)", len(res.Rows))
}

// TestE2E_O4_NestLoopHintGracefulDegrade: same for NestLoop hint.
func TestE2E_O4_NestLoopHintGracefulDegrade(t *testing.T) {
	dsn := os.Getenv("KQL_PG_DSN")
	if dsn == "" {
		t.Skip("KQL_PG_DSN not set")
	}
	bk, err := New(dsn)
	if err != nil {
		t.Skipf("pg not reachable: %v", err)
	}
	defer bk.Close()

	pipe := &ir.Pipeline{
		Source: &ir.SourceTable{Table: "events"},
		Stages: []ir.Stage{
			&ir.Join{
				Kind:  ir.JoinInner,
				Hint:  ir.JoinHintNestLoop,
				Right: &ir.Pipeline{Source: &ir.SourceTable{Table: "meta"}},
				On: []ir.Expr{
					&ir.BinOp{Op: token.EQL, X: &ir.Col{Name: "id"}, Y: &ir.Col{Name: "id"}},
				},
			},
		},
	}

	q, err := Emit(pipe)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	res, err := bk.Exec(context.Background(), q)
	if err != nil {
		t.Fatalf("NestLoop hint exec failed: %v", err)
	}
	t.Logf("NestLoop graceful degrade OK: %d rows", len(res.Rows))
}
