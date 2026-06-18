// Package ir — YAML dump (I4.S2).
//
// SprintYAML renders a pipeline as a YAML-like nested structure for Explain
// output that's more machine-readable than the indented tree (I4.S1). This is
// an optional path — the core execution pipeline never calls it; only the
// Explain CLI uses it when explicitly requested (--format yaml).
package ir

import (
	"fmt"
	"strings"
)

// SprintYAML returns a YAML-like rendering of the pipeline (indented, structured).
func SprintYAML(pipe *Pipeline) string {
	var sb strings.Builder
	dumpYAML(&sb, pipe, 0)
	return sb.String()
}

func dumpYAML(sb *strings.Builder, pipe *Pipeline, depth int) {
	indent := strings.Repeat("  ", depth)
	if pipe == nil {
		fmt.Fprintf(sb, "%spipeline: null\n", indent)
		return
	}
	fmt.Fprintf(sb, "%spipeline:\n", indent)
	d := depth + 1
	di := strings.Repeat("  ", d)
	if pipe.Source != nil {
		if st, ok := pipe.Source.(*SourceTable); ok {
			fmt.Fprintf(sb, "%ssource:\n%s  table: %q\n", di, di, st.Table)
		}
	}
	if len(pipe.Stages) > 0 {
		fmt.Fprintf(sb, "%sstages:\n", di)
		for _, st := range pipe.Stages {
			fmt.Fprintf(sb, "%s  - kind: %q\n", di, stageKindYAML(st))
			dumpStageYAML(sb, st, d+2)
		}
	}
}

func stageKindYAML(st Stage) string {
	switch st.(type) {
	case *Filter:
		return "filter"
	case *Project:
		return "project"
	case *Extend:
		return "extend"
	case *Aggregate:
		return "aggregate"
	case *Join:
		return "join"
	case *Sort:
		return "sort"
	case *Limit:
		return "limit"
	case *Distinct:
		return "distinct"
	case *Union:
		return "union"
	}
	return "unknown"
}

func dumpStageYAML(sb *strings.Builder, st Stage, depth int) {
	di := strings.Repeat("  ", depth)
	switch s := st.(type) {
	case *Filter:
		fmt.Fprintf(sb, "%spredicate: %q\n", di, DescribeExpr(s.Predicate))
	case *Project:
		fmt.Fprintf(sb, "%scolumns:\n", di)
		for _, c := range s.Cols {
			fmt.Fprintf(sb, "%s  - %s\n", di, DescribeNamedExpr(c))
		}
	case *Extend:
		fmt.Fprintf(sb, "%scolumns:\n", di)
		for _, c := range s.Cols {
			fmt.Fprintf(sb, "%s  - %s\n", di, DescribeNamedExpr(c))
		}
	case *Aggregate:
		fmt.Fprintf(sb, "%saggregates:\n", di)
		for _, a := range s.Aggregates {
			fmt.Fprintf(sb, "%s  - %s\n", di, DescribeNamedExpr(a))
		}
		if len(s.Keys) > 0 {
			fmt.Fprintf(sb, "%skeys:\n", di)
			for _, k := range s.Keys {
				fmt.Fprintf(sb, "%s  - %s\n", di, DescribeNamedExpr(k))
			}
		}
	case *Join:
		fmt.Fprintf(sb, "%skind: %s\n", di, JoinKindName(s.Kind))
		fmt.Fprintf(sb, "%shint: %s\n", di, s.Hint)
		if s.Right != nil {
			if st, ok := s.Right.Source.(*SourceTable); ok {
				fmt.Fprintf(sb, "%sright: %q\n", di, st.Table)
			}
		}
	case *Limit:
		fmt.Fprintf(sb, "%scount: %q\n", di, DescribeExpr(s.Count))
	}
}
