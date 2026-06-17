# kql — KQL query tool (prototype)

A command-line interface for running **Kusto Query Language (KQL)** queries
against a SQLite database. This is the prototype CLI for the `nzinfo/kql` project.

> **Status:** prototype. Only the SQLite backend is wired (sqlite is positioned
> as a prototype/test backend; PostgreSQL is the production target — see the
> project root `DESIGN.md`). P0 KQL operators are supported.

## Build

```bash
go build -o kql ./cmd/kql
```

## Usage

```
kql — KQL query tool (prototype; sqlite backend)

Usage:
  kql -d <dsn> '<query>'                            Run a query, print rows (default: csv)
  kql -d <dsn> -o json '<query>'                    Run, print as JSON
  kql -d <dsn> [--policy <p>] explain '<query>'    Parse+optimise, print IR + SQL + decision log (no exec)
  kql validate '<query>'                            Parse only, print diagnostics
  kql -h | --help                                   This help

Options:
  -d <dsn>       Data source (sqlite dsn: :memory:, file:path.db, or a .db path; postgres://... for pg)
  -o <format>    Output format: csv (default) | json | table
  --policy <p>   Optimizer decision policy for explain: conservative (default) | aggressive | gated
```

## Examples

Run a query against a SQLite file, default CSV output:

```bash
$ kql -d events.db 'StormEvents | where State == "TEXAS" | take 3'
State,DamagedProperty,EventType
TEXAS,1500,Hail
TEXAS,3200,Wind
TEXAS,100,Hail
```

JSON output:

```bash
$ kql -d events.db -o json 'events | summarize total = sum(Damage) by State | sort by total desc | take 1'
[
  {
    "State": "FLORIDA",
    "total": 9000
  }
]
```

Inspect the generated SQL and IR (no execution):

```bash
$ kql -d events.db explain 'events | where x > 0 | summarize c = count() by state | sort by c desc | take 5'
# IR Pipeline
Pipeline
  Source: Table "events"
  Filter
    where (Col("x") > long(0))
  Aggregate (1 aggs, 1 keys)
    agg c = count() [agg]
    by Col("state")
  Sort (1 keys)
    key Col("c") (desc)
  Limit
    take long(5)

# Emitted SQL (sqlite)
SELECT * FROM (SELECT * FROM (...) ORDER BY _k0."c" DESC) AS _k0 LIMIT ?1
```

Validate a query without a database:

```bash
$ kql validate 'events | where'
events:1:15: KQL005: unexpected end of input

$ kql validate 'events | take 1'
OK
```

## Supported KQL (P0)

Operators: `where` (alias `filter`), `project`, `extend`, `take` (`limit`),
`sort by` (`order by`), `summarize … by`, `join kind=…`, `union`, `distinct`,
`count`, `top`, `let`.

Expressions: arithmetic, comparison, logical (`and`/`or`), string operators
(`has`/`contains`/`startswith`/…), `in`/`!in`, `between`/`!between`, function
calls (incl. aggregates `count`/`sum`/`avg`/`min`/`max`/…, `iff`, casts),
member/index access, string/datetime/timespan/guid/bool literals.

## Notes

- The SQLite driver is `modernc.org/sqlite` (pure Go, CGO-free) — chosen for the
  prototype loop. The production PostgreSQL backend uses `pgx`.
- See the project root `DESIGN.md`, `claude.md`, and `docs/PROGRESS.md` for the
  overall architecture and roadmap.
