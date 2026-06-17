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

## 5. O2.S3 + O2.S4 — ColumnPrune + ConstantFold

**ConstantFold**（`constant_fold.go`）：
- 常量折叠：`1 + 2`→`3`、`-(1+2)`→`-3`（递归进 BinOp/UnaryOp/FuncCall/Case/List）。
- 谓词简化：`where 1 == 1`/`where true`→**filter 删除**；`where 1 == 0`/`where false`→
  **替换为 Limit 0**（空结果，比物化后过滤便宜）。
- `iff(true,a,b)`→`a`、`iff(false,a,b)`→`b`；`CASE WHEN true`→then 分支。
- 安全：只折叠两侧都是字面量的子表达式，**绝不**丢弃列引用。

**ColumnPrune**（`column_prune.go`）：
- 终末 Project 全是裸列引用 + 中间全是 passthrough（Filter/Sort/Limit/Distinct）→
  在 source 后插入一个 Project 投影所需列（含中间 Filter 引用的额外列），让 DB 少读列。
- **不触发**：Extend/Aggregate/Project 介入（会增列/改列）、终末 Project 有计算列
  （source 算不出）、无终末 Project。
- 保守版：没有 schema 时不能断言"严格子集"，所以只在能明确确定所需列集时触发。
  全列溯源（PhysicalPlan）留后续。

**defaultEngine 规则序**：`ConstantFold → PredicatePushdown → ColumnPrune`。
- 先 Fold：让恒真/恒假谓词短路，减少 Pushdown/Prune 要处理的 stage。
- 再 Pushdown：谓词推到 source 前。
- 最后 Prune：基于最终谓词集裁列。

**测试**：fold 7 个（算术/嵌套/恒真删/恒假→Limit0/保列引用/一元/iff）+ prune 4 个
（终末裸列触发/无 Project 不触发/Extend 阻挡/计算列阻挡）+ 组合（Fold 后 Prune 仍触发）。
全量 sqlite+pg e2e 验证三规则组合语义等价。

## 6. O1 — 选择率估算器 + 代价模型

**结构**：`internal/optimizer/cost/`
- **selectivity.go**（O1.S1）：`Estimator` 把谓词翻成选择率 ∈ [0,1]，严格按
  DESIGN §6.3 公式表：
  - `col = const` ∈ MCV → MCV 频率；不在 MCV → 1/card；无统计 → 0.1（默认）
  - `col < const` 有 hist → 1/(2×bucket_count)；无 hist → 0.33（pg 默认）
  - `col in (...)` → Σ 单值选择率（MCV 优先，余 1/card），上限 1
  - `col between (...)` → 0.25（双端范围粗估）
  - AND → s1×s2（独立假设）；OR → s1+s2−s1×s2
  - 字符串操作符（has/contains）→ 0.1（catalog 不建模子串分布）
- **cost.go**（O1.S4）：`Cost{IO,CPU,Net,Mem}` + `Add/Scale/Total(weights)` +
  `DefaultWeights(backend)`（pg Net 重、duckdb CPU 重、sqlite IO 重无 Net）+
  `WeightsFromCatalog`（O0 cost_model 覆盖默认权重）。
- **降级**（O1.S5）：nil catalog → 全 0.1 不 panic；`EstimateConfidence` 复用
  O0 置信度，<0.5 标 LowConfidence（决策策略据此走保守路径）。

**接线**：`rules.CatalogStatsReader` 升级——从粗估 `1/cardinality` 改用真
`cost.Estimator`（走非 MCV 等值路径）。规则现在拿到 DESIGN §6.3 的精确选择率。

**测试**：17 个（选择率 12：MCV 命中/非 MCV/无统计/IN 全 MCV/IN 混合/范围有 hist/
范围无 hist/AND/OR/nil catalog/nil pred/超 1 钳位；Cost 5：Add/Scale/Total/
默认权重差异/置信度）全绿。

**不在范围**（下一轮）：O1.S2 join 选择率（`1/max(card_a,card_b)`）、
O1.S3 corr 修正（强相关列复合谓词不严重高估）。两者都建在已落地的 Estimator 上。

## 7. O1.S2 + O1.S3 — join 选择率 + corr 修正

**join 选择率**（`selectivity_join.go`，O1.S2）：
- `JoinSelectivity(leftT, rightT, on, leftCard, rightCard)`：DESIGN §6.3 公式
  `t1.a = t2.a → 1/max(card_a, card_b)`。多 key 走独立假设乘积，再经 corr 修正。
- `OutputCardinality`：`leftCard × rightCard × sel`（≥1，<1 上取整）。
- 非 `=` 条件 / 缺 card → `DefaultSelectivity`。无 on 条件（cross join）→ 1.0。

**corr 修正**（`corr.go`，O1.S3）：
- 独立假设 `s1×s2` 在相关列上**高估**（典型 created_at vs id，rho≈1）。
- 公式：`s_corrected = s1*s2 + rho * sqrt(s1*(1-s1) * s2*(1-s2))`。
- 实现：`applyCorrCorrection` 扫 join key 对 (i,j)，若 col_i 的 `corr_vs` 指向 col_j
  （同表）且 rho≠0，把该对的独立乘积换成 rho 修正版。
- **rho 缺失不报错**（pg 不提供 ρ，常态）—— 纯独立假设。
- rho 正（相关）→ 抬高估算；rho 负（反相关）→ 压低；结果 clamp 到 [0,1]。

**测试**：12 个新增（join 6：单 key/不同 card/多 key 独立/无 on/未知 card/输出基数；
corr 5：正 rho 抬高/负 rho 压低/单 key 不受影响/无关列忽略/clamp；+ sqrt helper）。
全量 cost 包 29 个单测全绿。

**关键设计权衡**：
- **pairwise 修正**而非全协方差矩阵——catalog 只记 `corr_vs{OtherColumn, Rho}`
  （单列对单列），多变量需要 N×N 矩阵，超出 O0 catalog 形状。pairwise 是
 保守且够用的近似。
- **left 表视角**：corr_vs 记在左表列上（join 的 driving side）；右表不重复算。
- **sqrt 自实现**（牛顿法 16 迭代）：避免仅为这一个调用点 import math。

## 8. O3 — 决策策略层（可替换 policy）

**结构**：`internal/optimizer/decision/`
- **policy.go**（O3.S3）：`DecisionPolicy` 接口（`Name()` + `OrderPredicates(table, sels) → (order, Decision)`）。
  `Decision{PolicyName, Choice, Reason}` 带 reason 供 Explain。三策略：
  - **Conservative**（默认，O3.S4）：统计弱（全 DefaultSelectivity）→ 保源序，不乱猜；统计真 → 按选择性升序（更选择性的先）。
  - **Aggressive**（O3.S5）：永远按选择性排序，弱统计当均匀。
  - **ConfidenceGated**（O3.S5）：catalog 置信度 ≥0.5 → Aggressive；否则 Conservative。
- **predicate_order.go**：首个决策应用。Filter 的 AND 合取项按 policy 选的顺序重排。
  **语义安全**（AND 可交换，只改性能不改结果）——"代价"部分是选哪个序。
- **explain.go**（O3.S6）：`Explain(pipe, backend, policy, est, applyRules)` 一站式
  `跑规则 → 跑决策 → emit SQL → 收集决策日志`，返回 `ExplainOutput{IR,SQL,Args,Dialect,Decisions,RuleChanges}`。

**关键设计**：
- **policy 可热替换**（DESIGN §6.4 要求）：CLI `--policy` 切换，Explain 显示 policy 名 + reason。
- **决策带 reason**：每个 Decision 记录"哪条统计、什么选择性、为什么这个序"，`kql explain` 可读。
- **PredicateOrder 不进 defaultEngine**：它是**代价决策**（取决于 policy/stats），不是 O2 的"恒安全重写"。
  默认 ExecOn 仍只跑 O2 规则；PredicateOrder 走 Explain/可选 opt-in 路径。

**测试**：9 个（Conservative 有统计重排/弱统计保序、Aggressive 总重排/弱统计 reason、
Gated 高置信走 Aggressive/低置信走 Conservative、三策略同 IR reason 不同、单谓词 no-op）。
全量 14 包绿。

**不在范围**（下一轮）：O3.S1/S2 AltPlan/PhysicalPlanner（真正的多物理方案枚举：HashJoin vs
NestedLoop vs IndexedLookup）—— 当前 PredicateOrder 是单决策点，PhysicalPlanner 是系统化枚举框架。

## 9. O5 — 代价基准 + --stats 接线

**--stats CLI flag**：`kql explain --stats <path> --policy gated` 加载 O0 YAML catalog
→ `cost.Estimator` → 喂给 `PredicateOrder`/gated policy。完整闭环 O0→O1→O3 现在用户可见：
统计加载 → 置信度判断（high，因 catalog 全字段）→ 选择率估算（state MCV 0.08 < damage range 0.33）
→ 谓词重排（state 先）→ emit 优化后 SQL + 决策日志。

实测：`events | where damage > 1000 and state == "TEXAS"` 从 `damage AND state`
重排为 `state AND damage`（更选择性的先短路），explain 显示
`[ConfidenceGated] PredicateOrder: high confidence → ...`。

**pkg/kql.Explain** 新签名加 `statsPath`；`policyFor` 接受 `*stats.Catalog`
（gated 用它判置信度）；PredicateOrder 用真 `cost.Estimator`（非 nilEstimator）。
示例 catalog 在 `pkg/kql/testdata/stats/events.yaml`。

**O5 基准**（`pkg/kql/opt_bench_test.go`）：
- BenchmarkOptimize（O2 三规则到不动点 + O3）：**~3.9µs/op, 101 allocs**
- BenchmarkParseTranslate（baseline）：~4.7µs/op, 100 allocs
- **优化器开销 < parse 的 1µs** —— 健康（优化器远比 parse/emit/exec 便宜）。

**关键 bug 修**：`flagStr` 之前只认单横杠 `-stats`，`--stats`（双横杠）不识别 →
catalog 没加载 → gated 退化为保守。现在 `-`/`--` 都认。


## 10. O4 — Join 物理方案枚举（cost-based join-method selection）

**状态：✅ 核心落地（Hash/NestLoop/Merge hint + IndexLookup cost；IndexLookup emit deferred）。**

**架构（用户批准的设计）**：
- 不建 `backend.PhysicalStep`（B1.S3 大改），而是在 `ir.Join` 上加 `Hint JoinHint` 字段。
- 决策层 `decision.JoinPlan.Apply(pipe)` 枚举候选 + 代价 + policy 选择 → 写 `j.Hint`。
- pg emit 读 `j.Hint` → 发 `/*+ HashJoin(_sN _sN_j) */` 注释（pg_hint_plan 扩展；
  未装则静默忽略——graceful degrade）。sqlite/duckdb 无 join hint → 字段记 Explain 但不改 SQL。
- `JoinHintNone`（零值）= "让后端 planner 决定" = 当前行为（无回归）。

**决策策略 `ChooseJoinPlan`**（`DecisionPolicy` 新方法）：
- **Conservative**：默认选 Default（让 pg 决定），除非某候选比 default 便宜 ≥10× 且 catalog 高置信。永不在边际情况下覆盖 pg。
- **Aggressive**：总选 `argmin Cost.Total(weights)`，平手时偏向具体方法（非 Default）。信任估算器。
- **ConfidenceGated**：catalog 高置信 → Aggressive；否则 Conservative。

**代价组合器**（`decision/join_cost.go`，纯函数）：
- **HashJoin**：build（inner × cpuTupleCost × 3×）+ probe（outer × cpuTupleCost）；Mem=hash 表。3× build 倍数是 NestLoop 在微表上能赢的原因。
- **NestLoop**：O(outer×inner) CPU，但 inner < ~1000 页时 IO 近免费（cache residency）。仅在微表（<~30行）赢。
- **MergeJoin**：单趟扫描，Mem=0；可行性门控于 corr_vs（有序键的代理）。
- **IndexLookup**：outer × random_page_cost；仅当 inner 有 join-key 索引 + inner > 1000 行时枚举。**emit 延迟**（结构化 IN-list 改写是未来 PostProc 路径）。
- **Default**：= HashJoin 代价（pg 对等连接的默认选择），使得 Conservative 的 "≥10× cheaper" 比较有意义。

**已验证策略分歧**（O4.S5 验收）：
- events(1M) × meta(5K indexed)：Aggressive→Hash hint；Conservative→None（defer）。
- 小 outer(50) + 巨大 indexed inner(5M)：Aggressive→IndexLookup。
- corr_vs 存在：Aggressive→Merge。

**接线**：`pkg/kql/kql.go` 的 `Explain` 和 `ExecOptimized` 在 `PredicateOrder.Apply` 之后、有 catalog 时调 `JoinPlan.Apply`。无 catalog → no-op（ExecOn 路径不受影响）。

**延迟（文档化）**：IndexLookup 结构化 emit（IN-list PostProc）、完整 `backend.PhysicalStep`（B1.S3）、多表 join 顺序枚举。
