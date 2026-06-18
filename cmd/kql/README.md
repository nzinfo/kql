# kql — command-line KQL query tool

A command-line interface for the nzinfo/kql Kusto Query Language engine.
Parses KQL, translates to IR, optimizes, and executes against PostgreSQL
(production), DuckDB (analytics), or SQLite (prototype/test).

## Installation

```sh
go install nzinfo/kql/cmd/kql@latest
```

## Usage

### Run a query

```sh
# Against SQLite (in-memory, for testing)
kql -d ":memory:" 'datatable(Name:string, Age:long)[("Alice",30),("Bob",25)] | where Age > 20 | sort by Name'

# Against PostgreSQL (production)
kql -d "postgres://user:pass@localhost:5432/db" 'events | where state == "TX" | count'

# Against DuckDB (analytics)
kql -d "duckdb:///path/to/data.duckdb" 'SELECT * FROM sales'
```

### Output formats

```sh
kql -d "$DSN" -o csv  'query'   # default: comma-separated values
kql -d "$DSN" -o json 'query'   # JSON array of objects
kql -d "$DSN" -o table 'query'  # aligned table
```

### Explain (optimization decisions)

```sh
# Show the IR pipeline tree + generated SQL + optimization decisions
kql explain -d "$DSN" --policy aggressive --stats catalog.yaml 'events | join (meta) on $left.id == $right.id'

# Policies: conservative (default, safe), aggressive (cost-optimized), gated (confidence-based)
```

The explain output shows:
- The IR pipeline tree (source → stages)
- The generated SQL (with pg_hint_plan comments for join methods)
- Optimization decisions (predicate ordering + join plan selection rationale)

### Validate (syntax check without execution)

```sh
kql validate 'T | where x > 0 | summarize c = count() by g'
```

## Flags

| Flag | Description |
|---|---|
| `-d, --dsn` | Database connection string |
| `-o, --output` | Output format: csv (default), json, table |
| `--policy` | Optimizer policy: conservative, aggressive, gated |
| `--stats` | Path to stats catalog YAML (for cost-based optimization) |
| `--format` | Explain output format: text (default), yaml |

## Stats catalog

Generate a stats catalog from PostgreSQL for cost-based optimization:

```sh
# Collect stats from a real pg database
go run cmd/kql-collect-pg-stats/main.go -d "$PG_DSN" -o stats.yaml

# Use it for join-method optimization
kql explain -d "$DSN" --policy aggressive --stats stats.yaml 'query'
```

The optimizer's O4 Join AltPlan uses the catalog to select the best join
method (HashJoin/NestLoop/MergeJoin/IndexLookup) and emits pg_hint_plan
hints. Hints degrade gracefully (ignored if pg_hint_plan isn't installed).
