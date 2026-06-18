// Package exec — the PostProc stage execution registry.
//
// This is the general, rule-engine-inspired mechanism for client-side stage
// execution. Instead of an ad-hoc type switch in applyPostProc (which must be
// edited for every new PostProc operator), each operator type registers a
// StageExecutor via RegisterExecutor at init time. The engine dispatches by
// IR type name, so adding a new operator (e.g. MakeSeries) is:
//
//	1. define the IR stage type
//	2. write an executor function
//	3. RegisterExecutor("MakeSeries", execFn)
//
// No engine code changes needed — the same shape as the optimizer's rule
// registry (rules.NewEngine + RewriteRule.Apply).
//
// Two flavors of registration:
//   - PostProc executor: runs client-side on the rowset (mv-expand, parse, ...)
//   - Always-client stage: stages that ALWAYS run client-side once the pipeline
//     has entered the PostProc region (Aggregate count/sum, Limit, Project)
//
// The isPostProc predicate is likewise registry-driven: a stage is a PostProc
// boundary iff its type name is in the postProcBoundary set.
package exec

import (
	"fmt"
	"reflect"
	"sync"
)

// StageExecutor runs one IR stage client-side over the current result set.
// It returns the transformed result. Errors are propagated to the caller.
type StageExecutor func(res *Result, st interface{}) (*Result, error)

// postProcRegistry maps IR stage type name → executor. Two tiers:
//   - boundaryExecutors: stages that START the PostProc region (mv-expand, parse).
//     findPostProcBoundary returns the first such stage.
//   - followerExecutors: stages that run client-side WITHIN the PostProc region
//     (Aggregate, Limit, Project) — applied to rows already produced by a
//     boundary executor.
var (
	boundaryExecutors  = map[string]StageExecutor{}
	followerExecutors  = map[string]StageExecutor{}
	registryMu         sync.RWMutex
)

// RegisterBoundaryExecutor registers a PostProc boundary executor for the
// given IR stage type name (e.g. "MvExpand", "Parse"). Called from init().
func RegisterBoundaryExecutor(typeName string, fn StageExecutor) {
	registryMu.Lock()
	defer registryMu.Unlock()
	boundaryExecutors[typeName] = fn
}

// RegisterFollowerExecutor registers a client-side executor for a stage that
// runs WITHIN the PostProc region (after a boundary stage). Called from init().
func RegisterFollowerExecutor(typeName string, fn StageExecutor) {
	registryMu.Lock()
	defer registryMu.Unlock()
	followerExecutors[typeName] = fn
}

// stageTypeName returns the IR stage's type name without package qualifier
// (e.g. *ir.MvExpand → "MvExpand").
func stageTypeName(st interface{}) string {
	t := reflect.TypeOf(st)
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	return t.Name()
}

// isPostProcBoundary reports whether a stage starts the PostProc region.
// Registry-driven: true iff the stage's type has a registered boundary executor.
func isPostProcBoundary(st interface{}) bool {
	registryMu.RLock()
	defer registryMu.RUnlock()
	_, ok := boundaryExecutors[stageTypeName(st)]
	return ok
}

// dispatchPostProc looks up and runs the executor for one stage. Boundary
// stages use boundaryExecutors; follower stages use followerExecutors. Returns
// (result, handled) where handled=false means no executor registered (the stage
// is a no-op pass-through in the PostProc region).
func dispatchPostProc(res *Result, st interface{}) (*Result, bool, error) {
	name := stageTypeName(st)
	registryMu.RLock()
	// Try boundary first (a boundary stage can also appear mid-region).
	if fn, ok := boundaryExecutors[name]; ok {
		registryMu.RUnlock()
		out, err := fn(res, st)
		return out, true, err
	}
	if fn, ok := followerExecutors[name]; ok {
		registryMu.RUnlock()
		out, err := fn(res, st)
		return out, true, err
	}
	registryMu.RUnlock()
	return res, false, nil
}

// findPostProcBoundaryIndex returns the index of the first PostProc boundary
// stage, or -1 if none. Registry-driven (no type switch).
func findPostProcBoundaryIndex(stages []interface{}) int {
	for i, st := range stages {
		if isPostProcBoundary(st) {
			return i
		}
	}
	return -1
}

// ensure fmt is used (registry errors reference it).
var _ = fmt.Errorf
