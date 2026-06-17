package main

import (
	"fmt"
	"io"
	"strings"

	"nzinfo/kql/internal/ir"
)

// printIR renders an IR Pipeline as an indented tree to w. Used by `kql explain`.
// It walks source + stages and prints one line per node with depth indentation.
func printIR(w io.Writer, pipe *ir.Pipeline) {
	if pipe == nil {
		fmt.Fprintln(w, "  (nil pipeline)")
		return
	}
	pp := &irPrinter{w: w, depth: 0}
	pp.line("Pipeline")
	pp.depth++
	if pipe.Source != nil {
		pp.line("Source: " + describeSource(pipe.Source))
	} else {
		pp.line("Source: (none)")
	}
	for _, st := range pipe.Stages {
		pp.line(describeStage(st))
		pp.depth++
		describeStageBody(pp, st)
		pp.depth--
	}
	pp.depth--
}

// irPrinter tracks indentation depth for nested IR output.
type irPrinter struct {
	w     io.Writer
	depth int
}

// line writes one indented line.
func (p *irPrinter) line(s string) {
	fmt.Fprintf(p.w, "%s%s\n", strings.Repeat("  ", p.depth), s)
}

// describeSource returns a short label for a source node.
func describeSource(src ir.Source) string {
	switch s := src.(type) {
	case *ir.SourceTable:
		return fmt.Sprintf("Table %q", s.Table)
	case nil:
		return "(none)"
	}
	return fmt.Sprintf("%T", src)
}

// describeStage returns a one-line header for a stage.
func describeStage(st ir.Stage) string {
	switch s := st.(type) {
	case *ir.Filter:
		return "Filter"
	case *ir.Project:
		return fmt.Sprintf("Project (%d cols)", len(s.Cols))
	case *ir.Extend:
		return fmt.Sprintf("Extend (%d cols)", len(s.Cols))
	case *ir.Aggregate:
		return fmt.Sprintf("Aggregate (%d aggs, %d keys)", len(s.Aggregates), len(s.Keys))
	case *ir.Join:
		return fmt.Sprintf("Join (kind=%s)", joinKindName(s.Kind))
	case *ir.Sort:
		return fmt.Sprintf("Sort (%d keys)", len(s.Keys))
	case *ir.Limit:
		return "Limit"
	case *ir.Union:
		return fmt.Sprintf("Union (+%d)", len(s.Inputs))
	case *ir.Distinct:
		return fmt.Sprintf("Distinct (%d cols)", len(s.Cols))
	}
	return fmt.Sprintf("%T", st)
}

// describeStageBody prints the stage's children (expressions, keys) indented.
func describeStageBody(p *irPrinter, st ir.Stage) {
	switch s := st.(type) {
	case *ir.Filter:
		p.line("where " + describeExpr(s.Predicate))
	case *ir.Project:
		for _, c := range s.Cols {
			p.line(describeNamedExpr(c))
		}
	case *ir.Extend:
		for _, c := range s.Cols {
			p.line(describeNamedExpr(c))
		}
	case *ir.Aggregate:
		for _, a := range s.Aggregates {
			p.line("agg " + describeNamedExpr(a))
		}
		for _, k := range s.Keys {
			p.line("by " + describeNamedExpr(k))
		}
	case *ir.Sort:
		for _, k := range s.Keys {
			dir := "asc"
			if k.Desc {
				dir = "desc"
			}
			p.line(fmt.Sprintf("key %s (%s)", describeExpr(k.Expr), dir))
		}
	case *ir.Limit:
		p.line("take " + describeExpr(s.Count))
	case *ir.Join:
		if s.Right != nil {
			p.line("right: " + describeSource(s.Right.Source))
		}
		for _, c := range s.On {
			p.line("on " + describeExpr(c))
		}
	case *ir.Distinct:
		for _, c := range s.Cols {
			p.line(describeExpr(c))
		}
	}
}

// describeExpr returns a compact one-line rendering of an IR expression.
func describeExpr(e ir.Expr) string {
	if e == nil {
		return "<nil>"
	}
	switch n := e.(type) {
	case *ir.Lit:
		if !n.HasValue {
			return "null"
		}
		return fmt.Sprintf("%v(%v)", n.T, n.Value)
	case *ir.Col:
		if n.ColID.IsValid() {
			return fmt.Sprintf("Col#%d(%q)", n.ColID, n.Name)
		}
		return fmt.Sprintf("Col(%q)", n.Name)
	case *ir.Star:
		return "*"
	case *ir.BinOp:
		return fmt.Sprintf("(%s %s %s)", describeExpr(n.X), n.Op, describeExpr(n.Y))
	case *ir.UnaryOp:
		return fmt.Sprintf("(%s%s)", n.Op, describeExpr(n.X))
	case *ir.FuncCall:
		args := make([]string, 0, len(n.Args))
		for _, a := range n.Args {
			args = append(args, describeExpr(a))
		}
		agg := ""
		if n.Caps.Aggregate {
			agg = " [agg]"
		}
		return fmt.Sprintf("%s(%s)%s", n.Name, strings.Join(args, ", "), agg)
	case *ir.Member:
		return fmt.Sprintf("%s.%s", describeExpr(n.X), n.Field)
	case *ir.Index:
		return fmt.Sprintf("%s[%s]", describeExpr(n.X), describeExpr(n.Index))
	case *ir.Case:
		return fmt.Sprintf("case(%s?%s:%s)", describeExpr(n.Cond), describeExpr(n.Then), describeExpr(n.Else))
	case *ir.List:
		elems := make([]string, 0, len(n.Elems))
		for _, el := range n.Elems {
			elems = append(elems, describeExpr(el))
		}
		return "[" + strings.Join(elems, ", ") + "]"
	}
	return fmt.Sprintf("%T", e)
}

// describeNamedExpr renders a NamedExpr (bare or `name = expr`).
func describeNamedExpr(n *ir.NamedExpr) string {
	if n == nil {
		return "<nil>"
	}
	if n.Name != "" {
		return fmt.Sprintf("%s = %s", n.Name, describeExpr(n.Expr))
	}
	return describeExpr(n.Expr)
}

// joinKindName returns the KQL name for an IR JoinKind.
func joinKindName(k ir.JoinKind) string {
	switch k {
	case ir.JoinInnerUnique:
		return "innerunique"
	case ir.JoinInner:
		return "inner"
	case ir.JoinLeftOuter:
		return "left"
	case ir.JoinRightOuter:
		return "right"
	case ir.JoinFullOuter:
		return "full"
	}
	return "default(innerunique)"
}
