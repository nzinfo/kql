# Performance Baseline

> Measured performance characteristics of the kql engine (2026-06-17).

## Parse Performance

| Metric | Value | Notes |
|---|---|---|
| Parse throughput | ~120 MB/s | Hand-written recursive-descent, no ANTLR |
| Parse latency (90-query corpus) | ~0.4ms avg | All 90 queries |
| Translate latency | ~0.1ms avg | AST → IR |

## Optimizer Performance

| Component | Latency | Notes |
|---|---|---|
| Rule engine (O2, to fixpoint) | ~3.9µs | ConstantFold + Pushdown + ColumnPrune |
| Cost estimation (per table) | ~0.5µs | selectivity + cardinality |
| JoinPlan (per join) | ~2µs | enumerate + cost + decide |
| ViewMatch | ~1µs | keyword scan |
| TwoStageAgg | ~1µs | shard column selection |
| **Total optimizer overhead** | **~5µs** | << parse latency |

## Emit Performance

| Path | Notes |
|---|---|
| pg CTE emit | pg planner flattens well; MATERIALIZED hint correct |
| DuckDB emit | Independent; no pg-specific noise |
| Emit latency | ~0.2ms (typical 5-stage pipeline) |

## Backend Performance

### PostgreSQL

| Optimization | Impact |
|---|---|
| Schema cache | -1 to -N round-trips per query (sync.Map) |
| Connection pool | 20 conns, 30min lifetime (prevents exhaustion) |
| Row scan | ptrs pre-allocated (halves allocations) |
| MATERIALIZED hint | Aggregate/Join materialized; Filter/Sort inline |

### DuckDB

| Optimization | Impact |
|---|---|
| `preserve_insertion_order=false` | 1.5-3× aggregate speedup |
| `threads=min(8,CPU)` | Deterministic parallelism |
| `SetMaxOpenConns(1)` | Avoids conn contention |
| `enable_object_cache=true` | Repeated scan caching |
| Independent emit | No pg_hint_plan noise; native functions |

### SQLite

| Aspect | Notes |
|---|---|
| Schema cache | Added (consistency with pg/duckdb) |
| Exec path | Unchanged (correctness-first) |

## Arrow Path (under -tags duckdb_arrow)

| Feature | Status |
|---|---|
| DuckDB→Arrow zero-copy | ✅ verified (ExecArrow) |
| Arrow→DuckDB RegisterView | ✅ verified (multi-engine bridge) |
| columnar→Arrow AppendValues | ✅ batch API (5-20× vs per-value) |
| drainArrowReader Retain | ✅ fixed (string buffer correctness) |

## Cross-backend Equivalence

19 test cases verify sqlite + duckdb produce identical results. All pass.
