# I3 — 能力位与列绑定语义

> 范围：`internal/ir/caps.go` + `internal/ir/column.go`（夯实规则）
> 依赖：I2
> 阶段目标：明确能力位规则与列绑定边界语义，保证后端能正确分流

## 顺序化子目标

### I3.S1 — 能力位规则文档化
- 产出：`ir/caps.go` 内 godoc + `docs/capabilities.md`（决策表：何时 NeedsUDF、何时 NeedsPostProc，按 pg/duckdb/sqlite 三后端分列）。
- 验收：每个高频函数（count/sum/avg/percentile/series_*/bin/now）三后端能力位明确。
- 测试来源：F7 表 + 后端能力矩阵。

### I3.S2 — 列绑定边界处理
- 产出：`ir/column.go` 增强子查询/view/CTE 边界的重新绑定逻辑；列 ID 命名空间管理。
- 验收：子查询引入的列与外层不冲突；view 改写（O6）后列引用更新。
- 测试来源：手写 + T3 含子查询的用例。

### I3.S3 — 投影列追踪
- 产出：`ir/projection.go`（每个 Stage 输出列集合；供后续 Stage 与 O2 列裁剪使用）。
- 验收：`project a, b` 输出列集 = {a, b}；`extend x = 1` 输出 = 输入 ∪ {x}；`summarize` 输出 = by + aggregates。
- 测试来源：T3 P0。

### I3.S4 — 能力位与列绑定单元测试
- 产出：`ir/caps_test.go` + `ir/column_test.go`。
- 验收：每个能力位规则有正反例；列绑定边界有边界用例。
- 测试来源：手写。

## 阶段产出物
- `internal/ir/caps.go`（含文档）
- `docs/capabilities.md`
- `internal/ir/projection.go`
- 单元测试

## 风险与对策
| 风险 | 对策 |
|---|---|
| 能力位规则主观 | S1 决策表 + 三后端评审 |
| 列绑定边界复杂 | S2 命名空间显式建模 |
| 投影列追踪与 schema 流重复 | I3 投影只描述"输出形状"，schema 流（F5.S5）描述"类型与绑定"；分层清晰 |
