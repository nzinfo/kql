# pg Stats → KQL Stats Catalog Mapping

> How PostgreSQL's `pg_stats` / `pg_class` maps to our YAML stats catalog.

## Source Queries (cmd/kql-collect-pg-stats)

The collector queries:

1. **`pg_class`** — table row count estimate (`reltuples`) + page count (`relpages`)
2. **`pg_stats`** — per-column statistics:
   - `n_distinct` → NDV (number of distinct values)
   - `null_frac` → null fraction
   - `most_common_vals` / `most_common_freqs` → MCV (Most Common Values)
   - `histogram_bounds` → Hist (equi-width histogram)
   - `correlation` → CorrVs (physical ordering correlation)

## Field Mapping

| pg_stats column | Catalog field | Type | Notes |
|---|---|---|---|
| `reltuples` | `Table.RowCount` | int64 | Row count estimate |
| `relpages × 8192` | `Table.AvgRowBytes` | int | Pages × page_size → bytes |
| `attname` | `ColumnStats.Name` | string | Column name |
| `n_distinct` | `ColumnStats.Card` | int64 | NDV (negative = fraction) |
| `null_frac` | `ColumnStats.Nulls` | int64 | null_frac × row_count |
| `most_common_vals` | `MCV.Values` | []string | Top frequent values |
| `most_common_freqs` | `MCV.Frequencies` | []float64 | Corresponding frequencies |
| `histogram_bounds` | `Hist.Bounds` | []string | Equi-width boundaries |
| `correlation` | `CorrVs.Rho` | float64 | Physical clustering factor |

## Type Mapping

| pg data_type | Catalog type |
|---|---|
| `integer`, `bigint`, `smallint`, `serial` | `long` |
| `real`, `double precision`, `numeric` | `real` |
| `text`, `varchar`, `char` | `string` |
| `boolean` | `bool` |
| `timestamp`, `timestamptz` | `datetime` |
| `interval` | `timespan` |
| `json`, `jsonb` | `dynamic` |

## Index Mapping

pg indexes are collected from `pg_indexes`:

| pg_indexes column | Catalog field |
|---|---|
| `indexname` | `IndexDef.Name` |
| `indexdef` (parsed) | `IndexDef.Columns` (leading column) |
| `indisunique` | `IndexDef.Unique` |

## Confidence Calculation

| Source | Ceiling |
|---|---|
| `pg_analyze` (from `pg_stats`) | 0.9 |
| `sampling` (manual estimate) | 0.7 |
| `manual` (hand-written) | 0.6 |

Column confidence = fields_present / 4 (Card, Nulls, MCV, Hist). Table confidence
= avg(column confidences) × source_ceiling. Used by ConfidenceGated policy.
