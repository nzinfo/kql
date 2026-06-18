// Package builtin — Round 5: ipv4_*/ipv6_*, array_*, series_* (CROSS-PROJECT-COMPARISON.md §四).
//
// Network functions are high-value for Sentinel/security use cases. pg has
// native inet/cidr support (overrides in pg emitter). SQLite/DuckDB best-effort
// NeedsPostProc. The series_* family covers element-wise numeric array math
// (make-series companions); pg/DuckDB can express some via array ops but the
// baseline catalog registers them with NeedsPostProc for client-side compute.
package builtin

func init() {
	adds := []Spec{
		// --- IPv4 ---
		{Name: "ipv4_compare", MinArgs: 2, MaxArgs: 3, NeedsPostProc: true},
		{Name: "ipv4_is_match", MinArgs: 2, MaxArgs: 3, NeedsPostProc: true},
		{Name: "ipv4_is_in_range", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		{Name: "ipv4_is_in_any_range", MinArgs: 2, MaxArgs: -1, NeedsPostProc: true},
		{Name: "ipv4_netmask_suffix", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "ipv4_range_to_cidr_list", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		{Name: "format_ipv4", MinArgs: 1, MaxArgs: 2, NeedsPostProc: true},
		{Name: "format_ipv4_mask", MinArgs: 2, MaxArgs: 3, NeedsPostProc: true},
		{Name: "has_ipv4", MinArgs: 2, MaxArgs: -1, NeedsPostProc: true},
		{Name: "has_any_ipv4", MinArgs: 2, MaxArgs: -1, NeedsPostProc: true},
		{Name: "has_ipv4_prefix", MinArgs: 2, MaxArgs: -1, NeedsPostProc: true},
		{Name: "has_any_ipv4_prefix", MinArgs: 2, MaxArgs: -1, NeedsPostProc: true},

		// --- IPv6 ---
		{Name: "ipv6_compare", MinArgs: 2, MaxArgs: 3, NeedsPostProc: true},
		{Name: "ipv6_is_match", MinArgs: 2, MaxArgs: 3, NeedsPostProc: true},
		{Name: "ipv6_is_in_range", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		{Name: "ipv6_is_in_any_range", MinArgs: 2, MaxArgs: -1, NeedsPostProc: true},

		// --- Array operations (dynamic arrays) ---
		{Name: "array_concat", MinArgs: 1, MaxArgs: -1, NeedsPostProc: true},
		{Name: "array_iff", MinArgs: 3, MaxArgs: 3, NeedsPostProc: true}, // alias array_iif
		{Name: "array_iif", MinArgs: 3, MaxArgs: 3, NeedsPostProc: true},
		{Name: "array_index_of", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		{Name: "array_reverse", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "array_rotate_left", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		{Name: "array_rotate_right", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		{Name: "array_shift_left", MinArgs: 1, MaxArgs: 2, NeedsPostProc: true},
		{Name: "array_shift_right", MinArgs: 1, MaxArgs: 2, NeedsPostProc: true},
		{Name: "array_slice", MinArgs: 3, MaxArgs: 3, NeedsPostProc: true},
		{Name: "array_sort_asc", MinArgs: 1, MaxArgs: -1, NeedsPostProc: true},
		{Name: "array_sort_desc", MinArgs: 1, MaxArgs: -1, NeedsPostProc: true},
		{Name: "array_split", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		{Name: "array_strcat", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		{Name: "array_sum", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "has_any_index", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},

		// --- series_* element-wise math (make-series companions) ---
		// Arithmetic: pg/DuckDB can express via array unnest/zip but baseline NeedsPostProc.
		{Name: "series_add", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		{Name: "series_subtract", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		{Name: "series_multiply", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		{Name: "series_divide", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		{Name: "series_pow", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		{Name: "series_product", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "series_sum", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "series_abs", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "series_negate", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "series_sign", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "series_exp", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "series_log", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "series_floor", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "series_ceiling", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "series_sqrt", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "series_sin", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "series_cos", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "series_tan", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "series_asin", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "series_acos", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "series_atan", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		// Comparison (element-wise → bool array)
		{Name: "series_equals", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		{Name: "series_not_equals", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		{Name: "series_less", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		{Name: "series_less_equals", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		{Name: "series_greater", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		{Name: "series_greater_equals", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		// Linear algebra
		{Name: "series_dot_product", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		{Name: "series_magnitude", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "series_cosine_similarity", MinArgs: 2, MaxArgs: 3, NeedsPostProc: true},
		{Name: "series_pearson_correlation", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		// Statistics
		{Name: "series_stats", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "series_stats_dynamic", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "series_outliers", MinArgs: 1, MaxArgs: 2, NeedsPostProc: true},
		{Name: "series_seasonal", MinArgs: 1, MaxArgs: 2, NeedsPostProc: true},
		// Fill / interpolation
		{Name: "series_fill_backward", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "series_fill_const", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		{Name: "series_fill_forward", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "series_fill_linear", MinArgs: 1, MaxArgs: 2, NeedsPostProc: true},
		// Decomposition / forecasting (advanced)
		{Name: "series_decompose", MinArgs: 1, MaxArgs: -1, NeedsPostProc: true},
		{Name: "series_decompose_anomalies", MinArgs: 1, MaxArgs: -1, NeedsPostProc: true},
		{Name: "series_decompose_forecast", MinArgs: 1, MaxArgs: -1, NeedsPostProc: true},
		{Name: "series_periods_detect", MinArgs: 1, MaxArgs: 2, NeedsPostProc: true},
		{Name: "series_periods_validate", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
		// Signal processing
		{Name: "series_fft", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "series_ifft", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "series_fir", MinArgs: 2, MaxArgs: -1, NeedsPostProc: true},
		{Name: "series_iir", MinArgs: 2, MaxArgs: -1, NeedsPostProc: true},
		{Name: "series_fit_line", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "series_fit_line_dynamic", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "series_fit_2lines", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "series_fit_2lines_dynamic", MinArgs: 1, MaxArgs: 1, NeedsPostProc: true},
		{Name: "series_fit_poly", MinArgs: 2, MaxArgs: 2, NeedsPostProc: true},
	}
	for _, s := range adds {
		catalog[normalize(s.Name)] = s
	}
}
