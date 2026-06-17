# O1 — 选择率估算器 + 代价模型

> 范围：`internal/optimizer/cost/`
> 依赖：O0
> 阶段目标：把谓词/JOIN 翻成选择率，把物理方案翻成 Cost

## 顺序化子目标

### O1.S1 — 选择率估算核心
- 产出：`cost/selectivity.go`（按 DESIGN.md 6.3 节公式表实现：=, <, >, in, is null, between, like, AND, OR）。
- 验收：MCV 命中时 = MCV 频率；不在 MCV = 1/card；无统计 = 0.1；单元测试覆盖每类谓词。
- 测试来源：手写 + DESIGN.md 公式表。

### O1.S2 — JOIN 选择率
- 产出：`cost/selectivity_join.go`（`t1.a = t2.a` → 1/max(card_a, card_b)；多列 join 用独立假设 + corr 修正）。
- 验收：双表 join 选择率合理；与 pg 估算同数量级。
- 测试来源：手写。

### O1.S3 — corr 修正
- 产出：`cost/corr.go`（用 corr_vs 修正独立假设在相关列上的高估）。
- 验收：强相关列（rho=0.9）的复合谓词选择率不被严重高估。
- 测试来源：手写。

### O1.S4 — Cost 结构与权重
- 产出：`cost/cost.go`（Cost{IO, CPU, Net, Mem} + Total(weights)）、`cost/weights.go`（按后端默认权重：pg/duckdb/sqlite 不同）。
- 验收：Cost 可比较；权重可被 catalog 覆盖。
- 测试来源：手写。

### O1.S5 — 降级路径
- 产出：无统计时选择率 0.1、Cost 用粗估；置信度低于阈值时标记 LowConfidence。
- 验收：空 catalog 不 panic；优化器可据此走保守路径。
- 测试来源：手写。

## 阶段产出物
- `internal/optimizer/cost/`（selectivity/selectivity_join/corr/cost/weights）
- 单元测试

## 风险与对策
| 风险 | 对策 |
|---|---|
| 选择率估算偏差大 | S3 corr 修正 + 未来 EXPLAIN 反馈 |
| JOIN 选择率低估导致坏计划 | S2 与 pg 估算对齐 |
| Cost 不可加 | S4 Total 加权，权重可调 |
