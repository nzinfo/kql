// cmd/kql is the KQL command-line interface.
//
// Usage:
//
//	kql -d <dsn> '<query>'            # run a query, print rows (default csv)
//	kql -d <dsn> -o json '<query>'    # run, print as JSON
//	kql -d <dsn> explain '<query>'    # parse+translate, print IR + emitted SQL (no exec)
//	kql validate '<query>'            # parse only, print diagnostics
//	kql -h | --help
//
// For the minimal loop only the sqlite backend is wired; -d takes a sqlite dsn
// (e.g. ":memory:", "file:test.db", or a path). Other backends (pg/duckdb) are
// added as their backend lines land.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"nzinfo/kql/internal/backend/sqlite"
	"nzinfo/kql/pkg/kql"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "kql:", err)
		os.Exit(1)
	}
}

// run parses args and dispatches to the chosen subcommand or the default run
// action. Kept separate from main() for testability (tests call run directly).
func run(args []string) error {
	if len(args) == 0 {
		printUsage(os.Stdout)
		return nil
	}

	// Subcommands first.
	switch args[0] {
	case "explain":
		return runExplain(args[1:])
	case "validate":
		return runValidate(args[1:])
	case "-h", "--help", "help":
		printUsage(os.Stdout)
		return nil
	case "-v", "--version":
		fmt.Fprintln(os.Stdout, "kql 0.1.0 (prototype; sqlite backend)")
		return nil
	}

	// Default: run a query. Parse flags + positional query.
	fs := flag.NewFlagSet("kql", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dsn := fs.String("d", "", "data source (sqlite dsn, e.g. :memory: or a .db path)")
	format := fs.String("o", "csv", "output format: csv | json | table")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) == 0 {
		return errors.New("missing query (use -h for help)")
	}
	query := strings.Join(rest, " ")
	if *dsn == "" {
		return errors.New("missing -d <dsn>")
	}
	return runQuery(context.Background(), *dsn, *format, query)
}

// runQuery executes a KQL query against the sqlite backend and prints rows.
func runQuery(ctx context.Context, dsn, format, query string) error {
	bk, err := sqlite.New(dsn)
	if err != nil {
		return err
	}
	defer bk.Close()
	res, err := kql.ExecOn(ctx, bk, query)
	if err != nil {
		return err
	}
	return printResult(os.Stdout, res, format)
}

// runExplain parses and translates a query, then prints the IR and the emitted
// SQL WITHOUT executing it. Useful for debugging translation.
func runExplain(args []string) error {
	fs := flag.NewFlagSet("kql explain", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dsn := fs.String("d", "", "data source (sqlite dsn; needed to emit backend SQL)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) == 0 {
		return errors.New("explain: missing query")
	}
	query := strings.Join(rest, " ")

	pipe, err := kql.ParseTranslate(query)
	if err != nil {
		return err
	}
	fmt.Println("# IR Pipeline")
	printIR(os.Stdout, pipe)
	fmt.Println()

	if *dsn != "" {
		bk, err := sqlite.New(*dsn)
		if err != nil {
			return err
		}
		defer bk.Close()
		q, err := bk.Emit(pipe)
		if err != nil {
			return err
		}
		fmt.Println("# Emitted SQL (sqlite)")
		fmt.Println(q.SQL)
		if len(q.Args) > 0 {
			fmt.Printf("\n# Bind args: %v\n", q.Args)
		}
	}
	return nil
}

// runValidate parses a query and prints diagnostics only (no translate, no exec).
func runValidate(args []string) error {
	fs := flag.NewFlagSet("kql validate", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) == 0 {
		return errors.New("validate: missing query")
	}
	query := strings.Join(rest, " ")
	_, err := kql.ParseTranslate(query)
	if err == nil {
		fmt.Println("OK")
		return nil
	}
	// Print the diagnostic lines (the error carries them).
	if ke, ok := err.(*kql.Error); ok {
		for _, line := range ke.Diagnostics() {
			fmt.Fprintln(os.Stdout, line)
		}
		return nil
	}
	return err
}

// printUsage writes the help text to w.
func printUsage(w *os.File) {
	fmt.Fprint(w, `kql — KQL query tool (prototype; sqlite backend)

Usage:
  kql -d <dsn> '<query>'            Run a query, print rows (default: csv)
  kql -d <dsn> -o json '<query>'    Run, print as JSON
  kql -d <dsn> explain '<query>'    Parse+translate, print IR + SQL (no exec)
  kql validate '<query>'            Parse only, print diagnostics
  kql -h | --help                   This help

Options:
  -d <dsn>     Data source (sqlite dsn: :memory:, file:path.db, or a .db path)
  -o <format>  Output format: csv (default) | json | table

Examples:
  kql -d :memory: 'StormEvents | where State == "TEXAS" | take 10'
  kql -d test.db -o json 'events | summarize c = count() by state'
  kql -d test.db explain 'events | where x > 0 | take 5'
  kql validate 'events | where'
`)
}
