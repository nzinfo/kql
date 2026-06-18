// Package decision — multi-table join order enumeration (O4 extension).
//
// JoinPlan (join_plan.go) costs each join node independently and selects its
// physical method. For a chain like `A | join B | join C`, the ORDER of joins
// also affects cost: building A⋈B first then ⋈C may be far cheaper than the
// text order if A and C are small and B is large.
//
// EnumerateJoinOrder computes the lowest-cost left-deep order for a chain of
// COMMUTATIVE joins (inner / innerunique) via dynamic programming (the classic
// System-R approach). Non-commutable joins (left/right/full) stay in text order
// — reordering them changes KQL semantics (row survival).
//
// This function is pure (no IR mutation); ApplyJoinOrder stamps the chosen
// order back into the pipeline by reordering the Join stages. ApplyJoinOrder is
// a NO-OP when Catalog is nil or there are fewer than 2 commutable joins.
package decision

import (
	"strings"

	"nzinfo/kql/internal/ir"
	"nzinfo/kql/internal/optimizer/cost"
	"nzinfo/kql/internal/optimizer/stats"
)

// joinChain extracts a maximal run of consecutive commutable *ir.Join stages
// from pipe.Stages, starting at startIdx. Returns the stage indices + the table
// each join's right side references. A non-commutable join or a non-join stage
// terminates the chain.
func joinChain(pipe *ir.Pipeline, startIdx int) (indices []int, rights []string) {
	for i := startIdx; i < len(pipe.Stages); i++ {
		j, ok := pipe.Stages[i].(*ir.Join)
		if !ok {
			break
		}
		if !isCommutativeJoin(j.Kind) {
			if len(indices) >= 2 {
				return indices, rights
			}
			return nil, nil
		}
		indices = append(indices, i)
		rights = append(rights, joinRightTableName(j))
	}
	if len(indices) < 2 {
		return nil, nil
	}
	return indices, rights
}

// isCommutativeJoin reports whether the join kind permits reordering without
// changing row survival. inner / innerunique are commutable; left/right/full
// are not.
func isCommutativeJoin(k ir.JoinKind) bool {
	switch k {
	case ir.JoinDefault, ir.JoinInner, ir.JoinInnerUnique:
		return true
	}
	return false
}

// EnumerateJoinOrder computes the lowest-cost left-deep order for a chain of
// joins. The base table (pipe source) is always the left-most leaf; the joins
// (each adding its right table) are reordered. Uses System-R DP:
//
//	dp[{T}] = best cost to produce the set of tables T joined
//	dp[{T}] = min over t in T of dp[{T - t}] + cost(join t to rest)
//
// Returns the optimal order of right-table names (a permutation of rights), or
// rights unchanged if no reordering improves cost (or stats are insufficient).
// joinOns maps each right table name → its join ON conditions.
func EnumerateJoinOrder(catalog *stats.Catalog, baseTable string, baseCard int64, rights []string, joinOns map[string][]ir.Expr, weights cost.CostWeights) []string {
	n := len(rights)
	if n < 2 || catalog == nil {
		return rights
	}
	est := cost.NewEstimator(catalog)
	cardOf := map[string]int64{}
	cardOf[baseTable] = baseCard
	for _, t := range rights {
		c, _ := tableCard(catalog, t)
		if c == 0 {
			return rights // can't estimate → don't reorder
		}
		cardOf[t] = c
	}

	full := (1 << n) - 1
	type state struct {
		cost  cost.Cost
		prev  int  // index of the last table added to reach this subset
		out   int64
		valid bool
	}
	dp := make([]state, 1<<n)
	// Singletons: each table joined directly to the base.
	for i, t := range rights {
		mask := 1 << i
		on := joinOns[t]
		if on == nil {
			return rights // missing join condition → can't cost
		}
		sel := est.JoinSelectivity(baseTable, t, on, baseCard, cardOf[t])
		out := est.OutputCardinality(baseTable, t, on, baseCard, cardOf[t])
		_ = sel
		c := approxJoinCost(baseCard, cardOf[t], out, weights)
		dp[mask] = state{cost: c, prev: i, out: out, valid: true}
	}
	// Build up larger subsets.
	for size := 2; size <= n; size++ {
		for mask := 1; mask < (1 << n); mask++ {
			if bitsCount(mask) != size {
				continue
			}
			for i := 0; i < n; i++ {
				if mask&(1<<i) == 0 {
					continue
				}
				prevMask := mask ^ (1 << i)
				if !dp[prevMask].valid {
					continue
				}
				t := rights[i]
				on := joinOns[t]
				if on == nil {
					continue
				}
				leftOut := dp[prevMask].out
				sel := est.JoinSelectivity("", t, on, leftOut, cardOf[t])
				out := est.OutputCardinality("", t, on, leftOut, cardOf[t])
				_ = sel
				c := dp[prevMask].cost
				c = c.Add(approxJoinCost(leftOut, cardOf[t], out, weights))
				// Compare via weighted total (Cost has no Less method).
				if !dp[mask].valid || c.Total(weights) < dp[mask].cost.Total(weights) {
					dp[mask] = state{cost: c, prev: i, out: out, valid: true}
				}
			}
		}
	}
	if !dp[full].valid {
		return rights
	}
	// Reconstruct the optimal order by walking back from full.
	order := make([]int, 0, n)
	mask := full
	for mask != 0 {
		s := dp[mask]
		order = append(order, s.prev)
		mask ^= 1 << s.prev
	}
	out := make([]string, n)
	for i, idx := range order {
		out[n-1-i] = rights[idx]
	}
	// If the optimal equals the input order, return input (no-op signal).
	for i := range rights {
		if !strings.EqualFold(out[i], rights[i]) {
			return out
		}
	}
	return rights
}

// approxJoinCost is a simplified Hash-vs-NestLoop cost for ORDER comparison.
// It picks the cheaper of HashJoin (build on smaller, probe larger) and
// NestLoop. This is intentionally simpler than join_cost.go's full combinator
// set — join ORDER cares about relative cost, not absolute, so a consistent
// estimator suffices. The actual physical-method selection still runs in
// JoinPlan after reordering.
func approxJoinCost(leftCard, rightCard, outCard int64, w cost.CostWeights) cost.Cost {
	small, large := leftCard, rightCard
	if rightCard < leftCard {
		small, large = rightCard, leftCard
	}
	hashCPU := float64(small+large) * w.CPU
	hashMem := float64(small) * 64 // ~64 bytes/hash entry
	hash := cost.Cost{CPU: hashCPU, Mem: hashMem}
	nlCPU := float64(leftCard) * float64(rightCard) * w.CPU
	nl := cost.Cost{CPU: nlCPU}
	if nl.Total(w) < hash.Total(w) {
		return nl
	}
	return hash
}

// ApplyJoinOrder reorders chains of commutable joins in the pipeline to the
// lowest-cost order. Returns changed=true if any reordering occurred. It is a
// NO-OP when Catalog is nil. After reordering, the caller should re-run
// JoinPlan so physical-method hints match the new order.
//
// Reordering moves the *ir.Join stage objects (and their position in Stages)
// to match the chosen order. ON conditions are NOT rewritten — they reference
// columns by name, which are order-independent for inner/innerunique joins.
func ApplyJoinOrder(pipe *ir.Pipeline, catalog *stats.Catalog, weights cost.CostWeights) (*ir.Pipeline, bool) {
	if pipe == nil || catalog == nil || len(pipe.Stages) < 2 {
		return pipe, false
	}
	baseTable := sourceTableName(pipe)
	if baseTable == "" {
		return pipe, false
	}
	baseCard, _ := tableCard(catalog, baseTable)
	if baseCard == 0 {
		return pipe, false
	}

	changed := false
	// Scan for join chains; reorder each. We mutate a copy of Stages.
	newStages := make([]ir.Stage, len(pipe.Stages))
	copy(newStages, pipe.Stages)

	i := 0
	for i < len(newStages) {
		indices, rights := joinChain(&ir.Pipeline{Stages: newStages}, i)
		if len(indices) < 2 {
			i++
			continue
		}
		// Build joinOns: right table → its join ON conditions.
		joinOns := map[string][]ir.Expr{}
		for _, idx := range indices {
			j := newStages[idx].(*ir.Join)
			t := joinRightTableName(j)
			joinOns[t] = j.On
		}
		optimal := EnumerateJoinOrder(catalog, baseTable, baseCard, rights, joinOns, weights)
		if len(optimal) != len(rights) {
			i += len(indices)
			continue
		}
		// Check if order changed.
		changed_ := false
		for k := range rights {
			if !strings.EqualFold(optimal[k], rights[k]) {
				changed_ = true
				break
			}
		}
		if !changed_ {
			i += len(indices)
			continue
		}
		// Reorder the join stages to match optimal.
		// Map optimal table name → source join stage (first match).
		used := map[int]bool{}
		reordered := make([]ir.Stage, len(indices))
		for k, tableName := range optimal {
			for _, idx := range indices {
				if used[idx] {
					continue
				}
				j := newStages[idx].(*ir.Join)
				if strings.EqualFold(joinRightTableName(j), tableName) {
					reordered[k] = j
					used[idx] = true
					break
				}
			}
		}
		// Place reordered back into newStages at the chain position.
		for k, idx := range indices {
			newStages[idx] = reordered[k]
		}
		changed = true
		i += len(indices)
	}
	if changed {
		pipe.Stages = newStages
	}
	return pipe, changed
}

// bitsCount returns the number of set bits (population count).
func bitsCount(x int) int {
	c := 0
	for x != 0 {
		c += x & 1
		x >>= 1
	}
	return c
}
