// Package builtin — Round 4 high-value scalar functions (CROSS-PROJECT-COMPARISON.md §2.3/§四).
//
// These close the gap vs kqlparser's 386-function table. Templates are
// best-effort SQLite forms (pg/DuckDB override where their names differ).
// Functions without a portable SQL form set NeedsPostProc so they still
// parse + translate (best-effort passthrough) rather than failing to resolve.
//
// Categories covered: to* conversions, math/trig, row_* window functions,
// max_of/min_of/notnull, set_*, pack/zip, make_* constructors, strcat_array,
// strcmp/strrep/translate/repeat/replace_strings, datetime constructors,
// unicode/unixtime, is* predicates, and misc utilities.
package builtin

func init() {
	adds := []Spec{
		// --- to* conversions (cover the full KQL cast family) ---
		{Name: "todynamic", MinArgs: 1, MaxArgs: 1, SQLite: "%s"},      // json text pass-through
		{Name: "toobject", MinArgs: 1, MaxArgs: 1, SQLite: "%s"},       // alias of todynamic
		{Name: "parse_json", MinArgs: 1, MaxArgs: 1, SQLite: "%s"},     // already registered; kept for completeness
		{Name: "toguid", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},  // sqlite has no guid type
		{Name: "todatetime", MinArgs: 1, MaxArgs: 1, SQLite: "datetime(%s)"},
		{Name: "totimespan", MinArgs: 1, MaxArgs: 1, SQLite: "%s"},
		{Name: "totime", MinArgs: 1, MaxArgs: 1, SQLite: "time(%s)"},
		{Name: "tobool_str", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "to_utf8", MinArgs: 1, MaxArgs: 1, SQLite: "%s"},
		{Name: "make_string", MinArgs: 1, MaxArgs: -1, NeedsPostProc: true},

		// --- Math: trigonometry + misc ---
		{Name: "acos", MinArgs: 1, MaxArgs: 1, SQLite: "acos(%s)"},
		{Name: "asin", MinArgs: 1, MaxArgs: 1, SQLite: "asin(%s)"},
		{Name: "atan", MinArgs: 1, MaxArgs: 1, SQLite: "atan(%s)"},
		{Name: "atan2", MinArgs: 2, MaxArgs: 2, SQLite: "atan2(%s, %s)"},
		{Name: "cos", MinArgs: 1, MaxArgs: 1, SQLite: "cos(%s)"},
		{Name: "sin", MinArgs: 1, MaxArgs: 1, SQLite: "sin(%s)"},
		{Name: "tan", MinArgs: 1, MaxArgs: 1, SQLite: "tan(%s)"},
		{Name: "cot", MinArgs: 1, MaxArgs: 1, SQLite: "(1.0 / tan(%s))"},
		{Name: "degrees", MinArgs: 1, MaxArgs: 1, SQLite: "degrees(%s)"},
		{Name: "radians", MinArgs: 1, MaxArgs: 1, SQLite: "radians(%s)"},
		{Name: "exp2", MinArgs: 1, MaxArgs: 1, SQLite: "pow(2, %s)"},
		{Name: "erf", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},    // no sqlite/pg built-in
		{Name: "erfc", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "loggamma", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "beta_cdf", MinArgs: 3, MaxArgs: 3, NeedsPostProc: true},
		{Name: "beta_inv", MinArgs: 3, MaxArgs: 3, NeedsPostProc: true},
		{Name: "beta_pdf", MinArgs: 3, MaxArgs: 3, NeedsPostProc: true},
		{Name: "jaccard_index", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		{Name: "welch_test", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		{Name: "isfinite", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "isinf", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "isnan", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "isascii", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "isutf8", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "gettype", MinArgs: 1, MaxArgs: 1, SQLite: "typeof(%s)"},
		{Name: "estimate_data_size", MinArgs: 1, MaxArgs: -1, NeedsPostProc: true},
		{Name: "bitset_count_ones", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "countof", MinArgs: 2, MaxArgs: 3, NeedsPostProc: true}, // count occurrences of substring/regex

		// --- min/max/null helpers ---
		{Name: "max_of", MinArgs: 2, MaxArgs: -1, SQLite: "MAX(%s)"}, // variadic: MAX(a,b,...)
		{Name: "min_of", MinArgs: 2, MaxArgs: -1, SQLite: "MIN(%s)"},
		{Name: "notnull", MinArgs: 2, MaxArgs: -1, SQLite: "coalesce(%s)"}, // first non-null

		// --- binary bit ops ---
		{Name: "binary_and", MinArgs: 2, MaxArgs: 2, SQLite: "(%s & %s)"},
		{Name: "binary_or", MinArgs: 2, MaxArgs: 2, SQLite: "(%s | %s)"},
		{Name: "binary_xor", MinArgs: 2, MaxArgs: 2, SQLite: "(%s | %s)"}, // sqlite has no xor; | is best-effort
		{Name: "binary_not", MinArgs: 1, MaxArgs: 1, SQLite: "(~%s)"},
		{Name: "binary_shift_left", MinArgs: 2, MaxArgs: 2, SQLite: "(%s << %s)"},
		{Name: "binary_shift_right", MinArgs: 2, MaxArgs: 2, SQLite: "(%s >> %s)"},

		// --- String extras ---
		{Name: "strcat_array", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true}, // join array w/ delim
		{Name: "strcat_delim", MinArgs: 2, MaxArgs: -1, NeedsPostProc: true},
		{Name: "strcmp", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true}, // sqlite strcmp needs collation ext
		{Name: "strrep", MinArgs: 2, MaxArgs: 3, SQLite: "replace(replace(..., '', ''))", NeedsPostProc: true},
		{Name: "repeat", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true}, // repeat string N times
		{Name: "translate", MinArgs: 3, MaxArgs: 3, NeedsPostProc: true},
		{Name: "replace_strings", MinArgs: 3, MaxArgs: 3, NeedsPostProc: true},
		{Name: "regex_quote", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "indexof_regex", MinArgs: 2, MaxArgs: 4, NeedsPostProc: true},
		{Name: "extract_all", MinArgs: 2, MaxArgs: 3, NeedsPostProc: true},
		{Name: "extract_json", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		{Name: "dynamic_to_json", MinArgs: 1, MaxArgs: 1, SQLite: "%s"},
		{Name: "format_bytes", MinArgs: 1, MaxArgs: 2, NeedsPostProc: true},
		{Name: "url_encode_component", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},

		// --- DateTime constructors + timezone ---
		{Name: "datetime", MinArgs: 1, MaxArgs: 1, SQLite: "datetime(%s)"},
		{Name: "make_datetime", MinArgs: 3, MaxArgs: 6, NeedsPostProc: true},
		{Name: "make_timespan", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		{Name: "datetime_local_to_utc", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		{Name: "datetime_utc_to_local", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		{Name: "datetime_list_timezones", MinArgs: 0, MaxArgs: 0, NeedsPostProc: true},
		{Name: "datepart", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		{Name: "unixtime_seconds_todatetime", MinArgs: 1, MaxArgs: 1, SQLite: "datetime(%s, 'unixepoch')"},
		{Name: "unixtime_milliseconds_todatetime", MinArgs: 1, MaxArgs: 1, SQLite: "datetime(%s / 1000, 'unixepoch')"},
		{Name: "unixtime_microseconds_todatetime", MinArgs: 1, MaxArgs: 1, SQLite: "datetime(%s / 1000000, 'unixepoch')"},
		{Name: "unixtime_nanoseconds_todatetime", MinArgs: 1, MaxArgs: 1, SQLite: "datetime(%s / 1000000000, 'unixepoch')"},
		{Name: "guid", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},

		// --- Unicode ---
		{Name: "unicode_codepoints_from_string", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "unicode_codepoints_to_string", MinArgs: 1, MaxArgs: -1, NeedsPostProc: true},

		// --- Window functions (row_*) ---
		{Name: "row_cumsum", MinArgs: 1, MaxArgs: 2, NeedsPostProc: true},    // SUM(...) OVER
		{Name: "row_number", MinArgs: 0, MaxArgs: 2, NeedsPostProc: true},     // ROW_NUMBER() OVER
		{Name: "row_rank", MinArgs: 1, MaxArgs: 2, NeedsPostProc: true},       // RANK() OVER
		{Name: "row_rank_dense", MinArgs: 1, MaxArgs: 2, NeedsPostProc: true},
		{Name: "row_rank_min", MinArgs: 1, MaxArgs: 2, NeedsPostProc: true},
		{Name: "row_window_session", MinArgs: 1, MaxArgs: -1, NeedsPostProc: true},
		{Name: "prev", MinArgs: 1, MaxArgs: 3, NeedsPostProc: true},           // LAG() OVER
		{Name: "next", MinArgs: 1, MaxArgs: 3, NeedsPostProc: true},           // LEAD() OVER

		// --- Set / array predicates ---
		{Name: "set_difference", MinArgs: 2, MaxArgs: -1, NeedsPostProc: true},
		{Name: "set_equals", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		{Name: "set_union", MinArgs: 2, MaxArgs: -1, NeedsPostProc: true},     // already partial; reassert
		{Name: "set_has_element", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		{Name: "set_intersect", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},

		// --- pack / bag / zip (JSON object construction) ---
		{Name: "pack", MinArgs: 2, MaxArgs: -1, NeedsPostProc: true},
		{Name: "pack_all", MinArgs: 0, MaxArgs: 0, NeedsPostProc: true},
		{Name: "pack_array", MinArgs: 1, MaxArgs: -1, NeedsPostProc: true},
		{Name: "bag_has_key", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		{Name: "bag_merge", MinArgs: 2, MaxArgs: -1, NeedsPostProc: true},
		{Name: "bag_pack", MinArgs: 2, MaxArgs: -1, NeedsPostProc: true},
		{Name: "bag_pack_columns", MinArgs: 1, MaxArgs: -1, NeedsPostProc: true},
		{Name: "bag_remove_keys", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		{Name: "bag_set_key", MinArgs: 3, MaxArgs: 3, NeedsPostProc: true},
		{Name: "bag_zip", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		{Name: "zip", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		{Name: "treepath", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},

		// --- parse_* helpers ---
		{Name: "parse_command_line", MinArgs: 1, MaxArgs: 2, NeedsPostProc: true},
		{Name: "parse_csv", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "parse_ipv4_mask", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "parse_ipv6", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "parse_ipv6_mask", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "parse_path", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "parse_url", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "parse_urlquery", MinArgs: 1, MaxArgs: 2, NeedsPostProc: true},
		{Name: "parse_user_agent", MinArgs: 1, MaxArgs: 2, NeedsPostProc: true},
		{Name: "parse_version", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "parse_xml", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},

		// --- Misc utilities (ADX-internal: NeedsPostProc / no-op) ---
		{Name: "column_names_of", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "ingestion_time", MinArgs: 0, MaxArgs: 0, NeedsPostProc: true},
		{Name: "extent_id", MinArgs: 0, MaxArgs: 0, NeedsPostProc: true},
		{Name: "extent_tags", MinArgs: 0, MaxArgs: 0, NeedsPostProc: true},
		{Name: "current_cluster_endpoint", MinArgs: 0, MaxArgs: 0, NeedsPostProc: true},
		{Name: "current_cursor", MinArgs: 0, MaxArgs: 0, NeedsPostProc: true},
		{Name: "current_database", MinArgs: 0, MaxArgs: 0, NeedsPostProc: true},
		{Name: "current_principal", MinArgs: 0, MaxArgs: 1, NeedsPostProc: true},
		{Name: "current_principal_details", MinArgs: 0, MaxArgs: 0, NeedsPostProc: true},
		{Name: "current_principal_is_member_of", MinArgs: 1, MaxArgs: -1, NeedsPostProc: true},
		{Name: "cursor_after", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "cursor_before_or_at", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "cursor_current", MinArgs: 0, MaxArgs: 1, NeedsPostProc: true},
		{Name: "cluster", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "database", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "table", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "external_table", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "materialized_view", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "stored_query_result", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "assert", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		{Name: "base64_decode_toarray", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "base64_decode_toguid", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "base64_encode_fromarray", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "base64_encode_fromguid", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "gzip_compress_to_base64_string", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "gzip_decompress_from_base64_string", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "zlib_compress_to_base64_string", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "zlib_decompress_from_base64_string", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "punycode_domain_from_string", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "punycode_domain_to_string", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "punycode_from_string", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "punycode_to_string", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},

		// --- tdigest / hll scalar helpers ---
		{Name: "dcount_hll", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "hll_isvalid", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "tdigest_isvalid", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "merge_tdigest", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		{Name: "percentile_tdigest", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		{Name: "percentiles_array_tdigest", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		{Name: "percentrank_tdigest", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		{Name: "rank_tdigest", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
	}
	for _, s := range adds {
		catalog[normalize(s.Name)] = s
	}
}
