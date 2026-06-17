# O2 — 三条核心规则（规则重写阶段）

> 范围：`internal/optimizer/rules/`
> 依赖：I1（IR）、O0（StatsReader）、I5（等价性测试）
> 阶段目标：方言无关 IR→IR，覆盖"pg 一定会做或不会更差"的安全优化

## 顺序化子目标

### O2.S1 — RewriteRule 接口与 engine
- 产出：`rules/rule.go`（RewriteRule 接口：Apply(*Pipeline, StatsReader) → (*Pipeline, changed bool)）、`rules/engine.go`（按依赖顺序跑 + 不动点上限防循环 + IR 规范化检测不变）。
- 验收：engine 跑完不动点；循环时停止并 warn。
- 测试来源：手写。

### O2.S2 — PredicatePushdown
- 产出：`rules/predicate_pushdown.go`（where 穿过 project/extend 推到 source；不能穿过 summarize/join 的谓词保留）。
- 验收：`T | extend x = f(c) | where x > 0` 谓词推到能算 x 的最近位置；语义等价（I5）。
- 测试来源：T3 P0 + I5 等价性。

### O2.S3 — ColumnPrune
- 产出：`rules/column_prune.go`（扫描列裁剪到下游需要的列集；基于 I3 投影列追踪）。
- 验收：`T | project a | summarize count()` 只读 a 列（实际不需读任何列，但保留 source）。
- 测试来源：T3 P0 + I5。

### O2.S4 — ConstantFold
- 产出：`rules/constant_fold.go`（常量折叠 + 谓词简化：`where 1=1` 删除、`where 1=0` → EmptyResult）。
- 验收：常量表达式折叠为字面量；恒真/恒假谓词处理。
- 测试来源：手写 + I5。

### O2.S5 — 规则组合与回归测试
- 产出：`rules/engine_test.go`（多规则组合跑 T3 P0；语义等价验证）。
- 验收：T3 P0 上规则重写不破坏语义；优化后代价 ≤ 优化前（O5 基准）。
- 测试来源：T3 P0 + I5 + O5。

## 阶段产出物
- `internal/optimizer/rules/`（rule/engine/predicate_pushdown/column_prune/constant_fold）
- 等价性 + 代价对比测试

## 风险与对策
| 风险 | 对策 |
|---|---|
| 规则循环不动点 | engine 最大迭代 + 规范化检测 |
| 谓词下推破坏语义（穿过聚合） | S2 明确不能穿过 summarize 的规则 |
| 列裁剪漏算（extend 引用） | S3 基于 I3 投影追踪，反向引用分析 |
