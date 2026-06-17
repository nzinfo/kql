# B1 — Backend 接口与 Emit 框架

> 范围：`internal/backend/` + `internal/backend/sqlbuild/`
> 依赖：I1（IR）、O3（PhysicalPlan/AltPlan）
> 阶段目标：定义统一后端接口 + PhysicalPlan → SQL 的通用骨架

## 顺序化子目标

### B1.S1 — Backend 接口
- 产出：`backend/backend.go`（Backend 接口：Dialect() / Emit(plan) → (sql, args, error) / Capabilities()）、`backend/dialect.go`（Dialect 枚举 + 各后端能力位）。
- 验收：mock PhysicalPlan 能生成"形状正确"的 SQL。
- 测试来源：手写 mock plan。

### B1.S2 — SQL builder 工具
- 产出：`backend/sqlbuild/builder.go`（标识符引用、参数占位符、CTE 嵌套、类型映射）、`sqlbuild/ident.go`（按方言 quote 标识符）、`sqlbuild/param.go`（$1/? 抽象）。
- 验收：pg 用 `"col"` + `$1`；sqlite 用 `"col"` + `?`；duckdb 兼容 pg 风格。
- 测试来源：手写。

### B1.S3 — PhysicalStep 抽象
- 产出：`backend/physical.go`（PhysicalStep 接口 + 实现：PSource/PFilter/PProject/PAggregate/PJoin/PSort/PLimit/PCTE/PUDF）。
- 验收：optimizer 输出 PhysicalStep，后端只读不修改。
- 测试来源：手写。

### B1.S4 — Emit 编排框架
- 产出：`backend/emit.go`（按 PhysicalStep 序列生成 SQL；处理"能合就合"vs 断 CTE 决策，由 optimizer 在 PhysicalStep 标记）。
- 验收：相邻可合 step 合并进单 SELECT；summarize/join/窗口 step 触发 CTE。
- 测试来源：手写。

### B1.S5 — 类型映射
- 产出：`backend/types.go`（KQL 类型 → 各方言 SQL 类型映射表）。
- 验收：datetime/timespan/dynamic/long 在三后端映射正确。
- 测试来源：手写。

## 阶段产出物
- `internal/backend/`（backend/dialect/physical/emit/types）
- `internal/backend/sqlbuild/`
- mock plan 测试

## 风险与对策
| 风险 | 对策 |
|---|---|
| 方言差异散落 | sqlbuild 集中封装 |
| PhysicalStep 与 IR 耦合 | PhysicalStep 是 optimizer 输出，backend 只读 |
| 类型映射不全 | S5 表驱动 + 缺失时报错 |
