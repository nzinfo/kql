# Phase × Grammar 对齐差距分析（2026-06-18，更新 2026-06-17）

> 对照 docs/phases/ 子目标 + .source-projects/Kusto-Query-Language/grammar/ 金标准，
> 系统性盘点已实现/部分实现/缺失项。

## 一、Phase 文档对齐总览

### ✅ 完整实现（所有子目标达成）
- **F1** 词法层（token/position/lexer/reader benchmark）
- **F2** AST 骨架（全部节点 + Visitor）
- **F3** 表达式 parser（优先级阶梯/调用/后缀）
- **F4** tabular parser（全部 P0 算子 + P1/P2 passthroughs）
- **F6** diagnostic（Diagnostic/Code/List/render）
- **I1** IR 核心（Pipeline/Stage/Expr/Type/ColID/Caps/Visitor）
- **I2** 翻译器（全部算子 + datatable 物化）
- **O0** stats catalog（数据结构/置信度/YAML/采集脚本）
- **O1** 选择率 + 代价模型（标量/join/corr/Cost/weights/降级）
- **O2** 规则引擎 + 三条核心规则（pushdown/constfold/columnprune）
- **O3** 决策策略（三策略 + PredicateOrder + Explain）
- **O4** Join AltPlan（cost-based join-method selection: Hash/NestLoop/Merge hint + IndexLookup cost + pg_hint_plan emit + graceful degrade）
- **O5** benchmark（optimizer ~3.9µs < parse ~4.7µs）
- **B2** pg P0 后端（pgx/CTE emit/$left/$right）
- **B5** sqlite 后端（modernc 驱动/CTE emit）
- **B7** 快照测试（48 golden + 等价性）
- **S6** CI 工作流（.github/workflows/ci.yml: 3-OS test matrix + pg-e2e job + O4 graceful-degrade）
- **T1/T3/T5** 语料（90 真实查询 100% / **kqlparser grammar 207/207 = 100%** / corpus regression / fuzz stress）

### ✅ 完全实现（2026-06-17 更新 — 原 partial 全部补齐）
| Phase | 状态 |
|---|---|
| **F5** binder | ✅ S1 scope.go/symbol.go（作用域栈）+ S4 类型推断（KQL002）+ S6 函数校验（KQL003/KQL004）+ S7 StrictMode |
| **F7** builtin | ✅ S1 Signature 结构（Params/ReturnType/Kind/FuncKind）+ S2 134 函数 + S3 docs（capabilities.md/backend-differences.md） |
| **I2** translate | ✅ S4 FuncCall.Caps 从 F7 Spec 填充 + S5 KQL200/201 翻译诊断码 |
| **I3** 投影列追踪 | ✅ projection.go（ColSet + Projection 列集追踪）|
| **I4** IR pretty-print | ✅ S1 ir.Print/Sprint/DescribeExpr 库级 + S2 SprintYAML |
| **I5** 等价性 | ✅ Canonicalize + Equivalent（canonical form + 语义等价检查）|
| **O3** 决策 | ✅ S1/S2 AltPlan + JoinPlan + S3 ChooseJoinPlan（Conservative/Aggressive/ConfidenceGated）|
| **O5** 基准 | ✅ S1 cost/dump.go（per-stage 代价标注）+ optimizer vs parse 时间基准 |
| **B1** 后端框架 | ✅ S5 types.go（KQL→SQL 类型映射 + DDL 生成）|
| **B3** pg CTE/join | ✅ S4 MATERIALIZED/NOT MATERIALIZED hint（Aggregate/Join → MATERIALIZED；Filter/Sort → NOT MATERIALIZED）|
| **S2** exec | ✅ S2 schema.go（Schema/ColumnDesc 描述）+ columnar Record |
| **S5** CLI | ✅ S6 cmd/kql/README.md（用法/flags/示例）|
| **T4** golden | ✅ S2/S3 IR golden 测试框架（IR 稳定性 + Sprint 确定性）|

### ⚠️ 残留低优先级（架构增强，非功能缺失）
| Phase | 残留 | 影响 |
|---|---|---|
| **F7** builtin | g4 有 380+ 函数，当前 134（低频函数按需补） | 极低 |
| **B1** 后端框架 | S2 sqlbuild 包（当前 emit 内联）；S3 PhysicalStep（当前 ir.Join.Hint 代替） | 低：架构简化（DESIGN 对齐但非完整 PhysicalPlan） |
| **B4** duckdb | S2 列式优化；S3 Arrow 零拷贝；S5 aggregates.go | 低：复用 pg emit 可用；原生优化缺失 |
| **S1** API 骨架 | S3 Engine 类型（当前 ExecOn 函数式 API）；S6 datasource 文件加载 | 低 |
| **S2** exec | S3 后端直发 Arrow Record（当前 columnar 包待接入后端） | 低 |
| **S5** CLI | S1 arrow/parquet 输出格式 | 低 |

### ❌ 完全缺失
| Phase | 内容 | 优先级 |
|---|---|---|
| **O6.S3** 样本预筛 | 极选择性 where + 大 take → rowid 预筛 | 低 |
| **B6** UDF | pg plpgsql / duckdb UDF / UDF 生命周期 | 低（UDF-STRATEGY.md 分析了只有 3 类需要） |
| stats-pg-mapping.md / perf-baseline.md | 低 |

## 二、金标准 Grammar 对齐

### ✅ 已覆盖
- 全部 P0/P1/P2 算子（90/90 真实语料 100%）
- 全部标点 token + 全部字符串/数值/类型字面量
- `in~`/`!in~`/`has_any`/`has_all`/全部字符串操作符（含否定形式）
- `$left`/`$right` join 限定引用
- 函数式 lambda / datatable / union-as-function / materialize 管道参数
- project-away/keep/rename/reorder/smart（深层 bug 已修）
- **`set` 语句 / `| as Name` / `| invoke F()`（2026-06-17 补齐）**
  - `setStatement`：`set querytrace;` / `set x=30000000;` — SetStmt AST，translator 跳过
  - `asOperator`：`| as Result` — AsOp AST，passthrough Project{\*}
  - `invokeOperator`：`| invoke MyPlugin(x)` — InvokeOp AST（Call 捕获），passthrough
  - 附带修复两个深层 bug：
    - `tryParamName` 误吞列名 `kind`（`where kind == "x"` 断）→ 只在紧跟 `=` 时认作参数名
    - binder `Project{Star}` 丢列（render/as/invoke/getschema/externaldata/mv-expand/parse 全受影响）→ Star 展开前传

### ✅ Grammar 全覆盖（2026-06-17）

**所有 g4 OPERATOR 规则（61/61）和 STATEMENT 规则（8/8）均有对应实现。**
原列出的低频缺失项全部已处理（兼容）：

| 原缺失 | g4 规则 | 状态 |
|---|---|---|
| 通配符表名 `App*` | wildcardedName | ✅ 已实现（源位置 + union 参数的 IDENT* 邻接检测） |
| graph-* 算子（5个） | graphMatch/make-graph/... | ✅ AST + parser（pass-through；真实语义需图引擎） |
| `restrict access` | restrictAccessStatement | ✅ 已实现（解析 + 跳过，行级安全指令） |
| `alias database` | aliasDatabaseStatement | ✅ 已实现（解析 + 跳过，数据库别名） |
| `declare pattern` | declarePatternStatement | ✅ 已实现（parseDeclareStmt kind=pattern） |
| `decimal(...)` | DECIMALLITERAL | ✅ 已兼容（decimal(0.1) 解析为函数调用） |
| `.[]` legacy 路径 | legacyFunctionCallOrPath | ✅ 已兼容（arr[0] 数组索引已工作） |
| `\| search` | searchOperator | ✅ 已实现（AST + parser） |
| `mv-apply` | mvapplyOperator | ✅ 已实现（AST + parser + translate） |

> **验证**：kqlparser grammar corpus 207/207 (100%)、kql-parser fuzz corpus 85/85 parse + 85/85
> translate (100%)、Sentinel 语料 90/90 (100%)、g4 OPERATOR 61/61、STATEMENT 8/8。

## 三、建议的下一步优先级（综合 Phase + Grammar）

| 优先级 | 任务 | 理由 |
|---|---|---|
| 🟢 低 | F7 完整函数表（380+）/ 文档 | 按需补 |
| 🟢 低 | T2 语料分类/NOTICE | 合规性 |
| 🟢 低 | I4/I5 IR pretty-print/等价性 | 有 SQL golden 间接覆盖 |
| 🟢 低 | Arrow-native Record（全迁移） | columnar 包已落地；后端直发 Record 是后续工作 |
| 🟢 低 | 多表 join 顺序枚举 | O4 单 join 完成；多表顺序是下一步 |

### 已完成（2026-06-17）
- ✅ **`set`/`as`/`invoke` 算子 dispatch** — 三个生产 KQL 高频构造补齐（commit 7d60561），
  附带修复 `kind` 参数名误吞 + binder Star 丢列两个深层 bug。语料 89→90（100%）。
- ✅ **Unicode 空白 + `declare query_parameters`** — g4 WHITESPACE 全字符集 + 参数化查询解析（commit add964c）。
- ✅ **IndexLookup 结构化 emit** — 两阶段 IN-list 查询（唯一不需 pg_hint_plan 的 join 优化）。小 outer→取键→WHERE IN→客户端 hash join（commit fd00e72）。
- ✅ **F5 类型推断（S4）** — KQL002 TypeMismatch 激活（warning）；算术提升/比较→bool/逻辑/聚合/30+函数返回类型（commit 16b0a4b）。
- ✅ **Arrow Record 基础** — typed columnar Record（Int64/Float64/String/Bool/Mixed + NullMask + round-trip）。DESIGN §0 第一步（commit 5692b5a）。
- ✅ **CI 工作流（S6）** — 3-OS test matrix + pg-e2e job + O4 graceful-degrade（commit 19bd446 + b8e4f77）。
- ✅ **F5.S6 函数校验** — KQL003/KQL004 warning 激活（commit d0cbaae）。
- ✅ **O4 Join AltPlan** — cost-based join-method selection（Hash/NestLoop/Merge hint + IndexLookup cost），
  ir.JoinHint + pg_hint_plan emit + graceful degrade 验证（commits f0117d9–816b1b1 + 4f01a14）。
  生产安全已验证：hint 在无 pg_hint_plan 的 stock postgres:16 上正确执行。
- ✅ **F5.S1/S7 scope + strict mode** — Scope stack（let 嵌套 + shadow）+ StrictMode（warning→error 升级）。
- ✅ **F7.S1 Signature** — Params/ReturnType/Kind/FuncKind 结构 + 60+ 函数签名注册。
- ✅ **I3.S3 projection.go** — ColSet + Projection 列集追踪。
- ✅ **I2.S4/S5** — FuncCall.Caps 从 F7 Spec 填充 + KQL200/201 翻译诊断码。
- ✅ **I4.S2 YAML dump** — SprintYAML 结构化 IR dump。
- ✅ **O5.S1 cost dump** — cost/dump.go per-stage 代价标注。
- ✅ **B1.S5 types.go** — KQL→SQL 类型映射 + DDL 生成。
- ✅ **B3.S4 MATERIALIZED hint** — pg 14+ CTE 物化策略。
- ✅ **S2.S2 schema.go** — Schema/ColumnDesc 描述。
- ✅ **S5.S6 README** — cmd/kql/README.md。
- ✅ **T4.S2/S3 golden** — IR golden 测试框架。
- ✅ **O6.S1 ViewMatch** — 当 catalog 有匹配的预计算 view 时，summarize 改写为读 view（skip base table scan）。
- ✅ **O6.S2 两阶段聚合** — 大表（>100K行）+ 关联聚合（count/sum/min/max）自动 split 为 partial+final。
- ✅ **金标准 Grammar 完全对齐** — 20 个 P2/P3 算子 AST+parser（print/range/find/sample/lookup/
  scan/fork/facet/reduce/top-hitters/partition/macro-expand/execute-and-cache/assert-schema/graph-*）
  + 通用 lparenStartsPipeline 修复（操作符形式子查询检测，side-effect-free lookahead）。
  **kqlparser grammar corpus 207/207 = 100%**（独立维护的语法测试套件全通过）。
