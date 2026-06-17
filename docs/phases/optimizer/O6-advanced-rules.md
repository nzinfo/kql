# O6（可选）— ViewMatch / 两阶段聚合 / 样本预筛

> 范围：`internal/optimizer/rules/`（增强规则）
> 依赖：O2、O5
> 阶段目标：增量增益规则，按需启用

## 顺序化子目标

### O6.S1 — ViewMatch
- 产出：`rules/view_match.go`（匹配 `stats.views`，改写 source；含语义等价校验）。
- 验收：catalog 含 `orders_daily_summary` view 时，`summarize count() by bin(created_at, 1d)` 被改写为读 view。
- 测试来源：手写 + stats catalog 含 view。

### O6.S2 — 两阶段聚合
- 产出：`rules/agg_two_stage.go`（大表 summarize 先按分片列局部聚合再合并；分片列选择基于 stats）。
- 验收：大表（row_count > 阈值）summarize 生成两阶段 plan；Explain 显示决策。
- 测试来源：手写 + stats catalog 大表。

### O6.S3 — 样本预筛
- 产出：`rules/sample_prefilter.go`（极选择性 where + 大 take，先拉匹配 rowid 集回引擎，再批量 `WHERE id = ANY(...)` 拉明细）。
- 验收：高选择性谓词 + 大 take 时生成 rowid 预筛 plan。
- 测试来源：手写 + stats catalog 高选择性列。

### O6.S4 — 增强规则回归测试
- 产出：每条规则有 catalog 驱动的正/反例测试。
- 验收：规则启用/禁用切换行为可观察；O5 代价对比显示增益。
- 测试来源：手写 + stats catalog。

## 阶段产出物
- `internal/optimizer/rules/{view_match,agg_two_stage,sample_prefilter}.go`
- 测试 + 代价对比

## 风险与对策
| 风险 | 对策 |
|---|---|
| ViewMatch 语义不严格等价 | S1 改写前严格校验 view 定义等价于原查询 |
| 两阶段聚合结果偏差 | S2 仅对满足结合律的聚合（count/sum/min/max）启用；avg/std 需特殊处理 |
| 样本预筛 rowid 集过大 | S3 rowid 集大小阈值；超限回退常规 plan |
