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
//
// Subcommands (explain/validate/help/version) are recognised at args[0] OR
// after leading global flags (so `-d x explain 'q'` works, matching user
// intuition that `-d` configures the whole invocation).
func run(args []string) error {
	if len(args) == 0 {
		printUsage(os.Stdout)
		return nil
	}

	// Scan for a leading subcommand, skipping -d <dsn> / -o <fmt> flags.
	subIdx := -1
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-d" || a == "-o":
			i++ // skip the flag's value too
		case a == "explain" || a == "validate":
			subIdx = i
		case a == "-h" || a == "--help" || a == "help":
			printUsage(os.Stdout)
			return nil
		case a == "-v" || a == "--version":
			fmt.Fprintln(os.Stdout, "kql 0.1.0 (prototype; sqlite backend)")
			return nil
		case strings.HasPrefix(a, "-"):
			// unknown flag; let flag parsing report it
		default:
			// first positional (non-flag) token: if it's a subcommand we'd have
			// set subIdx above; otherwise it's the query and there's no sub.
		}
		if subIdx >= 0 {
			break
		}
	}

	if subIdx >= 0 {
		// Extract the -d/-o/-p flags from before the subcommand, the rest after.
		dsn := flagStr(args[:subIdx], "d", "")
		policy := flagStr(args[:subIdx], "policy", "")
		switch args[subIdx] {
		case "explain":
			return runExplain(dsn, policy, args[subIdx+1:])
		case "validate":
			return runValidate(args[subIdx+1:])
		}
	}
	fs := flag.NewFlagSet("kql", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dsn := fs.String("d", "", "data source (sqlite dsn, e.g. :memory: or a .db path)")
	format := fs.String("o", "csv", "output format: csv | json | table")
	_ = fs.String("policy", "conservative", "optimizer decision policy: conservative | aggressive | gated (affects explain only)")
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

// flagStr scans args for a -name <value> pair (name without leading "-") and
// returns its value (or def if absent). Used to pull global flags out before a
// subcommand without invoking flag.Parse on a mixed slice.
func flagStr(args []string, name, def string) string {
	flag := "-" + name
	for i := 0; i < len(args); i++ {
		if args[i] == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return def
}

// runQuery executes a KQL query against the backend selected by the dsn scheme
// (postgres:// → pg; anything else → sqlite) and prints rows.
func runQuery(ctx context.Context, dsn, format, query string) error {
	res, err := kql.Exec(ctx, dsn, query)
	if err != nil {
		return err
	}
	return printResult(os.Stdout, res, format)
}

// runExplain parses, binds, and optimises a query, then prints the IR, the
// emitted SQL, and the optimizer's decision log — WITHOUT executing. policy is
// the O3 strategy name ("" → rules-only, no cost-based decision). args is the
// slice after "explain" (the query + any explain-local flags).
func runExplain(dsn, policy string, args []string) error {
	fs := flag.NewFlagSet("kql explain", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) == 0 {
		return errors.New("explain: missing query")
	}
	query := strings.Join(rest, " ")

	// Show the IR (parse+translate only, pre-optimisation) for reference.
	pipe, err := kql.ParseTranslate(query)
	if err != nil {
		return err
	}
	fmt.Println("# IR Pipeline (pre-optimisation)")
	printIR(os.Stdout, pipe)
	fmt.Println()

	if dsn == "" {
		fmt.Println("# (no -d <dsn>: skipping bind/optimise/emit)")
		return nil
	}

	policyName := kql.Policy("")
	if policy != "" {
		policyName = kql.Policy(policy)
	}
	out, err := kql.Explain(context.Background(), dsn, query, policyName)
	if err != nil {
		return err
	}
	fmt.Printf("# Optimised SQL (%s), %d rule rewrites\n", out.Dialect, out.RuleChanges)
	fmt.Println(out.SQL)
	if len(out.Args) > 0 {
		fmt.Printf("\n# Bind args: %v\n", out.Args)
	}
	if len(out.Decisions) > 0 {
		fmt.Println("\n# Optimizer decisions")
		for _, d := range out.Decisions {
			fmt.Println("  " + d)
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
