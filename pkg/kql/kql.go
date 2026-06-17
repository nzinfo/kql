// Package kql is the public API for parsing, translating, and executing KQL
// queries against SQL backends.
//
// Minimal e2e (per docs/PROGRESS.md / user direction): Exec parses a KQL query,
// translates it to IR, emits SQLite SQL, and runs it. Only the SQLite backend
// is wired for now; pg/duckdb land with their backend lines.
//
// Example:
//
//	res, err := kql.Exec(ctx, ":memory:", `MyTable | where Count > 5 | take 10`)
//	for _, row := range res.Rows {
//	    fmt.Println(row)
//	}
package kql

import (
	"context"
	"fmt"
	"strings"

	"nzinfo/kql/internal/backend"
	"nzinfo/kql/internal/backend/duckdb"
	"nzinfo/kql/internal/backend/pg"
	"nzinfo/kql/internal/backend/sqlite"
	"nzinfo/kql/internal/exec"
	"nzinfo/kql/internal/frontend/binder"
	"nzinfo/kql/internal/frontend/diagnostic"
	"nzinfo/kql/internal/frontend/parser"
	"nzinfo/kql/internal/ir"
	"nzinfo/kql/internal/optimizer/cost"
	"nzinfo/kql/internal/optimizer/decision"
	"nzinfo/kql/internal/optimizer/rules"
	"nzinfo/kql/internal/optimizer/stats"
)

// Policy names the optimizer decision strategy for ExecWithPolicy/Explain.
// Maps to the O3 DecisionPolicy implementations.
type Policy string

const (
	PolicyConservative Policy = "conservative" // default: don't reorder with weak stats
	PolicyAggressive   Policy = "aggressive"   // always lowest estimated cost
	PolicyGated        Policy = "gated"        // aggressive when confidence high, else conservative
)

// policyFor builds the O3 DecisionPolicy for a name (used by Explain; Exec path
// runs only the always-safe O2 rules by default, so this is opt-in). catalog is
// optional (needed for ConfidenceGated to evaluate confidence; nil → conservative).
func policyFor(p Policy, catalog *stats.Catalog) decision.DecisionPolicy {
	switch p {
	case PolicyAggressive:
		return decision.Aggressive{}
	case PolicyGated:
		return decision.ConfidenceGated{Catalog: catalog}
	}
	return decision.Conservative{}
}

// Exec runs a KQL query against the database at dsn and returns the result.
// The backend is selected by dsn scheme: `postgres://`/`postgresql://` (or a
// key=value string containing `host=`/`postgres`) → pg; anything else
// (`:memory:`, `file:...`, a `.db` path) → sqlite.
//
// Parse/translate/bind errors are surfaced as a kql.Error wrapping the
// diagnostic list.
func Exec(ctx context.Context, dsn, query string) (*Result, error) {
	bk, err := openBackend(dsn)
	if err != nil {
		return nil, err
	}
	defer bk.Close()
	return ExecOn(ctx, bk, query)
}

// openBackend opens a backend chosen by the dsn scheme. SQLite is the default
// (prototype/test backend); pg is selected for postgres URLs/strings.
// Exported as OpenBackend so the CLI's explain path can emit dialect-correct
// SQL without re-implementing routing.
func OpenBackend(dsn string) (backend.Backend, error) { return openBackend(dsn) }

// ExplainOutput is the public result of Explain: emitted SQL + the optimizer's
// decision log. Returned by Explain for `kql explain` to render.
type ExplainOutput struct {
	SQL         string
	Args        []interface{}
	Dialect     backend.Dialect
	Decisions   []string // human-readable decision reasons
	RuleChanges int
}

// Explain parses + binds + optimises a query and returns the emitted SQL plus
// the optimizer's decision log, WITHOUT executing. policyName selects the O3
// strategy (conservative/aggressive/gated); pass "" for rules-only (no
// cost-based decision). statsPath optionally loads an O0 stats YAML catalog to
// drive selectivity-aware decisions (pass "" for no catalog → uniform/default
// selectivity). This is what `kql explain --stats <path>` calls.
func Explain(ctx context.Context, dsn, query string, policyName Policy, statsPath string) (*ExplainOutput, error) {
	bk, err := openBackend(dsn)
	if err != nil {
		return nil, err
	}
	defer bk.Close()
	pipe, err := ParseTranslate(query)
	if err != nil {
		return nil, err
	}
	// Bind (resolve column refs; rewrite Col.Name to physical names).
	var diags diagnostic.List
	if prov, ok := bk.(binderProvider); ok {
		if _, berr := binder.Bind(pipe, prov, &diags); berr != nil {
			return nil, fmt.Errorf("bind: %w", berr)
		}
		if diags.HasErrors() {
			return nil, &Error{diags: diags, stage: "bind"}
		}
	}
	// Optionally load the stats catalog for selectivity-aware decisions.
	var catalog *stats.Catalog
	if statsPath != "" {
		c, _, err := stats.Load(statsPath)
		if err != nil {
			return nil, fmt.Errorf("load stats %q: %w", statsPath, err)
		}
		catalog = c
	}
	// O2 rules (always-safe rewrites) → fixpoint.
	ruleEngine := rules.NewEngine(rules.ConstantFold{}, rules.PredicatePushdown{}, rules.ColumnPrune{})
	_, ruleChanges := ruleEngine.Optimize(pipe)
	// O3 cost-based decision (opt-in via policyName).
	var decisions []string
	if policyName != "" {
		pol := policyFor(policyName, catalog)
		est := cost.NewEstimator(catalog)
		po := decision.PredicateOrder{Policy: pol, Estimator: est}
		// Determine the source table for selectivity lookups.
		if src, ok := pipe.Source.(*ir.SourceTable); ok {
			po.Table = src.Table
		}
		_, _, d := po.Apply(pipe)
		if d.Choice != "" {
			decisions = append(decisions, "["+d.PolicyName+"] "+d.Choice+": "+d.Reason)
		}
		// O4 join-method planning (needs a catalog to cost joins).
		if catalog != nil {
			jp := decision.JoinPlan{
				Policy:  pol,
				Catalog: catalog,
				Weights: cost.DefaultWeights(bk.Dialect().String()),
			}
			_, _, jd := jp.Apply(pipe)
			if jd.Choice != "" {
				decisions = append(decisions, "["+jd.PolicyName+"] "+jd.Choice+": "+jd.Reason)
			}
		}
	}
	q, err := bk.Emit(pipe)
	if err != nil {
		return nil, fmt.Errorf("emit: %w", err)
	}
	return &ExplainOutput{
		SQL: q.SQL, Args: q.Args, Dialect: bk.Dialect(),
		Decisions: decisions, RuleChanges: ruleChanges,
	}, nil
}

func openBackend(dsn string) (backend.Backend, error) {
	if isDuckDBDSN(dsn) {
		return duckdb.New(duckDBPath(dsn))
	}
	if isPgDSN(dsn) {
		return pg.New(dsn)
	}
	return sqlite.New(dsn)
}

// isDuckDBDSN reports whether dsn refers to DuckDB (duckdb:// prefix or a
// .duckdb file path).
func isDuckDBDSN(dsn string) bool {
	low := strings.ToLower(dsn)
	return strings.HasPrefix(low, "duckdb://") || strings.HasSuffix(low, ".duckdb")
}

// duckDBPath extracts the DuckDB path from a dsn (strips duckdb:// prefix;
// returns "" for in-memory).
func duckDBPath(dsn string) string {
	if strings.HasPrefix(strings.ToLower(dsn), "duckdb://") {
		return dsn[len("duckdb://"):]
	}
	return dsn
}

// isPgDSN reports whether dsn refers to a PostgreSQL database.
func isPgDSN(dsn string) bool {
	low := strings.ToLower(dsn)
	if strings.HasPrefix(low, "postgres://") || strings.HasPrefix(low, "postgresql://") {
		return true
	}
	// key=value conninfo form: host=... user=... — treat as pg if it has host= or
	// postgres markers and isn't a file path.
	if strings.Contains(low, "host=") || strings.Contains(low, "user=") && strings.Contains(low, "dbname=") {
		return true
	}
	return false
}

// OptimizationConfig controls the optimizer's cost-based decisions during
// execution (not just explain). Pass a zero value for defaults (rules only,
// no cost-based reorder). Set StatsPath + Policy to enable selectivity-aware
// predicate ordering in the actual executed SQL.
type OptimizationConfig struct {
	StatsPath string // path to O0 stats YAML ("" = no catalog)
	Policy    Policy // "" = no cost-based decision (rules only)
}

// ExecOptimized is like ExecOn but applies cost-based optimization (predicate
// ordering via stats catalog + policy) BEFORE emit, so the executed SQL
// benefits from selectivity-aware reordering. This is the production path for
// performance-critical queries with a known stats catalog.
func ExecOptimized(ctx context.Context, bk backend.Backend, query string, opt OptimizationConfig) (*Result, error) {
	pipe, err := ParseTranslate(query)
	if err != nil {
		return nil, err
	}
	// Bind.
	var diags diagnostic.List
	if prov, ok := bk.(binderProvider); ok {
		if _, berr := binder.Bind(pipe, prov, &diags); berr != nil {
			return nil, fmt.Errorf("bind: %w", berr)
		}
		if diags.HasErrors() {
			return nil, &Error{diags: diags, stage: "bind"}
		}
	}
	// Load stats catalog if given.
	var catalog *stats.Catalog
	if opt.StatsPath != "" {
		c, _, err := stats.Load(opt.StatsPath)
		if err != nil {
			return nil, fmt.Errorf("load stats %q: %w", opt.StatsPath, err)
		}
		catalog = c
	}
	// O2 rules.
	defaultEngine.Optimize(pipe)
	// O3 cost-based decision (opt-in).
	if opt.Policy != "" {
		pol := policyFor(opt.Policy, catalog)
		est := cost.NewEstimator(catalog)
		po := decision.PredicateOrder{Policy: pol, Estimator: est}
		if src, ok := pipe.Source.(*ir.SourceTable); ok {
			po.Table = src.Table
		}
		po.Apply(pipe)
		// O4 join-method planning (needs a catalog to cost joins).
		if catalog != nil {
			jp := decision.JoinPlan{
				Policy:  pol,
				Catalog: catalog,
				Weights: cost.DefaultWeights(bk.Dialect().String()),
			}
			jp.Apply(pipe)
		}
	}
	// Execute with PostProc support.
	er, err := exec.ExecPipeline(ctx, bk, pipe)
	if err != nil {
		return nil, err
	}
	return &Result{&backend.Result{
		Columns: execColsToBackend(er.Columns),
		Rows:    er.Rows,
	}}, nil
}

// ExecOn runs a KQL query against an already-open backend. The caller owns the
// backend's lifecycle. This is the backend-agnostic entry point; Exec is the
// SQLite convenience wrapper.
//
// If the backend also implements binder.SchemaProvider (the sqlite backend
// does), column references are validated at bind time — producing friendly
// KQL009 "column not found" errors with KQL context BEFORE execution, rather
// than a raw SQLite "no such column" at runtime.
func ExecOn(ctx context.Context, bk backend.Backend, query string) (*Result, error) {
	pipe, err := ParseTranslate(query)
	if err != nil {
		return nil, err
	}
	// Bind: validate column references against the source schema (if the
	// backend can provide one). Bind errors are surfaced like parse errors.
	var diags diagnostic.List
	if prov, ok := bk.(binderProvider); ok {
		if _, berr := binder.Bind(pipe, prov, &diags); berr != nil {
			return nil, fmt.Errorf("bind: %w", berr)
		}
		if diags.HasErrors() {
			return nil, &Error{diags: diags, stage: "bind"}
		}
	}
	// Optimize: run the rule-based IR rewriter (predicate pushdown, etc.) to
	// fixpoint. Rules are dialect-agnostic; the rewritten pipeline is
	// semantically equivalent and emits the same results. Optimization never
	// fails the query (a rule bug would change IR but keep emitting valid SQL).
	defaultEngine.Optimize(pipe)
	// Execute: emit SQL + run on backend, with client-side post-processing
	// for operators the backend can't express (mv-expand etc.).
	er, err := exec.ExecPipeline(ctx, bk, pipe)
	if err != nil {
		return nil, err
	}
	// Convert exec.Result to backend.Result for the public Result wrapper.
	return &Result{&backend.Result{
		Columns: execColsToBackend(er.Columns),
		Rows:    er.Rows,
	}}, nil
}

// execColsToBackend converts exec column names to backend.ResultColumn.
func execColsToBackend(cols []string) []backend.ResultColumn {
	out := make([]backend.ResultColumn, len(cols))
	for i, c := range cols {
		out[i] = backend.ResultColumn{Name: c}
	}
	return out
}

// defaultEngine is the standard rule engine applied to every query:
// PredicatePushdown, ConstantFold, ColumnPrune. More rules land as O2 grows.
// Order matters: ConstantFold before Pushdown lets a folded-away filter
// short-circuit; Pushdown before ColumnPrune so pruned columns reflect the
// final predicate set.
var defaultEngine = rules.NewEngine(
	rules.ConstantFold{},
	rules.PredicatePushdown{},
	rules.ColumnPrune{},
	rules.PredicatePushdownUnion{},
)

// binderProvider is the optional interface a backend implements to enable
// bind-time column validation. Defined locally (not imported as
// binder.SchemaProvider) to avoid pkg/kql depending on an internal sub-package
// type by name in its public surface — but it's structurally identical, so the
// sqlite backend satisfies it via its Schema method returning *binder.Schema.
type binderProvider interface {
	Schema(table string) (*binder.Schema, error)
}

// ParseTranslate parses a KQL query and translates it to IR, returning the
// pipeline and any diagnostics as an error. Exposed so callers can dry-run /
// `kql explain` without executing.
func ParseTranslate(query string) (*ir.Pipeline, error) {
	p := parser.New("kql", query)
	script := p.Parse()
	var diags diagnostic.List
	// Surface parse errors first.
	pDiags := p.Diagnostics()
	for _, d := range pDiags.Items() {
		diags.Add(d)
	}
	if pDiags.HasErrors() {
		return nil, &Error{diags: diags, stage: "parse"}
	}
	pipe := ir.Translate(script, &diags)
	if diags.HasErrors() {
		return nil, &Error{diags: diags, stage: "translate"}
	}
	if pipe == nil {
		return nil, &Error{diags: diags, stage: "translate", msg: "no pipeline produced"}
	}
	return pipe, nil
}

// Result wraps a backend.Result.
type Result struct {
	inner *backend.Result
}

// WrapResult exposes a backend.Result as a public Result. Intended for tooling
// (e.g. the CLI's tests) that builds a Result from a backend.Result directly.
func WrapResult(r *backend.Result) *Result { return &Result{inner: r } }

// Columns returns the result columns.
func (r *Result) Columns() []backend.ResultColumn { return r.inner.Columns }

// Rows returns the result rows. Each row is a slice of cell values (driver-
// native types: int64, float64, string, []byte, nil, time.Time).
func (r *Result) Rows() [][]interface{} { return r.inner.Rows }

// Error is returned by Exec/ParseTranslate when a parse or translate diagnostic
// is produced. It carries the full diagnostic list for inspection.
type Error struct {
	diags diagnostic.List
	stage string // "parse" or "translate"
	msg   string
}

// Error implements the error interface.
func (e *Error) Error() string {
	if e.msg != "" {
		return fmt.Sprintf("kql %s: %s", e.stage, e.msg)
	}
	return fmt.Sprintf("kql %s: %v", e.stage, e.diags.Render())
}

// Diagnostics returns the underlying diagnostic list.
func (e *Error) Diagnostics() []string { return e.diags.Render() }
