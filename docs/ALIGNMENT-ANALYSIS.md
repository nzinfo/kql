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
- **B2** pg P0 后端（pgx/CTE emit/$left/$right）
- **B5** sqlite 后端（modernc 驱动/CTE emit）
- **B7** 快照测试（48 golden + 等价性）
- **T1/T3/T5** 语料（90 真实查询 100% / corpus regression / fuzz stress）

### ⚠️ 部分实现
| Phase | 缺失子目标 | 影响 |
|---|---|---|
| **F5** binder | S1 无 scope stack/symbol 类型；S4 Col.T 仅从 schema 读取不推导；S6 不校验函数签名；S7 无 StrictMode | 中：类型推断仅覆盖源列；函数调用不做 arity 校验 |
| **F7** builtin | S1 Spec 缺 Params/ReturnType/Kind；S2 仅 ~103 函数（g4 有 380+）；S3 无 docs/capabilities.md | 中：高频函数覆盖好；低频函数按需补 |
| **I2** translate | S4 FuncCall.Caps 用 DefaultCaps 不查 F7 表；S5 无 KQL010+ 码；S6 无 .ir golden | 低：Caps 在 emit 层按 catalog 查；golden 是 SQL 级 |
| **I3** 投影列追踪 | 全部缺失（capabilities.md / projection.go / CTE 边界重绑定） | 中：CTE 边界决策靠经验而非系统追踪 |
| **I4** IR pretty-print | S1 在 cmd/kql 而非 ir/print.go；S2 无 YAML dump | 低：explain 能用；库级 API 缺 |
| **I5** 等价性框架 | 全部缺失（canonical.go / equiv.go / IR golden） | 中：依赖 SQL golden + e2e 等价性间接覆盖 |
| **O3** 决策 | S1/S2 无 AltPlan/PhysicalPlanner（单决策点 PredicateOrder） | 高：join 物理方案选择未实现 |
| **O5** 基准 | S1 无 IR+cost dump；S4 explain 无前后代价数字 | 低：有 optimizer vs parse 时间基准 |
| **B1** 后端框架 | S2 无 sqlbuild 包；S3 无 PhysicalStep（直连 IR）；S5 无 types.go | 中：架构简化（DESIGN 对齐但非完整 PhysicalPlan） |
| **B3** pg CTE/join | S4 无 MATERIALIZED hint（依赖 O3 PhysicalPlanner） | 中 |
| **B4** duckdb | S2 无列式优化；S3 无 Arrow 零拷贝；S5 无 aggregates.go | 低：复用 pg emit 可用；原生优化缺失 |
| **S1** API 骨架 | S3 无 Engine 类型；S6 无 datasource 文件加载 | 低：Exec(ctx,dsn,query) 可用 |
| **S2** exec | S2/S3 无 schema.go；无 Arrow Record（[][]interface{} 代替） | 低：DESIGN 说返回 Arrow，当前用 slice |
| **S5** CLI | S1 无 arrow/parquet 输出；S6 无 cmd/kql/README.md | 低 |
| **T4** golden | S2/S3 无 AST/IR golden（仅 SQL golden） | 低 |

### ❌ 完全缺失
| Phase | 内容 | 优先级 |
|---|---|---|
| **O4** Join AltPlan | HashJoin/NestedLoop/IndexedLookup/MergeJoin 物理方案枚举 | 高 |
| **O6** 高级规则 | ViewMatch/两阶段聚合/采样预过滤 | 中 |
| **B6** UDF | pg plpgsql / duckdb UDF / UDF 生命周期 | 低（UDF-STRATEGY.md 分析了只有 3 类需要） |
| **S6** Mock + CI | 无 mock backend / 无 testutil / 无 .github/workflows/ | 中（CI 是质量保证基础） |
| **T2** 语料提取 | 无分类目录 / 无 sanitization-rules.yaml / 无 NOTICE | 低 |
| 文档 | capabilities.md / backend-differences.md / stats-pg-mapping.md / perf-baseline.md | 低 |

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
| `declare query_parameters` | declareQueryParametersStatement | **中**（参数化查询） | 低（加 token + dispatch） |
| `\| search Kind` | searchOperator (piped) | 中 | 中（特殊搜索语法） |
| `mv-apply` | mvapplyOperator | 低中 | 低（类似 mv-expand） |
| `:` 字符串操作符 | stringBinaryOperation | 低 | 低（加 isStringOpToken） |
| Unicode 空白（BOM/NBSP） | WHITESPACE | 低中（粘贴/导入） | 低（加 skipWhitespace 字符） |
| 通配符表名 `App*` | wildcardedName | 低中（union 常用） | 中 |
| graph-* 算子（5个） | graphMatch/make-graph/... | 低（上升中） | 中（图模式语法） |
| `restrict access`/`alias database`/`declare pattern` | 3 个语句 | 极低 | 低 |
| `decimal(...)` 字面量 | DECIMALLITERAL | 极低 | 低（NOTES §3 已记） |
| `.[]` legacy 路径元素 | legacyFunctionCallOrPath | 极低 | 低 |

### 最高价值修复优先级（Grammar 方向）
1. **Unicode 空白** — BOM 文件导入健壮性（低难度，防粘贴/导入断）
2. **`declare query_parameters`** — 参数化查询（低难度，生产常用）
3. **`mv-apply`** — 类似 mv-expand，已有基础设施（低难度）
4. **`:` 字符串操作符** — 加 isStringOpToken（低难度）
5. **通配符表名 `App*`** — union 常用（中难度）

## 三、建议的下一步优先级（综合 Phase + Grammar）

| 优先级 | 任务 | 理由 |
|---|---|---|
| 🔴 高 | O4 Join AltPlan | 优化器最大缺口；解锁 B3.S4 MATERIALIZED + S5.S5 policy demo |
| 🟡 中 | CI 工作流（.github/workflows/） | 质量保证基础 |
| 🟡 中 | F5 类型推断 + 函数签名校验 | KQL002/003/004 诊断码定义但从未触发 |
| 🟡 中 | Arrow Record 替代 [][]interface{} | DESIGN §0 承诺；大结果集性能 |
| 🟡 中 | Unicode 空白 + `declare query_parameters` | Grammar 残留高频缺口；低难度 |
| 🟢 低 | F7 完整函数表（380+）/ 文档 | 按需补 |
| 🟢 低 | T2 语料分类/NOTICE | 合规性 |
| 🟢 低 | I4/I5 IR pretty-print/等价性 | 有 SQL golden 间接覆盖 |

### 已完成（2026-06-17）
- ✅ **`set`/`as`/`invoke` 算子 dispatch** — 三个生产 KQL 高频构造补齐（commit 7d60561），
  附带修复 `kind` 参数名误吞 + binder Star 丢列两个深层 bug。语料 89→90（100%）。
