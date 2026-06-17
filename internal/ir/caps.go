package ir

// Caps are FuncCall capability bits that tell each backend how to emit a
// function. New vs rust-kql/kqlparser (which target a single backend and don't
// need this). Filled by the F7 builtin table (I1.S4 / I3); defaults apply until
// F7 lands.
//
// The bits are not mutually exclusive: a function might be expressible as a
// plain SQL scalar on one backend but need a UDF on another. Per-backend
// resolution happens in the backend layer; Caps here is the union of what the
// builtin table knows.
type Caps struct {
	// SQLExpr: the function can be emitted as a plain SQL scalar expression
	// (e.g. abs, sqrt, now, coalesce). Set for most standard scalar fns.
	SQLExpr bool

	// Aggregate: the function is an aggregate (count/sum/avg/min/max/...).
	// Backends emit it in a GROUP BY aggregate position.
	Aggregate bool

	// Window: the function requires window-function syntax (series_*, percentile
	// in some engines). Reserved for future stages.
	Window bool

	// NeedsUDF: the function cannot be expressed in plain SQL on the target
	// backend and needs a user-defined function (pg plpgsql, duckdb UDF).
	NeedsUDF bool

	// NeedsPostProc: the function must be computed client-side — the backend
	// emits the raw inputs and the Go exec layer computes the result on the
	// returned rows. Used when even a UDF is infeasible (e.g. sqlite limits).
	NeedsPostProc bool
}

// DefaultCaps returns reasonable defaults for a function of the given category.
// Used by the translator until the F7 builtin table is wired in (I1.S4).
func DefaultCaps(name string, isAggregate bool) Caps {
	if isAggregate {
		return Caps{Aggregate: true, SQLExpr: true}
	}
	// Scalar function: assume it's a plain SQL expr until proven otherwise.
	return Caps{SQLExpr: true}
}
