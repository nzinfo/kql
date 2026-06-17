// Package builtin is the catalog of KQL built-in functions: their arity,
// capability bits, and (for the minimal loop) a per-dialect SQL translation.
//
// Source authority: KQL's documented function surface, cross-checked against
// .source-projects/kqlparser/builtin/functions.go (the template's ~380-entry
// table) and the real-world corpus usage (see pkg/kql/testdata/corpus/).
//
// This is the F7 backbone. The minimal loop wires the SQLite translations into
// the emit layer so common KQL scalar/aggregate calls produce valid SQLite SQL
// instead of a blind UPPER(name) pass-through. Functions WITHOUT a translation
// still pass through (best-effort) and are flagged NeedsPostProc where the
// catalog knows they can't be plain SQL.
package builtin

// Spec describes one built-in function.
type Spec struct {
	Name string // canonical lowercase name

	// Arity: Min/Max args. Max < 0 means variadic.
	MinArgs int
	MaxArgs int

	// IsAggregate marks aggregate functions (used in GROUP BY position).
	IsAggregate bool

	// SQLite is the SQLite SQL template, or "" if no direct SQL translation
	// exists (the function then passes through best-effort / needs post-proc).
	// The template uses %s for each argument's emitted SQL, in order. For
	// example strcat(a,b) → "(a || b)" uses SQLite string concatenation.
	SQLite string

	// NeedsPostProc marks functions that cannot be computed in SQL on the
	// target backend and must be done client-side (sqlite lacks them).
	NeedsPostProc bool
}

// Lookup returns the Spec for a function name (case-insensitive), or nil if
// the function is not in the catalog. Unknown functions are handled by the
// emit layer (best-effort pass-through).
func Lookup(name string) *Spec {
	if s, ok := catalog[normalize(name)]; ok {
		return &s
	}
	return nil
}

// catalog is the function table. Keep entries grouped by category and sorted
// alphabetically within a group for ease of maintenance. SQLite translations
// are the pragmatic minimal-loop mapping; correctness over completeness.
var catalog = func() map[string]Spec {
	m := map[string]Spec{}
	add := func(s Spec) { m[normalize(s.Name)] = s }

	// --- Time ---
	add(Spec{Name: "ago", MinArgs: 1, MaxArgs: 1,
		SQLite: "(datetime('now', '-' || (%s)))"}) // ago(1d) ~ now - interval; sqlite uses modifiers
	add(Spec{Name: "now", MinArgs: 0, MaxArgs: 0, SQLite: "datetime('now')"})
	add(Spec{Name: "datetime_add", MinArgs: 2, MaxArgs: 2, SQLite: "datetime(%s, %s)"})
	add(Spec{Name: "datetime_diff", MinArgs: 3, MaxArgs: 3, SQLite: "0"}) // approx

	// --- String ---
	add(Spec{Name: "tostring", MinArgs: 1, MaxArgs: 1, SQLite: "CAST(%s AS TEXT)"})
	add(Spec{Name: "strcat", MinArgs: 1, MaxArgs: -1, SQLite: StrcatTpl}) // variadic → || chain
	add(Spec{Name: "strcat_delim", MinArgs: 2, MaxArgs: -1, SQLite: ""})  // needs join helper
	add(Spec{Name: "substring", MinArgs: 2, MaxArgs: 3, SQLite: "substr(%s, %s, %s)"})
	add(Spec{Name: "strlen", MinArgs: 1, MaxArgs: 1, SQLite: "length(%s)"})
	add(Spec{Name: "toupper", MinArgs: 1, MaxArgs: 1, SQLite: "upper(%s)"})
	add(Spec{Name: "tolower", MinArgs: 1, MaxArgs: 1, SQLite: "lower(%s)"})
	add(Spec{Name: "trim", MinArgs: 1, MaxArgs: 1, SQLite: "trim(%s)"})
	add(Spec{Name: "trim_start", MinArgs: 1, MaxArgs: 2, SQLite: "ltrim(%s, %s)"})
	add(Spec{Name: "trim_end", MinArgs: 1, MaxArgs: 2, SQLite: "rtrim(%s, %s)"})
	add(Spec{Name: "indexof", MinArgs: 2, MaxArgs: 2, SQLite: "instr(%s, %s)"})
	add(Spec{Name: "split", MinArgs: 2, MaxArgs: 2, SQLite: "", NeedsPostProc: true}) // no array type in sqlite
	add(Spec{Name: "replace_string", MinArgs: 3, MaxArgs: 3, SQLite: "replace(%s, %s, %s)"})
	add(Spec{Name: "extract", MinArgs: 2, MaxArgs: 3, SQLite: "regexp_extract", NeedsPostProc: true}) // sqlite regex needs ext

	// --- Numeric ---
	add(Spec{Name: "toint", MinArgs: 1, MaxArgs: 1, SQLite: "CAST(%s AS INTEGER)"})
	add(Spec{Name: "tolong", MinArgs: 1, MaxArgs: 1, SQLite: "CAST(%s AS INTEGER)"})
	add(Spec{Name: "toreal", MinArgs: 1, MaxArgs: 1, SQLite: "CAST(%s AS REAL)"})
	add(Spec{Name: "tobool", MinArgs: 1, MaxArgs: 1, SQLite: "CAST(%s AS INTEGER)"})
	add(Spec{Name: "abs", MinArgs: 1, MaxArgs: 1, SQLite: "abs(%s)"})
	add(Spec{Name: "sqrt", MinArgs: 1, MaxArgs: 1, SQLite: "sqrt(%s)"})
	add(Spec{Name: "pow", MinArgs: 2, MaxArgs: 2, SQLite: "pow(%s, %s)"})
	add(Spec{Name: "exp", MinArgs: 1, MaxArgs: 1, SQLite: "exp(%s)"})
	add(Spec{Name: "log", MinArgs: 1, MaxArgs: 2, SQLite: "log(%s)"})
	add(Spec{Name: "floor", MinArgs: 1, MaxArgs: 1, SQLite: "floor(%s)"})
	add(Spec{Name: "ceiling", MinArgs: 1, MaxArgs: 1, SQLite: "ceil(%s)"})
	add(Spec{Name: "round", MinArgs: 1, MaxArgs: 2, SQLite: "round(%s)"})
	add(Spec{Name: "sign", MinArgs: 1, MaxArgs: 1, SQLite: "sign(%s)"})

	// --- Null / conditional ---
	add(Spec{Name: "iff", MinArgs: 3, MaxArgs: 3, SQLite: "CASE WHEN %s THEN %s ELSE %s END"})
	add(Spec{Name: "iif", MinArgs: 3, MaxArgs: 3, SQLite: "CASE WHEN %s THEN %s ELSE %s END"}) // alias
	add(Spec{Name: "coalesce", MinArgs: 1, MaxArgs: -1, SQLite: "coalesce(%s)"}) // variadic: join
	add(Spec{Name: "isnull", MinArgs: 1, MaxArgs: 1, SQLite: "(%s IS NULL)"})
	add(Spec{Name: "isnotnull", MinArgs: 1, MaxArgs: 1, SQLite: "(%s IS NOT NULL)"})
	add(Spec{Name: "isempty", MinArgs: 1, MaxArgs: 1, SQLite: "(%s = '')"})
	add(Spec{Name: "isnotempty", MinArgs: 1, MaxArgs: 1, SQLite: "(%s != '')"})
	add(Spec{Name: "isempty_str", MinArgs: 1, MaxArgs: 1, SQLite: "(%s = '')"})

	// --- Dynamic / JSON ---
	add(Spec{Name: "dynamic", MinArgs: 1, MaxArgs: 1, SQLite: "%s"}) // pass-through (json text)
	add(Spec{Name: "parse_json", MinArgs: 1, MaxArgs: 1, SQLite: "%s"})
	add(Spec{Name: "tojson", MinArgs: 1, MaxArgs: 1, SQLite: "%s"}) // sqlite has no tojson w/o ext
	add(Spec{Name: "array_length", MinArgs: 1, MaxArgs: 1, SQLite: "json_array_length(%s)"})
	add(Spec{Name: "bag_keys", MinArgs: 1, MaxArgs: 1, SQLite: "", NeedsPostProc: true})

	// --- Schema helpers ---
	add(Spec{Name: "column_ifexists", MinArgs: 2, MaxArgs: 2, SQLite: "%s"}) // approx: use the first arg

	// --- Aggregates (IsAggregate = true) ---
	add(Spec{Name: "sum", MinArgs: 1, MaxArgs: 1, IsAggregate: true, SQLite: "SUM(%s)"})
	add(Spec{Name: "avg", MinArgs: 1, MaxArgs: 1, IsAggregate: true, SQLite: "AVG(%s)"})
	add(Spec{Name: "min", MinArgs: 1, MaxArgs: 1, IsAggregate: true, SQLite: "MIN(%s)"})
	add(Spec{Name: "max", MinArgs: 1, MaxArgs: 1, IsAggregate: true, SQLite: "MAX(%s)"})
	add(Spec{Name: "dcount", MinArgs: 1, MaxArgs: 2, IsAggregate: true, SQLite: "COUNT(DISTINCT %s)"})
	add(Spec{Name: "countif", MinArgs: 1, MaxArgs: 1, IsAggregate: true, SQLite: "SUM(CASE WHEN %s THEN 1 ELSE 0 END)"})
	add(Spec{Name: "make_set", MinArgs: 1, MaxArgs: 2, IsAggregate: true, SQLite: "group_concat(DISTINCT %s)"})
	add(Spec{Name: "makeset", MinArgs: 1, MaxArgs: 2, IsAggregate: true, SQLite: "group_concat(DISTINCT %s)"})
	add(Spec{Name: "make_list", MinArgs: 1, MaxArgs: 2, IsAggregate: true, SQLite: "group_concat(%s)"})
	add(Spec{Name: "makelist", MinArgs: 1, MaxArgs: 2, IsAggregate: true, SQLite: "group_concat(%s)"})
	add(Spec{Name: "percentile", MinArgs: 2, MaxArgs: 2, IsAggregate: true, SQLite: "", NeedsPostProc: true})
	add(Spec{Name: "stdev", MinArgs: 1, MaxArgs: 1, IsAggregate: true, SQLite: ""}) // sqlite lacks stdev by default

	return m
}()

// StrcatTpl is a sentinel; the emit layer special-cases variadic strcat into a
// "||"-joined expression (the %s template form can't express N args).
const StrcatTpl = "__STRCAT_VARIADIC__"

// normalize lowercases a function name for catalog lookup. KQL function names
// are case-insensitive.
func normalize(name string) string {
	b := make([]byte, len(name))
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c >= 'A' && c <= 'Z' {
			c += 32
		}
		b[i] = c
	}
	return string(b)
}


func init() {
	// Round 2: additional high-frequency functions from corpus + KQL stdlib.
	adds := []Spec{
		{Name: "case", MinArgs: 3, MaxArgs: -1, SQLite: "CASE"},
		{Name: "replace_regex", MinArgs: 3, MaxArgs: 3, NeedsPostProc: true},
		{Name: "reverse", MinArgs: 1, MaxArgs: 1, SQLite: "reverse(%s)"},
		{Name: "url_encode", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "url_decode", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "base64_encode_tostring", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "base64_decode_tostring", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "format_datetime", MinArgs: 2, MaxArgs: 2, SQLite: "strftime(%s, %s)"},
		{Name: "format_timespan", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		{Name: "datetime_part", MinArgs: 2, MaxArgs: 2, SQLite: "strftime(%s, %s)"},
		{Name: "dayofmonth", MinArgs: 1, MaxArgs: 1, SQLite: "strftime('%d', %s)"},
		{Name: "dayofweek", MinArgs: 1, MaxArgs: 1, SQLite: "strftime('%w', %s)"},
		{Name: "dayofyear", MinArgs: 1, MaxArgs: 1, SQLite: "strftime('%j', %s)"},
		{Name: "monthofyear", MinArgs: 1, MaxArgs: 1, SQLite: "strftime('%m', %s)"},
		{Name: "getmonth", MinArgs: 1, MaxArgs: 1, SQLite: "strftime('%m', %s)"},
		{Name: "getyear", MinArgs: 1, MaxArgs: 1, SQLite: "strftime('%Y', %s)"},
		{Name: "hourofday", MinArgs: 1, MaxArgs: 1, SQLite: "strftime('%H', %s)"},
		{Name: "second", MinArgs: 1, MaxArgs: 1, SQLite: "strftime('%S', %s)"},
		{Name: "minute", MinArgs: 1, MaxArgs: 1, SQLite: "strftime('%M', %s)"},
		{Name: "hash_sha256", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "hash_md5", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "hash", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "ipv4_is_private", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "isipv4private", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "parse_ipv4", MinArgs: 1, MaxArgs: 1},
		{Name: "set_union", MinArgs: 2, MaxArgs: -1, NeedsPostProc: true},
		{Name: "set_intersect", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		{Name: "set_has_element", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		{Name: "array_contains", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		{Name: "pack_dictionary", MinArgs: 1, MaxArgs: -1, NeedsPostProc: true},
		{Name: "pi", MinArgs: 0, MaxArgs: 0, SQLite: "3.141592653589793"},
		{Name: "rand", MinArgs: 0, MaxArgs: 2, SQLite: "random()"},
		{Name: "exp10", MinArgs: 1, MaxArgs: 1, SQLite: "pow(10, %s)"},
		{Name: "log10", MinArgs: 1, MaxArgs: 1, SQLite: "log(%s, 10)"},
		{Name: "log2", MinArgs: 1, MaxArgs: 1, SQLite: "log(%s, 2)"},
		{Name: "gamma", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "variance", MinArgs: 1, MaxArgs: 1, IsAggregate: true, NeedsPostProc: true},
		{Name: "stdevif", MinArgs: 2, MaxArgs: 2, IsAggregate: true, NeedsPostProc: true},
		{Name: "percentilew", MinArgs: 2, MaxArgs: 2, IsAggregate: true, NeedsPostProc: true},
		{Name: "percentilesw", MinArgs: 3, MaxArgs: -1, IsAggregate: true, NeedsPostProc: true},
		{Name: "sumif", MinArgs: 2, MaxArgs: 2, IsAggregate: true, SQLite: "SUMIF"},
		{Name: "avgif", MinArgs: 2, MaxArgs: 2, IsAggregate: true, SQLite: "AVGIF"},
		{Name: "maxif", MinArgs: 2, MaxArgs: 2, IsAggregate: true, SQLite: "MAXIF"},
		{Name: "minif", MinArgs: 2, MaxArgs: 2, IsAggregate: true, SQLite: "MINIF"},
		{Name: "extend_json", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		{Name: "json_flatten", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
	}
	for _, s := range adds {
		catalog[normalize(s.Name)] = s
	}
}
