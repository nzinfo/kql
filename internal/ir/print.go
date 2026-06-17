// Package ir — Print renders a Pipeline as an indented tree (I4.S1).
//
// This is the library-level pretty-printer, extracted from cmd/kql/ir.go so it
// can be used by any importer of the ir package (not just the CLI). The CLI's
// `kql explain` delegates here.
package ir

import (
	"fmt"
	"io"
	"strings"
)

// Print writes a human-readable indented tree of the pipeline to w.
// Format:
//
//	Pipeline
//	  Source: Table "events"
//	  Filter
//	    where (Col("x") > Lit(1))
//	  Extend (1 cols)
//	    y = Col("x")
func Print(w io.Writer, pipe *Pipeline) {
	if pipe == nil {
		fmt.Fprintln(w, "  (nil pipeline)")
		return
	}
	pp := &printer{w: w, depth: 0}
	pp.line("Pipeline")
	pp.depth++
	if pipe.Source != nil {
		pp.line("Source: " + DescribeSource(pipe.Source))
	} else {
		pp.line("Source: (none)")
	}
	for _, st := range pipe.Stages {
		pp.line(DescribeStage(st))
		pp.depth++
		describeStageBody(pp, st)
		pp.depth--
	}
	pp.depth--
}

// Sprint returns the printed pipeline as a string (convenience for tests).
func Sprint(pipe *Pipeline) string {
	var sb strings.Builder
	Print(&sb, pipe)
	return sb.String()
}

// printer tracks indentation depth.
type printer struct {
	w     io.Writer
	depth int
}

func (p *printer) line(s string) {
	fmt.Fprintf(p.w, "%s%s\n", strings.Repeat("  ", p.depth), s)
}

// DescribeSource returns a short label for a source node.
func DescribeSource(src Source) string {
	switch s := src.(type) {
	case *SourceTable:
		return fmt.Sprintf("Table %q", s.Table)
	case nil:
		return "(none)"
	}
	return fmt.Sprintf("%T", src)
}

// DescribeStage returns a one-line header for a stage.
func DescribeStage(st Stage) string {
	switch s := st.(type) {
	case *Filter:
		return "Filter"
	case *Project:
		return fmt.Sprintf("Project (%d cols)", len(s.Cols))
	case *Extend:
		return fmt.Sprintf("Extend (%d cols)", len(s.Cols))
	case *Aggregate:
		return fmt.Sprintf("Aggregate (%d aggs, %d keys)", len(s.Aggregates), len(s.Keys))
	case *Join:
		return fmt.Sprintf("Join (kind=%s, hint=%s)", JoinKindName(s.Kind), s.Hint)
	case *Sort:
		return fmt.Sprintf("Sort (%d keys)", len(s.Keys))
	case *Limit:
		return "Limit"
	case *Union:
		return fmt.Sprintf("Union (+%d)", len(s.Inputs))
	case *Distinct:
		return fmt.Sprintf("Distinct (%d cols)", len(s.Cols))
	}
	return fmt.Sprintf("%T", st)
}

func describeStageBody(p *printer, st Stage) {
	switch s := st.(type) {
	case *Filter:
		p.line("where " + DescribeExpr(s.Predicate))
	case *Project:
		for _, c := range s.Cols {
			p.line(DescribeNamedExpr(c))
		}
	case *Extend:
		for _, c := range s.Cols {
			p.line(DescribeNamedExpr(c))
		}
	case *Aggregate:
		for _, a := range s.Aggregates {
			p.line("agg " + DescribeNamedExpr(a))
		}
		for _, k := range s.Keys {
			p.line("by " + DescribeNamedExpr(k))
		}
	case *Sort:
		for _, k := range s.Keys {
			dir := "asc"
			if k.Desc {
				dir = "desc"
			}
			p.line(fmt.Sprintf("key %s (%s)", DescribeExpr(k.Expr), dir))
		}
	case *Limit:
		p.line("take " + DescribeExpr(s.Count))
	case *Join:
		if s.Right != nil {
			p.line("right: " + DescribeSource(s.Right.Source))
		}
		for _, c := range s.On {
			p.line("on " + DescribeExpr(c))
		}
	case *Distinct:
		for _, c := range s.Cols {
			p.line(DescribeExpr(c))
		}
	}
}

// DescribeExpr returns a compact one-line rendering of an IR expression.
func DescribeExpr(e Expr) string {
	if e == nil {
		return "<nil>"
	}
	switch n := e.(type) {
	case *Lit:
		if !n.HasValue {
			return "null"
		}
		return fmt.Sprintf("%v(%v)", n.T, n.Value)
	case *Col:
		if n.ColID.IsValid() {
			return fmt.Sprintf("Col#%d(%q)", n.ColID, n.Name)
		}
		return fmt.Sprintf("Col(%q)", n.Name)
	case *Star:
		return "*"
	case *BinOp:
		return fmt.Sprintf("(%s %s %s)", DescribeExpr(n.X), n.Op, DescribeExpr(n.Y))
	case *UnaryOp:
		return fmt.Sprintf("(%s%s)", n.Op, DescribeExpr(n.X))
	case *FuncCall:
		args := make([]string, 0, len(n.Args))
		for _, a := range n.Args {
			args = append(args, DescribeExpr(a))
		}
		agg := ""
		if n.Caps.Aggregate {
			agg = " [agg]"
		}
		return fmt.Sprintf("%s(%s)%s", n.Name, strings.Join(args, ", "), agg)
	case *Member:
		return fmt.Sprintf("%s.%s", DescribeExpr(n.X), n.Field)
	case *Index:
		return fmt.Sprintf("%s[%s]", DescribeExpr(n.X), DescribeExpr(n.Index))
	case *Case:
		return fmt.Sprintf("case(%s?%s:%s)", DescribeExpr(n.Cond), DescribeExpr(n.Then), DescribeExpr(n.Else))
	case *List:
		elems := make([]string, 0, len(n.Elems))
		for _, el := range n.Elems {
			elems = append(elems, DescribeExpr(el))
		}
		return "[" + strings.Join(elems, ", ") + "]"
	}
	return fmt.Sprintf("%T", e)
}

// DescribeNamedExpr renders a NamedExpr (bare or `name = expr`).
func DescribeNamedExpr(n *NamedExpr) string {
	if n == nil {
		return "<nil>"
	}
	if n.Name != "" {
		return fmt.Sprintf("%s = %s", n.Name, DescribeExpr(n.Expr))
	}
	return DescribeExpr(n.Expr)
}

// JoinKindName returns the KQL name for an IR JoinKind.
func JoinKindName(k JoinKind) string {
	switch k {
	case JoinInnerUnique:
		return "innerunique"
	case JoinInner:
		return "inner"
	case JoinLeftOuter:
		return "left"
	case JoinRightOuter:
		return "right"
	case JoinFullOuter:
		return "full"
	}
	return "default(innerunique)"
}
