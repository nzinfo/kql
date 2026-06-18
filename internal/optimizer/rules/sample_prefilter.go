// Package rules — Sample pre-filter (O6.S3).
//
// When a query has an extremely selective WHERE followed by a large TAKE
// (e.g. `where event_id == 42 | take 1000000`), it's faster to first fetch
// matching rowids, then take from the filtered set. This avoids scanning
// the full table when the filter is highly selective.
//
// The rule transforms:
//
//	T | where <very selective pred> | take N
//
// into:
//
//	T | where <pred> | take N  (unchanged — the rule is advisory; it marks
//	                            the pipeline for the exec layer to use a
//	                            two-phase strategy if N is large and sel is tiny)
//
// Currently this rule is a no-op marker (records the optimization opportunity
// for future exec-layer enhancement). The cost model identifies when it would
// help; the exec layer can check the marker and use a rowid pre-filter query.
package rules

type SamplePrefilter struct {
	Catalog interface{} // *stats.Catalog (typed loosely to avoid import cycle)
}

func (SamplePrefilter) Name() string { return "SamplePrefilter" }

// Apply checks if the pipeline matches the sample-prefilter pattern. Currently
// a no-op (returns false) — the actual optimization would require exec-layer
// support for two-phase rowid queries.
func (SamplePrefilter) Apply(pipe interface{}, sr interface{}) (interface{}, bool) {
	// TODO: implement when exec layer supports rowid pre-filtering.
	// The pattern is: Filter(selectivity < 0.01) followed by Limit(N > 10000).
	// When detected, mark the pipeline for two-phase execution:
	//   Phase 1: SELECT rowid FROM T WHERE <pred> LIMIT N
	//   Phase 2: SELECT * FROM T WHERE rowid IN (phase1_result)
	return nil, false
}
