// cmd/kql-collect-pg-stats connects to a PostgreSQL database and collects
// statistics from pg_stats / pg_class / pg_index into a YAML catalog file
// (source: pg_analyze), ready for use with `kql explain --stats`.
//
// Usage:
//
//	kql-collect-pg-stats -d "postgres://user:pass@host/db" -schema public -o stats.yaml
//
// The output is an O0 stats.Catalog YAML with per-table row_count, per-column
// card/nulls/MCV/hist, and indexes. Confidence will be 0.9 (pg_analyze source).
package main

import (
	"database/sql"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib"
	"gopkg.in/yaml.v3"
)

type columnStats struct {
	Card   int64     `yaml:"card"`
	Nulls  int64     `yaml:"nulls"`
	Type   string    `yaml:"type"`
	MCV    *mcvStats `yaml:"mcv,omitempty"`
	Hist   *histStats `yaml:"hist,omitempty"`
}

type mcvStats struct {
	Values     []string  `yaml:"values"`
	Frequencies []float64 `yaml:"frequencies"`
}

type histStats struct {
	Kind   string   `yaml:"kind"`
	Bounds []string `yaml:"bounds"`
}

type tableStats struct {
	RowCount    int64                 `yaml:"row_count"`
	AvgRowBytes int                   `yaml:"avg_row_bytes"`
	Columns     map[string]*columnStats `yaml:"columns"`
}

type catalog struct {
	Version string                   `yaml:"version"`
	Source  string                   `yaml:"source"`
	Schema  string                   `yaml:"schema"`
	Tables  map[string]*tableStats   `yaml:"tables"`
}

func main() {
	dsn := flag.String("d", "", "postgres DSN (postgres://user:pass@host/db)")
	schema := flag.String("schema", "public", "schema name to collect")
	output := flag.String("o", "stats.yaml", "output YAML file")
	flag.Parse()

	if *dsn == "" {
		fmt.Fprintln(os.Stderr, "missing -d <dsn>")
		os.Exit(1)
	}

	db, err := sql.Open("pgx", *dsn)
	if err != nil {
		die("open: %v", err)
	}
	defer db.Close()

	cat := &catalog{
		Version: "1",
		Source:  "pg_analyze",
		Schema:  *schema,
		Tables:  map[string]*tableStats{},
	}

	// Collect tables in the schema.
	tables, err := queryTables(db, *schema)
	if err != nil {
		die("query tables: %v", err)
	}

	for _, table := range tables {
		t, err := collectTable(db, *schema, table)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warn: skip %s: %v\n", table, err)
			continue
		}
		cat.Tables[table] = t
	}

	out, err := yaml.Marshal(cat)
	if err != nil {
		die("marshal: %v", err)
	}
	if err := os.WriteFile(*output, out, 0644); err != nil {
		die("write %s: %v", *output, err)
	}
	fmt.Fprintf(os.Stderr, "collected %d tables → %s\n", len(cat.Tables), *output)
}

func queryTables(db *sql.DB, schema string) ([]string, error) {
	rows, err := db.Query(`
		SELECT table_name FROM information_schema.tables
		WHERE table_schema = $1 AND table_type = 'BASE TABLE'
		ORDER BY table_name`, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		tables = append(tables, name)
	}
	return tables, rows.Err()
}

func collectTable(db *sql.DB, schema, table string) (*tableStats, error) {
	// Row count from pg_class.reltuples (analyzed).
	var reltuples int64
	err := db.QueryRow(`
		SELECT c.reltuples FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE c.relname = $1 AND n.nspname = $2`,
		table, schema).Scan(&reltuples)
	if err != nil {
		return nil, fmt.Errorf("reltuples: %w", err)
	}

	// Avg row bytes from relpages.
	var relpages int
	db.QueryRow(`
		SELECT c.relpages FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE c.relname = $1 AND n.nspname = $2`,
		table, schema).Scan(&relpages)
	avgBytes := 0
	if reltuples > 0 {
		avgBytes = int(relpages) * 8192 / int(reltuples)
	}

	t := &tableStats{
		RowCount:    reltuples,
		AvgRowBytes: avgBytes,
		Columns:     map[string]*columnStats{},
	}

	// Per-column stats from pg_stats.
	colRows, err := db.Query(`
		SELECT attname, inherited, null_frac, n_distinct,
		       most_common_vals::text, most_common_freqs,
		       histogram_bounds::text
		FROM pg_stats WHERE schemaname = $1 AND tablename = $2
		`, schema, table)
	if err != nil {
		return nil, fmt.Errorf("pg_stats: %w", err)
	}
	defer colRows.Close()

	for colRows.Next() {
		var (
			attname     string
			inherited   bool
			null_frac   float64
			n_distinct  float64
			mcv_text    sql.NullString
			mcf_text    sql.NullString
			hist_text   sql.NullString
		)
		if err := colRows.Scan(&attname, &inherited, &null_frac, &n_distinct,
			&mcv_text, &mcf_text, &hist_text); err != nil {
			return nil, err
		}
		if inherited {
			continue // skip partitions
		}

		// Cardinality.
		card := int64(0)
		if n_distinct > 0 {
			card = int64(n_distinct)
		} else if n_distinct < 0 && reltuples > 0 {
			card = int64(-n_distinct * float64(reltuples))
		}

		// Nulls.
		nulls := int64(0)
		if reltuples > 0 {
			nulls = int64(null_frac * float64(reltuples))
		}

		cs := &columnStats{
			Card:  card,
			Nulls: nulls,
			Type:  "auto", // pg_stats doesn't give the SQL type directly
		}

		// MCV.
		if mcv_text.Valid && mcv_text.String != "" {
			vals := parsePgArray(mcv_text.String)
			freqs := parsePgFloats(mcf_text.String)
			if len(vals) > 0 && len(freqs) == len(vals) {
				cs.MCV = &mcvStats{Values: vals, Frequencies: freqs}
			}
		}

		// Histogram.
		if hist_text.Valid && hist_text.String != "" {
			bounds := parsePgArray(hist_text.String)
			if len(bounds) > 0 {
				cs.Hist = &histStats{Kind: "equi_freq", Bounds: bounds}
			}
		}

		t.Columns[attname] = cs
	}

	return t, colRows.Err()
}

// parsePgArray parses a pg_stats text representation of an array
// (e.g. {TEXAS,FLORIDA,OHIO} → ["TEXAS","FLORIDA","OHIO"]).
func parsePgArray(s string) []string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "{") || !strings.HasSuffix(s, "}") {
		return nil
	}
	inner := s[1 : len(s)-1]
	if inner == "" {
		return nil
	}
	return strings.Split(inner, ",")
}

// parsePgFloats parses a pg_stats float array (e.g. {0.3,0.2,0.1}).
func parsePgFloats(s string) []float64 {
	parts := parsePgArray(s)
	var out []float64
	for _, p := range parts {
		f, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil {
			continue
		}
		out = append(out, f)
	}
	return out
}

func die(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
