# Backend Differences — pg / duckdb / sqlite

> How the same KQL query is handled differently across the three supported backends.
> Last updated: 2026-06-17.

## Overview

| | PostgreSQL (production) | DuckDB (analytics) | SQLite (prototype/test) |
|---|---|---|---|
| Driver | pgx v5 | duckdb-go v2 | modernc.org/sqlite (pure Go) |
| Emit | CTE-based ($N placeholders) | reuses pg.Emit | CTE-based (?N placeholders) |
| Use case | Production queries | Analytical/columnar | Testing, prototyping |
| Cross-backend equiv | ✅ 19 test cases lockstep | ✅ same | ✅ same |

## Function translation differences

| KQL function | pg | sqlite | duckdb |
|---|---|---|---|
| `make_set(x)` | `array_agg(x)` | `(SELECT json_group_array...)` | reuses pg |
| `make_list(x)` | `array_agg(x)` | `json_group_array(x)` | reuses pg |
| `split(s, delim)` | `regexp_split_to_array(s, delim)` | custom | reuses pg |
| `extract(s, regex)` | `regexp_match(s, regex)` | `substring` | reuses pg |
| `parse_json(s)` | `s::jsonb` | `json(s)` | reuses pg |
| `=~` (case-insensitive eq) | `ILIKE` | `LOWER(a) = LOWER(b)` | reuses pg |
| `has` | `LIKE` / full-text | `LIKE` | reuses pg |

## Join handling

| Feature | pg | sqlite | duckdb |
|---|---|---|---|
| Emit shape | CTE `_sN` + `_sN_j` aliases | same | same |
| Join kinds | inner/left/right/full/innerunique | same | same |
| **O4 hints** | `/*+ HashJoin(...) */` (pg_hint_plan) | no hints (ignored) | no hints |
| **O4 IndexLookup** | two-phase `WHERE = ANY(?)` | two-phase `WHERE IN (...)` | reuses pg path |
| Graceful degrade | ✅ hint ignored if no extension | N/A (no hints) | N/A |

## Placeholder numbering

| | pg | sqlite |
|---|---|---|
| Style | `$1, $2, ...` | `?1, ?2, ...` |
| Scope | order-independent across CTEs | same |
| duckdb | reuses pg (`$N`) | — |

## Schema providers

| | pg | sqlite | duckdb |
|---|---|---|---|
| Source | `information_schema.columns` | `PRAGMA table_info` | reuses pg |
| Type mapping | pg OID → ir.Type | SQLite affinity → ir.Type | reuses pg |
| Case folding | unquoted → lowercase | case-insensitive | case-insensitive |

The binder resolves columns case-insensitively and rewrites to the physical
name, so KQL `EventType` becomes `eventtype` on pg but stays `EventType` on
sqlite/duckdb — emit uses the bound physical name directly.

## CTE emit architecture

All three backends share the same stage-splitting logic:
- Breakpoints: Aggregate/Join/Distinct/Union/Extend/Project → each becomes its own CTE
- Mergeable: Filter/Sort/Limit → fold into one SELECT
- Chained: `WITH _s0 AS (...), _s1 AS (SELECT ... FROM _s0 ...) SELECT * FROM _sN`

This means the same KQL query produces structurally identical SQL across all
backends, differing only in placeholder style, function names, and hints.

## Performance characteristics

| | pg | duckdb | sqlite |
|---|---|---|---|
| Large joins | Hash join (O4 hint available) | Columnar vectorized | Nested loop (small data) |
| Aggregation | Hash aggregate | Vectorized | In-memory |
| Index usage | O4 IndexLookup forces index | N/A (columnar) | Limited |
| Stats-driven | ✅ O4 cost model + pg_hint_plan | ❌ (no hints) | ❌ (no hints) |
