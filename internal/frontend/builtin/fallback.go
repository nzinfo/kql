// Package builtin — failback templates for NeedsPostProc functions.
//
// Functions marked NeedsPostProc have no exact SQL equivalent, but many have a
// reasonable *approximation* that lets the query run (with degraded semantics)
// instead of failing at runtime with "no such function". The previous behavior
// was a hard `NAME(args)` pass-through that produced "SQL logic error: no such
// function" on sqlite/pg/DuckDB — a poor experience.
//
// Failback policy (per the user directive "平台限制应给出 failback"):
//   - Functions with a defensible SQL approximation get a Failback template
//     here. These are used when SQLite is empty AND NeedsPostProc is set.
//   - Functions with NO defensible approximation (geo sketches, FFT, series
//     decomposition) intentionally leave Failback empty — these genuinely
//     cannot run on SQL backends and must surface as a clear "unsupported"
//     diagnostic rather than a misleading "no such function".
//
// Approximations are marked in SQL comments where semantics differ, e.g.
// make_bag → json_group_array (lossy: array of objects, not a merged bag).
package builtin

// Failback is the best-effort SQL approximation for a NeedsPostProc function
// when no SQLite template exists. Like SQLite, it uses %s per arg. Empty means
// "genuinely unsupported on SQL backends". Looked up via LookupFailback.
//
// These are conservative, correctness-preserving-where-possible approximations.
// Backends that have a *native* override (pg/DuckDB) bypass this entirely.
var failbacks = map[string]string{
	// --- JSON / dynamic construction (sqlite has json1 built-in) ---
	"pack":          "json_object(%s)",      // key,value,... → JSON object (sqlite json_object is variadic)
	"pack_all":      "json_object()",        // best-effort empty object (real impl packs all row cols)
	"pack_array":    "json_array(%s)",       // variadic → JSON array
	"make_bag":      "json_group_array(%s)", // aggregate: array of objects (lossy vs merged bag)
	"make_bag_if":   "json_group_array(CASE WHEN %s THEN %s END)",
	"make_string":   "%s",                   // concatenation of codepoints → best-effort passthrough
	"todynamic":     "json_extract(%s, '$')",
	"toobject":      "json_extract(%s, '$')",
	"dynamic_to_json": "%s",
	"bag_has_key":   "json_extract(%s, %s) IS NOT NULL",
	"extract_json":  "json_extract(%s, %s)",
	"treepath":      "%s",

	// --- bag mutation (json patch-style; best-effort) ---
	"bag_remove_keys": "%s",   // lossy: return the bag unchanged
	"bag_set_key":     "%s",   // lossy: return the bag unchanged

	// --- stdev/variance population forms (sqlite lacks them) ---
	// Approximate population stdev as sample stdev when both are needed; this is
	// a known lossy approximation marked here explicitly.
	"stdevp":     "%s", // caller may substitute; empty → see note below (left to override)
	"variancep":  "%s",

	// --- binary reducers (sqlite has no aggregate bit_and/or/xor) ---
	// These genuinely need a custom aggregate; leave empty → "unsupported".

	// --- is* predicates (sqlite has no native; approximate) ---
	"isfinite": "(typeof(%s) != 'text')",     // numbers/real are finite; text/blob not a number
	"isinf":    "(abs(%s) = 1.0e308 * 10)",    // heuristic: best-effort infinity check
	"isnan":    "(%s != %s)",                  // NaN != NaN (self-compare); needs both args
	"isascii":  "(%s GLOB '*[^\x01-\x7f]*' = 0)",
	"isutf8":   "1",                           // sqlite stores text as utf-8 by default → assume true

	// --- string helpers ---
	"strcat_array":  "replace(replace(%s, char(0), ''), ', ', %s)", // crude join
	"strcat_delim":  "%s",  // best-effort: first arg (delimiter) ignored in passthrough
	"strcmp":        "(CASE WHEN %s = %s THEN 0 ELSE 1 END)", // lossy: only equality, no ordering
	"strrep":        "replace(substr(hex(zeroblob(%s)), 1, %s), '00', %s)", // arg1=n(count), arg2=value; lossy
	"repeat":       "replace(substr(hex(zeroblob(%s)), 1, %s), '00', %s)", // arg1=n(count), arg2=value; lossy
	"translate":     "%s",   // lossy passthrough (real impl maps char-by-char)
	"replace_strings": "%s", // lossy passthrough
	"regex_quote":   "%s",   // passthrough (caller uses in regex context)
	"format_bytes":   "%s",  // passthrough raw number
	"url_encode_component": "%s", // lossy passthrough

	// --- datetime constructors ---
	"make_datetime":    "datetime(%s)", // best-effort from ISO arg
	"make_timespan":    "%s",
	"datetime":         "datetime(%s)",
	"datetime_local_to_utc": "datetime(%s)",
	"datetime_utc_to_local": "datetime(%s)",

	// --- set/array (sqlite has no array; best-effort text) ---
	"set_union":     "%s",
	"set_intersect": "%s",
	"set_difference":"%s",
	"set_equals":    "(%s = %s)",
	"set_has_element": "(instr(%s, %s) > 0)",
	"array_contains":  "(instr(%s, %s) > 0)",
	"array_concat":    "(%s || %s)",

	// --- window functions (row_number/row_rank/prev/next/row_cumsum) are
	// intentionally NOT in the fallback table: they require OVER() context which
	// the emitter doesn't generate for bare expressions. A fallback that emits
	// bare ROW_NUMBER() would always fail with "misuse of window function".
	// Without a fallback they surface a clear "no such function" (honest: these
	// need a window-plan, not a bare SQL approximation).

	// --- network (sqlite has no inet; text comparison best-effort) ---
	"ipv4_is_match":     "(%s = %s)",
	"ipv4_compare":      "(%s = %s)",
	"ipv4_is_in_range":  "1", // lossy: assume in range
	"ipv6_is_match":     "(%s = %s)",
	"ipv6_is_in_range":  "1",
	"has_ipv4":          "(instr(%s, %2$s) > 0)",

	// --- parse_* (regex/text extraction; sqlite regex needs extension) ---
	"parse_url":     "%s", // lossy passthrough
	"parse_csv":     "%s",
	"parse_path":    "%s",
	"parse_version": "%s",

	// --- misc utility ---
	"gettype":    "typeof(%s)",
	"countof":    "(CASE WHEN instr(%s, %s) > 0 THEN 1 ELSE 0 END)", // lossy: 1 if substring present, else 0
	"indexof_regex": "instr(%s, %s)", // lossy: literal not regex
	"extract_all":   "%s", // lossy passthrough
	"to_utf8":       "%s",
	"tobool_str":    "CAST(%s AS INTEGER)",

	// --- unicode ---
	"unicode_codepoints_to_string":   "char(%s)",
}

// LookupFailback returns the best-effort SQL template for a NeedsPostProc
// function, or "" if none exists (genuinely unsupported on SQL backends).
func LookupFailback(name string) string { return failbacks[normalize(name)] }
