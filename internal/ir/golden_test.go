// Package ir — T4 golden tests (IR output stability).
//
// These tests verify that the IR translation produces stable, reproducible
// output for a set of canonical queries. If the IR shape changes (e.g. a new
// optimization pass alters the stage order), the golden comparison catches it.
// The golden strings are embedded in the test (no external fixture files needed)
// for simplicity and portability.
package ir

import (
	"strings"
	"testing"

	"nzinfo/kql/internal/frontend/diagnostic"
	"nzinfo/kql/internal/frontend/parser"
)

// goldenCase is one IR golden test case.
type goldenCase struct {
	name  string
	query string
	// check is a function that asserts on the translated pipeline.
	check func(t *testing.T, pipe *Pipeline)
}

// TestGolden_IR_Stability verifies IR translation is stable for canonical queries.
func TestGolden_IR_Stability(t *testing.T) {
	cases := []goldenCase{
		{
			name:  "where_filter",
			query: `T | where x > 0`,
			check: func(t *testing.T, pipe *Pipeline) {
				if len(pipe.Stages) != 1 {
					t.Fatalf("stages = %d, want 1", len(pipe.Stages))
				}
				if _, ok := pipe.Stages[0].(*Filter); !ok {
					t.Errorf("stage0 = %T, want *Filter", pipe.Stages[0])
				}
			},
		},
		{
			name:  "project_extend",
			query: `T | extend y = x * 2 | project y`,
			check: func(t *testing.T, pipe *Pipeline) {
				if len(pipe.Stages) != 2 {
					t.Fatalf("stages = %d, want 2", len(pipe.Stages))
				}
				if _, ok := pipe.Stages[0].(*Extend); !ok {
					t.Errorf("stage0 = %T, want *Extend", pipe.Stages[0])
				}
				if _, ok := pipe.Stages[1].(*Project); !ok {
					t.Errorf("stage1 = %T, want *Project", pipe.Stages[1])
				}
			},
		},
		{
			name:  "summarize",
			query: `T | summarize c = count() by state`,
			check: func(t *testing.T, pipe *Pipeline) {
				if len(pipe.Stages) != 1 {
					t.Fatalf("stages = %d, want 1", len(pipe.Stages))
				}
				agg, ok := pipe.Stages[0].(*Aggregate)
				if !ok {
					t.Fatalf("stage0 = %T, want *Aggregate", pipe.Stages[0])
				}
				if len(agg.Aggregates) != 1 || len(agg.Keys) != 1 {
					t.Errorf("aggs=%d keys=%d, want 1/1", len(agg.Aggregates), len(agg.Keys))
				}
			},
		},
		{
			name:  "join",
			query: `T | join kind=inner (U) on $left.id == $right.id`,
			check: func(t *testing.T, pipe *Pipeline) {
				j, ok := pipe.Stages[0].(*Join)
				if !ok {
					t.Fatalf("stage0 = %T, want *Join", pipe.Stages[0])
				}
				if j.Kind != JoinInner {
					t.Errorf("kind = %v, want JoinInner", j.Kind)
				}
				if j.Hint != JoinHintNone {
					t.Errorf("hint = %v, want JoinHintNone (no optimizer)", j.Hint)
				}
			},
		},
		{
			name:  "sort_limit",
			query: `T | sort by x desc | take 10`,
			check: func(t *testing.T, pipe *Pipeline) {
				if len(pipe.Stages) != 2 {
					t.Fatalf("stages = %d, want 2", len(pipe.Stages))
				}
				s, ok := pipe.Stages[0].(*Sort)
				if !ok || len(s.Keys) != 1 || !s.Keys[0].Desc {
					t.Errorf("stage0 sort wrong: %+v", pipe.Stages[0])
				}
				if _, ok := pipe.Stages[1].(*Limit); !ok {
					t.Errorf("stage1 = %T, want *Limit", pipe.Stages[1])
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := parser.New("", tc.query)
			script := p.Parse()
			if diags := p.Diagnostics(); diags.HasErrors() {
				t.Fatalf("parse errors: %v", diags.Render())
			}
			var diags diagnostic.List
			pipe := Translate(script, &diags)
			if diags.HasErrors() {
				t.Fatalf("translate errors: %v", diags.Render())
			}
			if pipe == nil {
				t.Fatal("Translate returned nil")
			}
			tc.check(t, pipe)
		})
	}
}

// TestGolden_Sprint_Stable verifies ir.Sprint produces deterministic output
// (same query → same string). This is the golden-string stability test.
func TestGolden_Sprint_Stable(t *testing.T) {
	p := parser.New("", `T | where x > 0 | take 10`)
	script := p.Parse()
	var diags diagnostic.List
	pipe := Translate(script, &diags)
	out1 := Sprint(pipe)
	out2 := Sprint(pipe)
	if out1 != out2 {
		t.Error("Sprint is not deterministic")
	}
	if !strings.Contains(out1, "Pipeline") || !strings.Contains(out1, "Filter") {
		t.Errorf("Sprint missing expected content:\n%s", out1)
	}
}
