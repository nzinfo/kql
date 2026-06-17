# O4 — Join 物理方案枚举

> 范围：`internal/optimizer/decision/join.go`
> 依赖：O3
> 阶段目标：为 JOIN 节点生成多个等价 AltPlan 并由策略选优

## 顺序化子目标

### O4.S1 — HashJoin AltPlan
- 产出：`decision/join_hash.go`（Cost 用 O1 join 选择率 + 内表大小；Emit 生成 hash join 物理 step）。
- 验收：内表小（< work_mem）时 Cost 低；Emit 输出 hash join hint（pg）。
- 测试来源：手写 + O5。

### O4.S2 — NestedLoop AltPlan
- 产出：`decision/join_nested.go`（Cost = outer_rows * inner_rows * cpu_tuple_cost；适合小表×小表或带范围条件）。
- 验收：双表都小时 Cost 低于 hash。
- 测试来源：手写。

### O4.S3 — IndexedLookup AltPlan（split 半边）
- 产出：`decision/join_indexed.go`（一边极小 → 客户端 nested loop，IN 列表批量查；用 stats indexes 判断是否有可用索引）。
- 验收：inner 表有索引且极小（< 阈值）时 Cost 低；生成 `WHERE id = ANY(...)` 物理 step。
- 测试来源：手写 + stats catalog 含索引。

### O4.S4 — MergeJoin AltPlan（可选）
- 产出：`decision/join_merge.go`（双表都按 join key 排序时 merge join；corr_vs 高时优先）。
- 验收：corr_vs 高的列 join 时 Cost 低。
- 测试来源：手写。

### O4.S5 — 策略切换演示与集成测试
- 产出：`decision/join_test.go`（Conservative vs Aggressive 在小表 join 时选不同方案；Explain 显示选择率与决策理由）。
- 验收：catalog 给定时，Conservative 选 NestedLoop 或交 pg，Aggressive 选 IndexedLookup。
- 测试来源：手写 + stats catalog。

## 阶段产出物
- `internal/optimizer/decision/join_*.go`
- 集成测试 + Explain 快照

## 风险与对策
| 风险 | 对策 |
|---|---|
| 索引信息不准 | IndexedLookup 走保守阈值 |
| split 半边查询网络开销 | Cost 含 Net 项；阈值的 Net 权重可调 |
| AltPlan 数量多 | 限制每 join 节点 ≤4 候选 |
