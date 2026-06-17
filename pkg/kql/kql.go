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
	"nzinfo/kql/internal/backend/pg"
	"nzinfo/kql/internal/backend/sqlite"
	"nzinfo/kql/internal/frontend/binder"
	"nzinfo/kql/internal/frontend/diagnostic"
	"nzinfo/kql/internal/frontend/parser"
	"nzinfo/kql/internal/ir"
	"nzinfo/kql/internal/optimizer/decision"
	"nzinfo/kql/internal/optimizer/rules"
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
// runs only the always-safe O2 rules by default, so this is opt-in).
func policyFor(p Policy, catalog interface{}) decision.DecisionPolicy {
	switch p {
	case PolicyAggressive:
		return decision.Aggressive{}
	case PolicyGated:
		// ConfidenceGated needs a catalog; nil → always conservative fallback.
		return decision.ConfidenceGated{}
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
// cost-based decision). This is what `kql explain` calls.
func Explain(ctx context.Context, dsn, query string, policyName Policy) (*ExplainOutput, error) {
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
	// O2 rules (always-safe rewrites) → fixpoint.
	ruleEngine := rules.NewEngine(rules.ConstantFold{}, rules.PredicatePushdown{}, rules.ColumnPrune{})
	_, ruleChanges := ruleEngine.Optimize(pipe)
	// O3 cost-based decision (opt-in via policyName).
	var decisions []string
	if policyName != "" {
		po := decision.PredicateOrder{Policy: policyFor(policyName, nil)}
		_, _, d := po.Apply(pipe)
		if d.Choice != "" {
			decisions = append(decisions, "["+d.PolicyName+"] "+d.Choice+": "+d.Reason)
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
	if isPgDSN(dsn) {
		return pg.New(dsn)
	}
	return sqlite.New(dsn)
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
	q, err := bk.Emit(pipe)
	if err != nil {
		return nil, fmt.Errorf("emit: %w", err)
	}
	r, err := bk.Exec(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("exec: %w", err)
	}
	return &Result{r}, nil
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
