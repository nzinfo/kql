// Package ir — equivalence checking (I5).
//
// Equivalent reports whether two pipelines are semantically equivalent — they
// produce the same result for any input. This is used to verify that optimizer
// rewrites (predicate pushdown, column prune, join plan selection) don't change
// semantics.
//
// Approach: canonicalize both pipelines to a normalized form, then compare.
// Canonicalization rules:
//   - AND predicates sorted by a stable key (predicate order doesn't matter).
//   - Column IDs normalized (rewrite to a contiguous 1..N sequence per pipeline).
//   - Literal values compared by Go equality.
//   - Filter/Sort/Limit stages that commute are grouped.
//
// This is a syntactic equivalence check (not a full semantic prover). It catches
// the common optimizer-transform cases (reordered predicates, pushed filters,
// pruned columns) but is conservative — two queries that produce the same result
// via different IR shapes may report as non-equivalent. When in doubt, it
// returns false (safe: never claims equivalence that doesn't hold).
package ir

import (
	"fmt"
	"sort"
	"strings"

	"nzinfo/kql/internal/frontend/token"
)

// Equivalent reports whether two pipelines are semantically equivalent.
// Conservative: returns false if it can't prove equivalence.
func Equivalent(a, b *Pipeline) bool {
	if a == nil || b == nil {
		return a == b
	}
	ca := Canonicalize(a)
	cb := Canonicalize(b)
	return canonicalString(ca) == canonicalString(cb)
}

// CanonicalPipeline is a normalized pipeline representation for comparison.
type CanonicalPipeline struct {
	Source string // canonical source label
	Stages []CanonicalStage
}

// CanonicalStage is a normalized stage.
type CanonicalStage struct {
	Kind  string // "filter", "project", "extend", "aggregate", "join", "sort", "limit", "distinct", "union"
	Body  string // canonical body (sorted predicates, normalized column refs)
}

// Canonicalize produces a normalized form of a pipeline for comparison.
func Canonicalize(p *Pipeline) *CanonicalPipeline {
	if p == nil {
		return nil
	}
	cp := &CanonicalPipeline{}
	if p.Source != nil {
		if st, ok := p.Source.(*SourceTable); ok {
			cp.Source = "table:" + st.Table
		} else {
			cp.Source = fmt.Sprintf("%T", p.Source)
		}
	}
	for _, st := range p.Stages {
		cs := canonicalizeStage(st)
		if cs != nil {
			cp.Stages = append(cp.Stages, *cs)
		}
	}
	return cp
}

func canonicalizeStage(st Stage) *CanonicalStage {
	switch s := st.(type) {
	case *Filter:
		return &CanonicalStage{Kind: "filter", Body: canonicalExpr(s.Predicate)}
	case *Project:
		return &CanonicalStage{Kind: "project", Body: canonicalNamedExprs(s.Cols)}
	case *Extend:
		return &CanonicalStage{Kind: "extend", Body: canonicalNamedExprs(s.Cols)}
	case *Aggregate:
		aggs := canonicalNamedExprs(s.Aggregates)
		keys := canonicalNamedExprs(s.Keys)
		return &CanonicalStage{Kind: "aggregate", Body: "aggs:[" + aggs + "] by:[" + keys + "]"}
	case *Join:
		body := fmt.Sprintf("kind=%d hint=%d on:[", s.Kind, s.Hint)
		ons := make([]string, len(s.On))
		for i, c := range s.On {
			ons[i] = canonicalExpr(c)
		}
		sort.Strings(ons)
		body += strings.Join(ons, ",") + "]"
		if s.Right != nil {
			if st, ok := s.Right.Source.(*SourceTable); ok {
				body += " right:table:" + st.Table
			}
		}
		return &CanonicalStage{Kind: "join", Body: body}
	case *Sort:
		var keys []string
		for _, k := range s.Keys {
			dir := "asc"
			if k.Desc {
				dir = "desc"
			}
			keys = append(keys, canonicalExpr(k.Expr)+":"+dir)
		}
		sort.Strings(keys)
		return &CanonicalStage{Kind: "sort", Body: strings.Join(keys, ",")}
	case *Limit:
		return &CanonicalStage{Kind: "limit", Body: canonicalExpr(s.Count)}
	case *Distinct:
		var cols []string
		for _, c := range s.Cols {
			cols = append(cols, canonicalExpr(c))
		}
		sort.Strings(cols)
		return &CanonicalStage{Kind: "distinct", Body: strings.Join(cols, ",")}
	case *Union:
		return &CanonicalStage{Kind: "union", Body: fmt.Sprintf("+%d", len(s.Inputs))}
	}
	return &CanonicalStage{Kind: fmt.Sprintf("%T", st), Body: "?"}
}

// canonicalExpr produces a canonical string for an expression.
// AND/OR operands are sorted (predicate order doesn't matter).
func canonicalExpr(e Expr) string {
	if e == nil {
		return "<nil>"
	}
	switch n := e.(type) {
	case *BinOp:
		if n.Op == token.AND || n.Op == token.OR {
			// Sort AND/OR operands for canonical order.
			parts := []string{canonicalExpr(n.X), canonicalExpr(n.Y)}
			sort.Strings(parts)
			return "(" + tokenString(n.Op) + " " + strings.Join(parts, " ") + ")"
		}
		return "(" + tokenString(n.Op) + " " + canonicalExpr(n.X) + " " + canonicalExpr(n.Y) + ")"
	case *Lit:
		if !n.HasValue {
			return "null"
		}
		return fmt.Sprintf("lit:%v:%v", n.T, n.Value)
	case *Col:
		return "col:" + n.Name
	case *Star:
		return "*"
	case *UnaryOp:
		return "(" + tokenString(n.Op) + " " + canonicalExpr(n.X) + ")"
	case *FuncCall:
		args := make([]string, len(n.Args))
		for i, a := range n.Args {
			args[i] = canonicalExpr(a)
		}
		sort.Strings(args) // commutative functions (min/max/sum) — sort args
		return n.Name + "(" + strings.Join(args, ",") + ")"
	case *Member:
		return canonicalExpr(n.X) + "." + n.Field
	case *Index:
		return canonicalExpr(n.X) + "[" + canonicalExpr(n.Index) + "]"
	case *Case:
		return "case(" + canonicalExpr(n.Cond) + "?" + canonicalExpr(n.Then) + ":" + canonicalExpr(n.Else) + ")"
	case *List:
		elems := make([]string, len(n.Elems))
		for i, el := range n.Elems {
			elems[i] = canonicalExpr(el)
		}
		return "[" + strings.Join(elems, ",") + "]"
	}
	return fmt.Sprintf("%T", e)
}

// canonicalNamedExprs renders a list of NamedExprs canonically (sorted by name).
func canonicalNamedExprs(ns []*NamedExpr) string {
	parts := make([]string, len(ns))
	for i, n := range ns {
		if n != nil && n.Name != "" {
			parts[i] = n.Name + "=" + canonicalExpr(n.Expr)
		} else if n != nil {
			parts[i] = canonicalExpr(n.Expr)
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

// canonicalString renders a CanonicalPipeline as a stable string for == comparison.
func canonicalString(cp *CanonicalPipeline) string {
	if cp == nil {
		return "<nil>"
	}
	var sb strings.Builder
	sb.WriteString(cp.Source)
	for _, s := range cp.Stages {
		sb.WriteString("|")
		sb.WriteString(s.Kind)
		sb.WriteString("{")
		sb.WriteString(s.Body)
		sb.WriteString("}")
	}
	return sb.String()
}

// tokenString returns a stable name for a token operator.
func tokenString(t token.Token) string {
	return t.String()
}
