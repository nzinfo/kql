# 优化器线阶段拆解

> 范围：`internal/optimizer/{stats,rules,cost,decision}`
> 总目标：两段式（规则重写 + 代价选择）+ 基于预定义统计描述 + 可切换决策策略（默认保守）

## 阶段

### Phase O0 — 统计描述数据结构与加载器
**目标**：定义 StatsCatalog Go 结构 + manual YAML 加载器。

子目标：
- 数据结构（`stats/catalog.go`：Catalog/Table/ColumnStats/IndexDef/ViewDef/CostModel）。
- 置信度评分（`Confidence(table, col) float`，按 source 算：manual=0.6, pg_analyze=0.9, sampling=0.7）。
- YAML 加载器（`stats/loader.go`：Load(path) → Catalog），缺字段走降级默认。
- 多后端 catalog 隔离（pg/duckdb/sqlite 各一份），按 backend 名加载。

验收：加载 DESIGN.md 第 6.2 节示例 YAML；缺失 mcv/hist 时不报错；版本/来源字段可读。
产出物：stats 包 + 加载测试 + 示例 YAML。
依赖：无。

### Phase O1 — 选择率估算器 + 代价模型
**目标**：把谓词/JOIN 翻成选择率，把物理方案翻成 Cost。

子目标：
- 选择率估算（`cost/selectivity.go`）：见 DESIGN.md 第 6.3 节公式表（=, <, in, is null, join, AND）。
- corr 修正（`cost/corr.go`：相关列的独立假设修正）。
- Cost 结构（`cost/cost.go`：IO/CPU/Net/Mem 四维 + Total(w)）。
- CostWeights（按后端默认值，可被 catalog 覆盖）。
- 降级路径：无统计时选择率用 0.1，Cost 用粗估。

验收：MCV 命中时选择率 = MCV 频率；无统计走 0.1；单元测试覆盖每个谓词类型。
产出物：cost 包 + 选择率/代价测试。
依赖：O0。

### Phase O2 — 三条核心规则（规则重写阶段）
**目标**：方言无关 IR→IR，覆盖"pg 一定会做或不会更差"的安全优化。

子目标：
- RewriteRule 接口（`rules/rule.go`：Apply(*Pipeline, StatsReader) → (*Pipeline, changed bool)）。
- `rules/predicate_pushdown.go`：where 穿过 project/extend/join 推到 source。
- `rules/column_prune.go`：扫描列裁剪到下游需要的列集。
- `rules/constant_fold.go`：常量折叠 + 谓词简化。
- 规则编排器（`rules/engine.go`：按依赖顺序跑 + 不动点上限防循环）。

验收：每条规则有"语义等价"测试（输入/输出 IR 跑同 SQL 同结果）；T5 大语料上不 panic。
产出物：rules 包（3 条 + engine）+ 测试。
依赖：I1、O0（StatsReader 接口）、I5（等价性测试框架）。

### Phase O3 — DecisionPolicy 接口 + 保守策略
**目标**：定义可替换决策策略，默认 Conservative。

子目标：
- AltPlan 接口（`decision/altplan.go`：Cost + Emit(Dialect)）。
- DecisionPolicy 接口（`decision/policy.go`：Choose(alts, stats) AltPlan）。
- ConservativePolicy 默认实现：关键统计缺失或置信度 < 0.5 时，不生成激进 AltPlan，回退"最像 pg 会做的"。
- Explain 输出（`decision/explain.go`：每个决策附 reason）。

验收：保守策略在统计缺失时不做激进 join 重排/两阶段聚合；Explain 可读。
产出物：decision 包骨架 + 保守策略 + Explain。
依赖：O1（Cost）、O2（IR 规则后 IR）。

### Phase O4 — Join 物理方案枚举
**目标**：为 JOIN 节点生成多个等价 AltPlan 并由策略选优。

子目标：
- AltPlan 实现：HashJoin / NestedLoop / IndexedLookup（split 半边）。
- 各 AltPlan 的 Cost 计算用 O1 选择率 + CostWeights。
- 策略切换演示：Conservative vs Aggressive 在小表 join 时选不同方案。

验收：双表 join 在 catalog 给定时，Conservative 选 NestedLoop 或交给 pg，Aggressive 选 IndexedLookup；Explain 显示选择率与决策理由。
产出物：join altplan + 集成测试。
依赖：O3。

### Phase O5 — 优化前后代价对比基准
**目标**：把 Explain 与代价对比做成可观测工具。

子目标：
- 优化前后 IR 树 dump（pretty + 代价标注）。
- 基准脚本（`cost/bench_test.go`）：固定 catalog 跑一组查询，记录代价对比。
- 负优化检测：当规则导致代价上升时 warn。

验收：在 T3 P0 子集上，优化后代价 ≤ 优化前（或 warn）。
产出物：bench 测试 + Explain CLI 入口。
依赖：O2、O3、O4、T3。

### Phase O6（可选）— ViewMatch / 两阶段聚合 / 样本预筛
**目标**：增量增益，按需启用。

子目标：
- ViewMatch：匹配 `stats.views`，改写 source。
- 两阶段聚合：大表 summarize 先按分片局部聚合再合并。
- 样本预筛：极选择性 where + 大 take，先拉 rowid 集再批量回查。

验收：每个规则有 catalog 驱动的正/反例测试。
产出物：3 条增强规则 + 测试。
依赖：O2、O5。

## 关键决策记录

1. **为什么用预定义统计而非运行时查系统表**：跨后端统一、可版本管理、不增加每次查询往返、可由 DBA 显式声明。pg_stats 采集脚本只作为 YAML 生成工具，不进运行时热路径。
2. **DecisionPolicy 接口如何保证可切换**：所有物理方案以 AltPlan 形式枚举给策略，策略只做"选"不做"算"。换策略不改 AltPlan 代码。
3. **保守/激进/置信度网关三种策略差异**：
   - Conservative：统计缺失/低置信度 → 不生成激进 AltPlan，只做规则重写。
   - Aggressive：始终选最低估算代价，哪怕统计不全。
   - ConfidenceGated：低置信度决策回退保守，高置信度走激进——折中。
4. **Cost 用 IO/CPU/Net/Mem 四维**：IO 决定 scan/index 选择；CPU 决定向量化 vs 行式后端差异；Net 仅 pg 远程拉取；Mem 决定 work_mem 限制下的 hash/sort 选择。Total 加权后端不同（见 DESIGN.md 6.7）。
5. **规则重写与代价选择两段式**：规则重写是"安全优化"（语义保持且 pg 也会做），代价选择是"激进优化"（可能负优化需 Explain 监控）。两段分开便于策略控制。

## 风险与对策

| 风险 | 对策 |
|---|---|
| 统计不准导致负优化 | 保守默认 + Explain reason + 代价对比基准监控 |
| 规则重写循环不动点 | engine 设最大迭代次数 + IR 规范化检测不变 |
| 选择率估算偏差大 | corr 修正 + 置信度评分 + 真实 EXPLAIN ANALYZE 反馈（未来扩展） |
| AltPlan 枚举爆炸 | 限制每个节点的 AltPlan 数量；剪枝高代价方案 |
| 策略切换行为变化难追踪 | Explain 记录 policy 名 + 决策日志 |
