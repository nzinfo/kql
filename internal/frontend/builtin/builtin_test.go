package builtin

import "testing"

func TestLookup(t *testing.T) {
	// case-insensitive
	for _, name := range []string{"ago", "AGO", "Ago", "tostring", "TOSTRING"} {
		if Lookup(name) == nil {
			t.Errorf("Lookup(%q) = nil, want a spec", name)
		}
	}
	if Lookup("definitely_not_a_function") != nil {
		t.Error("unknown function should return nil")
	}
}

func TestSpecFields(t *testing.T) {
	cases := map[string]struct {
		agg     bool
		sqlite  string
		minArgs int
	}{
		"ago":       {false, "(datetime('now', '-' || (%s)))", 1},
		"tostring":  {false, "CAST(%s AS TEXT)", 1},
		"iff":       {false, "CASE WHEN %s THEN %s ELSE %s END", 3},
		"sum":       {true, "SUM(%s)", 1},
		"dcount":    {true, "COUNT(DISTINCT %s)", 1},
		"countif":   {true, "SUM(CASE WHEN %s THEN 1 ELSE 0 END)", 1},
		"strcat":    {false, StrcatTpl, 1},
		"coalesce":  {false, "coalesce(%s)", 1},
		"make_set":  {true, "group_concat(DISTINCT %s)", 1},
		"isnotempty": {false, "(%s != '')", 1},
	}
	for name, want := range cases {
		s := Lookup(name)
		if s == nil {
			t.Errorf("%s: not in catalog", name)
			continue
		}
		if s.IsAggregate != want.agg {
			t.Errorf("%s: IsAggregate = %v, want %v", name, s.IsAggregate, want.agg)
		}
		if s.SQLite != want.sqlite {
			t.Errorf("%s: SQLite = %q, want %q", name, s.SQLite, want.sqlite)
		}
		if s.MinArgs != want.minArgs {
			t.Errorf("%s: MinArgs = %d, want %d", name, s.MinArgs, want.minArgs)
		}
	}
}

func TestNeedsPostProc(t *testing.T) {
	// Functions sqlite can't compute in SQL should be flagged.
	for _, name := range []string{"split", "extract", "make_set", "percentile"} {
		s := Lookup(name)
		if s == nil {
			t.Errorf("%s: not in catalog", name)
			continue
		}
		if !s.NeedsPostProc && s.SQLite == "" {
			t.Errorf("%s: expected NeedsPostProc or no-translation, got SQLite=%q NeedsPostProc=%v",
				name, s.SQLite, s.NeedsPostProc)
		}
	}
}

func TestVariadicArity(t *testing.T) {
	s := Lookup("strcat")
	if s.MaxArgs >= 0 {
		t.Errorf("strcat MaxArgs = %d, want < 0 (variadic)", s.MaxArgs)
	}
	s = Lookup("sum")
	if s.MaxArgs != 1 {
		t.Errorf("sum MaxArgs = %d, want 1", s.MaxArgs)
	}
}
