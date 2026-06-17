# O3 — DecisionPolicy 接口 + 保守策略

> 范围：`internal/optimizer/decision/`
> 依赖：O1（Cost）、O2（IR 规则后 IR）
> 阶段目标：定义可替换决策策略，默认 Conservative；Explain 输出决策理由

## 顺序化子目标

### O3.S1 — AltPlan 接口
- 产出：`decision/altplan.go`（AltPlan 接口：Cost(stats, CostModel) Cost + Emit(Dialect) PhysicalStep + Describe() string）。
- 验收：能表达多种等价物理方案（如 HashJoin/NestedLoop/IndexedLookup）。
- 测试来源：手写 mock altplan。

### O3.S2 — PhysicalPlanner（枚举 AltPlan）
- 产出：`decision/planner.go`（遍历 IR，为每个节点生成 AltPlan 候选集；按节点类型分发）。
- 验收：JOIN 节点生成 ≥2 个候选；Filter/Project 通常单候选。
- 测试来源：手写。

### O3.S3 — DecisionPolicy 接口
- 产出：`decision/policy.go`（DecisionPolicy 接口：Choose(alts []AltPlan, stats StatsReader) AltPlan）。
- 验收：接口稳定；策略实现可热替换。
- 测试来源：手写。

### O3.S4 — ConservativePolicy 默认实现
- 产出：`decision/conservative.go`（关键统计缺失或置信度 < 0.5 时不生成激进 AltPlan，回退"最像 pg 会做的"）。
- 验收：统计缺失时不做激进 join 重排/两阶段聚合；Explain 显示 reason。
- 测试来源：手写 + O5。

### O3.S5 — AggressivePolicy 与 ConfidenceGatedPolicy
- 产出：`decision/aggressive.go`（总选最低估算代价）、`decision/gated.go`（低置信度回退保守）。
- 验收：三策略在同一 IR 上选不同 AltPlan；O4 join 案例验证。
- 测试来源：手写 + O4。

### O3.S6 — Explain 输出
- 产出：`decision/explain.go`（每个决策附 reason：哪条统计、什么选择率、为什么选这条；输出结构化 ExplainOutput）。
- 验收：Explain 可读；含 IR 文本（I4）+ SQL + 代价对比 + 决策日志。
- 测试来源：手写快照。

## 阶段产出物
- `internal/optimizer/decision/`（altplan/planner/policy/conservative/aggressive/gated/explain）
- 单元测试 + Explain 快照

## 风险与对策
| 风险 | 对策 |
|---|---|
| AltPlan 枚举爆炸 | planner 限制每节点候选数；剪枝高代价 |
| 策略切换行为难追踪 | Explain 记录 policy 名 + 决策日志 |
| 保守策略过于保守 | S4 仅在代价敏感决策（join/两阶段）才回退；规则重写不受影响 |
