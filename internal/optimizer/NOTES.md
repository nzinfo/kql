# Optimizer 实现笔记

> 持久化 optimizer 线（O0–O6）实现决策与坑。新发现随时追加。
> 依赖见 docs/PROGRESS.md；DESIGN 依据见 DESIGN.md §6。

## 1. 已实现（O0 统计 catalog）

### O0.S1–S3 ✅（`internal/optimizer/stats/`）
- **数据结构**（catalog.go）：`Catalog{Version,Source,Schema,Tables,Views,CostModel}`
  / `Table{RowCount,AvgRowBytes,Columns,Indexes}` / `ColumnStats{Card,Nulls,Type,MCV,Hist,CorrVs*}`
  / `IndexDef{Name,Columns,Include,Unique}` / `CorrVs{OtherColumn,Rho}` / `CostModel`。
- **校验固化**（O0-verification.md）：
  - `CorrVs` 是 `*CorrVs`（可选指针）—— pg 不暴露跨列相关 ρ，缺失是常态。
  - `Hist.Kind` 默认 `equi_freq`（对齐 pg histogram_bounds：等频非等宽）；`equi_width`
    保留给未来采样估算。
  - `CostModel.CacheHitRate` 是 `*float64`（可选，pg 无直接对应）。
- **置信度**（confidence.go）：source 给**上限**（pg_analyze 0.9 / sampling 0.7 / manual 0.6），
  缺 core 字段（card/nulls/mcv/hist）把分数往下拉：`ceiling × (present/4)`。
  **CorrVs 缺失不扣分**（增强字段，非基础字段）。例：manual 列只有 card+nulls → 0.6×0.5=0.3 < 0.5 ✓。
- **YAML 加载器**（loader.go）：缺可选字段（CorrVs/MCV/Hist）不报错；**未知字段告警不报错**
  （O0.S3 修订：pg 采集脚本会写 pg_oid/stats_target 等额外字段，报错会阻碍程序化生成）。
  `Load(path)`、`LoadFor(backend, baseDir, schema)`（O0.S4 多后端目录隔离 `stats/<backend>/<schema>.yaml`）。
- **示例**：`testdata/stormevents.yaml`（DESIGN.md §6.2 风格，pg_analyze 源，含 MCV/Hist/index）。
- **测试**：10 个（加载/字段往返/缺字段/未知字段告警/置信度 4 场景/CorrVs 不扣分/列级置信）全绿。

## 2. 待办（后续 O 子目标）

- **O0.S5 StatsReader 接口**：只读访问层，给 optimizer rules/cost 用（mock 可注入）。
- **O0.S6 pg 采集脚本**：连 pg 从 pg_stats 等生成 YAML（字段映射见 docs/stats-pg-mapping.md 待建）。
- **O1 rewrite rules**：谓词下推、投影裁剪、常量折叠。**首个 rule 候选**：把 `| where P` 推过
  `| extend`/`| project`（当 P 只引用 extend/project 之前就存在的列时）—— 可直接在 IR 上做，
  stats 不必需，但有了 stats 能判断"下推后选择性更高→更值得做"。
- **O2–O6**：代价模型、join 顺序、CTE 断点决策、policy 切换。依赖 O1 + O3 PhysicalPlan。

## 3. 关键坑（防再犯）

### 3.1 置信度公式：上限 × 完整度，不是 floor + (1-floor)×完整度 ⚠️
第一版写成了 `floor + (1-floor)*completeness`，结果是 pg_analyze 全字段 = 1.0（超上限）、
manual 缺字段 = 0.8（不够低）。spec 要的是 **source 给上限，缺字段往下拉**：
`ceiling × (present_core_fields / 4)`。pg_analyze 全字段 = 0.9×1 = 0.9 ✓，
manual 缺 mcv+hist = 0.6×0.5 = 0.3 < 0.5 ✓。
教训：先看 spec 的边界例子（"X+全字段 ≈ 0.9"、"Y+缺字段 < 0.5"），反推公式，别凭直觉写。

### 3.2 CorrVs 是增强字段不是基础字段 ⚠️
校验补丁：pg 不提供 ρ，**任何 source 缺 CorrVs 都不影响置信度**。第一反应可能想"缺字段就扣分"，
但 CorrVs 缺失是常态，扣分会把所有 pg catalog 都拉低——错。只有 card/nulls/mcv/hist 缺失才扣。

## 4. O2 规则重写引擎 + PredicatePushdown

**结构**：`internal/optimizer/rules/`
- **rule.go**：`RewriteRule` 接口（`Name()` + `Apply(pipe, StatsReader) → (pipe, changed)`）+
  `Engine`（按序跑规则到不动点，maxIter 上限防震荡，默认 16）。`StatsReader` 接口
  （`Selectivity(table,col)`）让规则可选读 stats；`CatalogStatsReader(*stats.Catalog)` 适配器
  把 O0 catalog 接进来（粗估 `1/cardinality`）。nil reader → 规则 stats-blind 但仍安全。
- **predicate_pushdown.go**（O2.S2）：`where` 谓词下推。规则：filter 向左穿过 Extend/Project
  到 source，**当且仅当**谓词不引用该 stage 引入的列。Aggregate/Join/Union/Distinct/Limit/Sort
  是**不可穿透屏障**（语义会变）。

**安全性**（O2 风险表逐条落实）：
- 穿 Extend：谓词不引用 extend 新增列 → 安全（Extend 保留所有输入列）。
- 穿 Project：仅当 Project 全是裸列透传（无计算/重命名）且谓词只引用这些名 → 安全。
  Project 重命名/计算列 → 阻挡（谓词引用的名在 Project 前不存在）。
- 聚合屏障：`summarize` 后的 `where total > 0` 不能推过 summarize（total 是聚合产物）。

**接线**：`pkg/kql.ExecOn` 在 bind 后、emit 前跑 `defaultEngine`（目前只装 PredicatePushdown）。
优化**永不失败查询**——规则 bug 只会改 IR 形状，emit 仍出合法 SQL。全量 e2e（sqlite+pg）
验证语义等价（结果不变）。

**实测**：`T | extend x = id*2 | where id > 5 | take 1` 从 `[Extend,Filter,Limit]`
重排为 `[Filter,Extend,Limit]`——filter 先跑，少做无用 extend 计算。

**下一轮**（O2.S3/S4）：ColumnPrune（投影裁剪）、ConstantFold（`where 1=1` 删除、
`where 1=0` → EmptyResult）。
