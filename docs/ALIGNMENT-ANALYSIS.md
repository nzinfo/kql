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
- **T1/T3/T5** 语料（90 真实查询 100% / corpus regression / fuzz stress）

### ⚠️ 部分实现
| Phase | 缺失子目标 | 影响 |
|---|---|---|
| **F5** binder | S1 无 scope stack/symbol 类型；S7 无 StrictMode | 低：~~S4 类型推断已完成（KQL002 warning）~~；~~S6 函数校验已完成（KQL003/KQL004 warning）~~ |
| **F7** builtin | S1 Spec 缺 Params/ReturnType/Kind；S2 ~134 函数（g4 有 380+）；~~S3 无 docs~~ → capabilities.md/backend-differences.md 已落地 | 低：高频函数覆盖好 |
| **I2** translate | S4 FuncCall.Caps 用 DefaultCaps 不查 F7 表；S5 无 KQL010+ 码；S6 无 .ir golden | 低：Caps 在 emit 层按 catalog 查；golden 是 SQL 级 |
| **I3** 投影列追踪 | 全部缺失（capabilities.md / projection.go / CTE 边界重绑定） | 中：CTE 边界决策靠经验而非系统追踪 |
| **I4** IR pretty-print | ~~S1 在 cmd/kql~~ → `ir.Print/Sprint/DescribeExpr` 已移入库；S2 无 YAML dump | 低 |
| **I5** 等价性 | ~~全部缺失~~ → Canonicalize + Equivalent 已落地 | 低 |
| **O3** 决策 | ~~S1/S2 无 AltPlan/PhysicalPlanner~~ → AltPlan + JoinPlan 已落地（O4）；仅余 O3.S3 谓词排序的 Explain 代价数字缺失 | 低 |
| **O5** 基准 | S1 无 IR+cost dump；S4 explain 无前后代价数字 | 低：有 optimizer vs parse 时间基准 |
| **B1** 后端框架 | S2 无 sqlbuild 包；S3 无 PhysicalStep（直连 IR）；S5 无 types.go | 中：架构简化（DESIGN 对齐但非完整 PhysicalPlan） |
| **B3** pg CTE/join | S4 无 MATERIALIZED hint（依赖 O3 PhysicalPlanner） | 中 |
| **B4** duckdb | S2 无列式优化；S3 无 Arrow 零拷贝；S5 无 aggregates.go | 低：复用 pg emit 可用；原生优化缺失 |
| **S1** API 骨架 | S3 无 Engine 类型；S6 无 datasource 文件加载 | 低：Exec(ctx,dsn,query) 可用 |
| **S2** exec | S2/S3 无 schema.go；~~无 Arrow Record~~ → columnar 包已落地（DESIGN §0 第一步）；后端直发 Record 是后续 | 低 |
| **S5** CLI | S1 无 arrow/parquet 输出；S6 无 cmd/kql/README.md | 低 |
| **T4** golden | S2/S3 无 AST/IR golden（仅 SQL golden） | 低 |

### ❌ 完全缺失
| Phase | 内容 | 优先级 |
|---|---|---|
| **O6** 高级规则 | ViewMatch/两阶段聚合/采样预过滤 | 中 |
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

### ⚠️ 缺失但低频（0/90 语料）
| 缺失 | g4 规则 | 真实频率 | 修复难度 |
|---|---|---|---|
| `\| search Kind` | searchOperator (piped) | 中 | 中（特殊搜索语法） |
| `mv-apply` | mvapplyOperator | 低中 | 低（类似 mv-expand） |
| 通配符表名 `App*` | wildcardedName | 低中（union 常用） | 中 |
| graph-* 算子（5个） | graphMatch/make-graph/... | 低（上升中） | 中（图模式语法） |
| `restrict access`/`alias database`/`declare pattern` | 3 个语句 | 极低 | 低 |
| `decimal(...)` 字面量 | DECIMALLITERAL | 极低 | 低（NOTES §3 已记） |
| `.[]` legacy 路径元素 | legacyFunctionCallOrPath | 极低 | 低 |

> **2026-06-17 补齐**：`declare query_parameters`（declareQueryParametersStatement）+ Unicode 空白
> （BOM/NBSP/全 g4 WHITESPACE 字符集）+ `:` 字符串操作符（stringBinaryOperation）已实现，
> 不再列入缺失。

### 最高价值修复优先级（Grammar 方向）
1. **`mv-apply`** — 类似 mv-expand，已有基础设施（低难度）
2. **通配符表名 `App*`** — union 常用（中难度）
3. **`\| search`** — 特殊搜索语法（中难度）

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
