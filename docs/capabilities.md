# Capabilities — what nzinfo/kql supports

> Living reference of supported KQL operators, functions, types, and optimization features.
> Last updated: 2026-06-17.

## Operators (tabular pipeline)

| Operator | Support | Notes |
|---|---|---|
| `where` / `filter` | ✅ | Full predicate expressions; predicate pushdown (O2) |
| `project` | ✅ | Renaming, computed columns |
| `project-away` | ✅ | |
| `project-keep` | ✅ | |
| `project-rename` | ✅ | |
| `project-reorder` | ✅ | |
| `project-smart` | ✅ | |
| `extend` | ✅ | |
| `take` / `limit` | ✅ | |
| `sort` / `order` | ✅ | asc/desc, nulls first/last |
| `summarize` | ✅ | count/sum/avg/min/max/dcount/countif/sumif/avgif + `by` grouping |
| `join` | ✅ | kind=innerunique/inner/left/right/full; `$left`/`$right`; **O4 cost-based hint selection** |
| `union` | ✅ | Multi-source; `union withsource=`; union-as-function |
| `distinct` | ✅ | |
| `count` | ✅ | |
| `top` | ✅ | |
| `top-nested` | ⚠️ passthrough | Parsed; not optimized |
| `evaluate` | ⚠️ passthrough | Plugin-style; passthrough emit |
| `externaldata` | ⚠️ passthrough | |
| `mv-expand` | ✅ | Client-side PostProc for sqlite; SQL UNNEST for pg |
| `parse` / `parse-where` | ⚠️ passthrough | |
| `make-series` | ⚠️ passthrough | |
| `as` | ✅ | Name binding (row-wise no-op; metadata) |
| `invoke` | ✅ | Function/plugin call (passthrough) |
| `consume` / `getschema` / `serialize` / `render` | ⚠️ passthrough | |

### Statements

| Statement | Support |
|---|---|
| `let` (scalar + tabular) | ✅ |
| `set` (query options) | ✅ |
| `declare query_parameters(...)` | ✅ |
| `declare pattern` | ⚠️ lenient skip |

## Operators (scalar expression)

| Category | Operators | Support |
|---|---|---|
| Arithmetic | `+ - * / %` | ✅ + type inference (int→long→real→decimal promotion) |
| Comparison | `< > <= >= == !=` | ✅ → bool |
| Logical | `and or` | ✅ → bool |
| Case-insensitive eq | `=~ !~` | ✅ pg ILIKE; sqlite LOWER() |
| String ops | `has !has contains !contains startswith !startswith endswith !endswith hasprefix hasprefix_cs hassuffix hasprefix_cs` | ✅ |
| `has_any` / `has_all` | | ✅ |
| `in` / `!in` / `in~` / `!in~` | | ✅ |
| `between` / `!between` | | ✅ |
| `:` (case-insensitive eq alias) | | ✅ (normalized to =~) |
| `matches regex` | | ✅ |
| `like` / `!like` / `like_cs` / `!like_cs` | | ✅ pg/DuckDB ILIKE/LIKE; sqlite LIKE + COLLATE BINARY for cs; `!like` lexes via negated-operator lookup |

## Types

bool, int, long, real, decimal, string, datetime, timespan, dynamic — all
supported in type inference. `decimal(...)` literal is not lexed (NOTES §3).

## Functions (433 catalog names — full kqlparser parity achieved)

All 386 scalar + 39 aggregate families from kqlparser are now registered.
Functions with portable SQL forms emit directly (see per-backend overrides);
the remainder are NeedsPostProc (parse+translate cleanly; client-side compute
required at runtime).

Categories: aggregate (count/sum/avg/min/max/dcount/countif/sumif/avgif/minif/maxif/
any/arg_max/arg_min/stdev/stdevp/variance/variancep/percentile[s/w] + make_list[_if]/
make_set[_if]/make_bag[_if]/hll/tdigest/binary_all_*/buildschema),
string (strcat/strcat_array/strcat_delim/tostring/tolower/toupper/substring/trim/
trim_start/trim_end/replace/replace_strings/replace_regex/extract/extract_all/
extract_json/split/indexof/indexof_regex/strlen/reverse/strrep/repeat/translate/
strcmp/regex_quote/format_bytes),
conversion (tobool/toint/tolong/toreal/todouble/todecimal/todatetime/totimespan/
toguid/todynamic/toobject/to_utf8/tohex/make_string),
datetime (now/ago/bin/datetime_add/datetime_diff/format_datetime/year/month/dayofmonth/
dayofweek/dayofyear/hour/minute/second/startof*/endof*/datetime_local_to_utc/
datetime_utc_to_local/make_datetime/make_timespan/unixtime_*_todatetime/datepart),
network (**ipv4_*/ipv6_*** for Sentinel security: ipv4_compare/is_match/is_in_range/
format_ipv4/has_ipv4/has_any_ipv4/has_ipv4_prefix + ipv6 equivalents),
array (array_length/array_concat/array_iff/array_iif/array_index_of/array_reverse/
array_rotate_*/array_shift_*/array_slice/array_sort_*/array_split/array_strcat/
array_sum),
series (51 functions: arithmetic add/subtract/multiply/divide/pow/abs/sign/exp/log/
sqrt/trig, comparison, dot_product/magnitude/cosine_similarity/pearson_correlation,
stats/outliers/seasonal, fill backward/const/forward/linear, fft/ifft/fir/iir,
fit_line/fit_2lines/fit_poly, decompose/anomalies/forecast, periods_detect/validate),
geo (53 functions: H3/geohash/S2 cells, point conversions, distance, line/polygon ops,
intersections — all NeedsPostProc, require PostGIS/geospatial backend),
window (row_cumsum/row_number/row_rank[_dense/min]/row_window_session/prev/next),
set (set_union/intersect/difference/equals/has_element),
pack/bag/zip (pack/pack_all/pack_array/bag_has_key/merge/pack/pack_columns/remove_keys/
set_key/zip/treepath),
parse (csv/url/urlquery/xml/user_agent/version/path/command_line/ipv4_mask/ipv6/ipv6_mask),
math (abs/sqrt/pow/exp/log/log2/log10/exp10/floor/ceiling/round/sign/pi/rand +
trig acos/asin/atan/atan2/cos/sin/tan/cot/degrees/radians + erf/erfc/loggamma/
beta_cdf/inv/pdf + isfinite/isinf/isnan/isascii/isutf8 + binary_and/or/xor/not/
shift_left/shift_right + bitset_count_ones),
conditional (iff/iif/case/coalesce/isnull/isnotnull/isempty/isnotempty/notnull/
max_of/min_of/column_ifexists),
JSON (parse_json/dynamic/dynamic_to_json/extract_json),
misc (gettype/countof/estimate_data_size/guid/hash_*/assert + ADX-internal
current_*/cursor_*/extent_*/cluster/database/table/external_table/materialized_view/
stored_query_result + base64/gzip/zlib/punycode + sketch helpers dcount_hll/hll_isvalid/
tdigest_isvalid/merge_tdigest/percentile*_tdigest).

Function call validation: KQL003 (unknown function) + KQL004 (arity) as
warnings. Type inference: 30+ functions get correct return types (KQL002).

## Optimization (O0–O5)

| Feature | Support |
|---|---|
| Stats catalog (YAML) | ✅ MCV/range/IN/AND/OR/join/corr selectivity; confidence |
| pg stats collector | ✅ cmd/kql-collect-pg-stats |
| Rule engine | ✅ PredicatePushdown + ConstantFold + ColumnPrune + PredicatePushdownUnion (to fixpoint) |
| Cost-based decisions | ✅ 3 strategies: Conservative / Aggressive / ConfidenceGated |
| Predicate ordering | ✅ Most-selective-first (confidence-gated) |
| **Join AltPlan (O4)** | ✅ Hash/NestLoop/Merge pg_hint_plan hints + IndexLookup two-phase IN-list |
| Columnar Record | ✅ internal/columnar (typed Int64/Float64/String/Bool/Mixed) |

## Diagnostics

| Code | Meaning | Severity |
|---|---|---|
| KQL000 | Syntax error | Error |
| KQL001 | Unknown column | Error |
| KQL002 | Type mismatch | Warning |
| KQL003 | Unknown function | Warning |
| KQL004 | Argument count | Warning |
| KQL005 | Table not found | Error |
| KQL006 | Duplicate binding | Warning |
| KQL007 | Unsupported feature | Warning |
| KQL008 | Internal error | Error |
