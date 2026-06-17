# IR 线阶段拆解

> 范围：`internal/ir/`
> 总目标：把绑定后的 AST 翻成方言无关、接近 SQL 关系代数的 IR，供优化器与后端共享

## ⚠️ IR 的定位（重要）

**IR 是内部数据结构，不是运行时输出物。**

- 实际产物（运行时输出）：**SQL**（由后端从 PhysicalPlan 生成，发给 pg/duckdb/sqlite 执行）。
- IR 的可读表示（包括 YAML dump、pretty-print）**仅用于 Explain / 调试 / 测试快照对比**，不会作为查询的最终输出。
- 因此：
  - `pkg/kql` 的公开 API 返回的是 `arrow.Record`（结果）或 `*ExplainOutput`（含 IR 文本、SQL 文本、代价），**不直接返回 IR 对象**。
  - CLI 默认输出是结果集（CSV/Arrow/Parquet）；`kql explain` 才显示 IR（可选 YAML）+ SQL + 代价。
  - IR 的 YAML 序列化器只是调试工具，不进入核心执行路径，可按需启停（构建标签或 `Explain` 选项）。

## 阶段

### Phase I1 — 核心数据结构
**目标**：定义 Pipeline / Stage / Expr 接口与基础类型。

子目标：
- `Pipeline{ Source; Stages []Stage }` 顶层结构（`pipeline.go`）。
- Stage 接口与 P0 实现（`stage.go`：Source/Filter/Project/Extend/Aggregate/Join/Sort/Limit/Union）。
- Expr 接口与实现（`expr.go`：Lit/Col/BinOp/UnaryOp/FuncCall/Agg/Case）。
- 列引用用**物理列 ID**（绑定器产物，不是字符串名）。
- FuncCall 带**能力位** `Caps{SQLExpr, Aggregate, Window, NeedsUDF, NeedsPostProc}`。

验收：能构造一条 `orders | where status=="paid" | take 10` 的 IR；所有节点有 `Pos()`/`String()`。
产出物：ir 核心类型 + 构造器测试。
依赖：F2（AST 节点结构参考）。

### Phase I2 — AST → IR 翻译器（P0）
**目标**：把 binder 输出的 AST 翻成 IR。

子目标：
- 翻译器入口（`translate.go`：Translate(ast) → (*Pipeline, error)）。
- 表达式翻译（AST Expr → IR Expr），保留物理列 ID。
- P0 tabular 算子翻译（where→Filter, project→Project, extend→Extend, summarize→Aggregate, take→Limit, order by→Sort, join→Join）。
- 能力位填充：从 builtin 函数表查 Caps，填到 FuncCall。

验收：F4 解析出的 P0 查询都能翻成 IR；语义等价（字段、列、能力位正确）。
产出物：translate.go + 翻译测试。
依赖：I1、F4、F5（binder 提供列 ID）、F7（builtin Caps）。

### Phase I3 — 能力位与列绑定语义
**目标**：夯实能力位规则与列绑定语义，保证后端能正确分流。

子目标：
- 能力位计算规则文档化：何时标 NeedsUDF（pg 无对应内建）、何时标 NeedsPostProc（sqlite 缺函数）。
- 列绑定校验：列引用必须有物理列 ID，否则报错。
- 投影列追踪：每个 Stage 输出的列集合（供后续 Stage 与列裁剪使用）。

验收：`summarize percentile(x, 90) by k` 在 pg 后端标 `percentile.NeedsUDF=true`；列引用缺失 ID 报错。
产出物：能力位规则注释 + 单元测试。
依赖：I2。

### Phase I4 — IR Pretty-Printer（仅 Explain/调试用）
**目标**：IR 可读输出，**仅用于 Explain 与调试快照**，不作为运行时产物。

子目标：
- Pretty-print（`print.go`：缩进文本形式，主用）。
- YAML dump（`print.go` 内可选函数，仅在 `Explain` 显式请求时序列化；不进入核心执行路径）。
- 同时输出能力位与列绑定信息（debug 模式）。

验收：`kql explain` 能输出 IR 树（文本，可选 YAML）；**普通 `kql <query>` 执行路径不调用任何 IR 序列化**（构建/运行时不依赖 YAML 序列化器即可产出 SQL）。
产出物：print.go（含可选 YAML dump）+ 快照测试。
依赖：I1。
**定位提醒**：本阶段产出物是检视/调试工具，非查询输出。查询输出永远是后端生成的 SQL。

### Phase I5 — IR 等价性测试（快照用途）
**目标**：保证 AST→IR 翻译与重写不破坏语义。**golden 快照是测试/调试工具，非产物。**

子目标：
- IR 规范化（canonical form），用于等价对比。
- Golden file：T3 语料的 P0 子集 → IR 文本对照（文本/YAML 均可，仅测试使用）。
- 反例：明显语义变化的改动应被测试捕获。

验收：F4/I2 修改后 golden file 不漂移；规则重写前后语义等价（用 SQL 执行结果对比验证，而非仅 IR 形状）。
产出物：ir golden test + canonical form。
依赖：I2、T3（语料）、T4（golden 机制）。
**定位提醒**：golden 文件是回归测试资产，不是查询输出。最终产物仍是 SQL。

## 关键设计决策记录

1. **列引用用物理列 ID 而非字符串**：避免 pg/duckdb/sqlite 大小写、引号、保留字差异；列 ID 在 binder 阶段绑定一次，全链路复用。
2. **能力位挂在 FuncCall**：后端只读 Caps 选择翻译路径（SQL 表达式 / UDF / 客户端补），不重新推断。能力位在翻译阶段填好，优化器只读不写。
3. **Stage 用接口而非结构体联合**：便于规则重写时按类型断言访问；新增 Stage 不影响已有规则。
4. **Expr 区分 scalar 与 tabular**：scalar 用于 SELECT/WHERE 项；tabular 是 Stage 本身（如子管道）。混用会编译期拒绝。
5. **Pipeline = Source + Stages**：Source 是表名或子管道（let/子查询），Stages 是顺序变换。对应 SQL 的 `FROM ... <ops>`，便于后端"能合就合"。
6. **不引入 optimizer 私有字段**：IR 只描述"做什么"，不描述"怎么做"。物理方案（AltPlan）放在 optimizer 包，避免污染 IR。
7. **IR 不是产物，SQL 才是产物**：IR 是内部中间表示；查询的运行时输出永远是后端生成的 SQL。IR 的可读表示（pretty/YAML）仅服务 Explain 与测试快照，序列化器不进入核心执行路径——保证"只产出 SQL"的构建/运行时可不依赖 IR 序列化代码。

## 风险与对策

| 风险 | 对策 |
|---|---|
| 物理列 ID 在 view/CTE 边界失效 | view/CTE 引入新列命名空间时重新绑定 |
| 能力位规则复杂导致难维护 | 集中在 builtin 表标注，不在多处推断 |
| IR 与 AST 漂移（改一边漏一边） | I5 golden file + 双向测试 |
| Expr 接口过宽导致类型断言满天飞 | 提供类型化的访问器（AsBinOp/AsFuncCall...） |
| 重写后能力位失效 | 重写规则必须保留 Caps；测试覆盖 |
