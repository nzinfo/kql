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
| `like` / `!like` / `like_cs` / `!like_cs` | | ⚠️ parsed; emit falls back to contains/regex approximation (see CROSS-PROJECT-COMPARISON.md §2.2) |

## Types

bool, int, long, real, decimal, string, datetime, timespan, dynamic — all
supported in type inference. `decimal(...)` literal is not lexed (NOTES §3).

## Functions (88 base Specs; 158 names incl. pg/DuckDB overrides)

Categories: aggregate (count/sum/avg/min/max/dcount/countif/sumif/avgif/minif/maxif/
stdev/variance/percentile/percentilew/percentilesw + make_list/make_set),
string (strcat/tostring/tolower/toupper/substring/trim/replace/extract/split/indexof),
conversion (tobool/toint/tolong/toreal/todatetime/totimespan/toguid/todynamic),
datetime (now/ago/bin/dayofweek/monthofyear),
array (array_length/array_concat/array_slice/make_set/make_list),
conditional (iff/iif/case/coalesce/isnull/isnotnull/isempty),
JSON (parse_json/dynamic/extract_json), math (abs/sqrt/pow/exp/log/floor/ceiling/sign).

> **Gap vs kqlparser** (reference Go project): 88 base / 158 names vs kqlparser's
> 386 scalar + 39 aggregate. See `docs/CROSS-PROJECT-COMPARISON.md` §2.3 for the
> full 285-scalar / 24-aggregate gap and prioritized import plan.

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
