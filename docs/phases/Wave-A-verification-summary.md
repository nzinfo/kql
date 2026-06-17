# Wave A 认知校验汇总

> 6 份校验报告的跨线总结：哪些认知成立、哪些需修订、各参考项目的可引用位置清单。
> 校验报告分布：
> - `frontend/F1-F2-verification.md`
> - `frontend/F7-verification.md`
> - `ir/I1-verification.md`
> - `optimizer/O0-verification.md`
> - `test-corpus/T1-verification.md`
> - `shell/S1-verification.md`

## 1. 总体结论

**6 条线的核心认知全部成立**，没有方向性错误。3 类需修订项：

| 严重度 | 数量 | 性质 |
|---|---|---|
| 🟢 微调（补字段/补清单） | ~15 | 文档细化 |
| 🟡 中等修订（结构/范围调整） | 4 | F7 分类、S1 过度设计、O0 corr_vs、T1 抽取工作量 |
| 🔴 重大设计风险 | 1 | O0 corr_vs 跨列相关性 pg 不提供 |

## 2. 重大设计风险（必须处理）

### O0 — corr_vs 跨列相关性 pg 不提供

**问题**：DESIGN.md 6.2 节 YAML 契约里 `user_id: {corr_vs: {col: created_at, rho: 0.82}}`，用于修正独立假设。但 PostgreSQL 只给单列 `correlation`（pg_stats），**不给跨列相关性**。

**对策**（O0-verification.md 第 3 节）：
1. corr_vs 降级为**可选**字段（catalog 契约）
2. manual 优先（DBA 手填）
3. 采集脚本可读 `pg_statistic_ext` dependencies 给粗略"是否相关"提示（不给 rho）
4. 优化器缺 corr_vs 时走"独立假设 + 0.1 默认选择率"（已设计在 O1.S5）

**需修订**：DESIGN.md 6.2 节、O0.S1（指针表可选）、O0.S2（不影响置信度）。

## 3. 中等修订项

### F7 — builtin 分类太粗 + 能力位从零设计

- kqlparser 实测 **386 个函数**（与"380+"吻合），但分 **23 类**（不是我们列的 9 类）
- **kqlparser 完全没有能力位（Caps）概念**——我们的 Caps 是新增工作量，1-2 天集中标注
- 签名质量好（参数名/类型/可选/变长全），可直接抽
- 抽取难度低（声明式 Go，可用 go/ast 或正则）

### S1 — 过度设计

- rust-kql 的 kq CLI **只有 56 行**（clap + 直接调 DataFusion ctx）
- 我们设计了 4 个公开 API + Engine + DI 容器，对 MVP 偏重
- **简化**：API 从 4 个减为 2 个（Exec + Explain），Engine 用简单 struct 不用 DI

### T1 — fuzz_corpus 抽取比预期复杂

- fuzz_corpus 是 **Go 字面量**（`[]struct{name, query}`），1214 行 ~150+ 条
- 抽取需 go/ast 解析（不能用纯正则），工作量 0.5-1 天
- large_corpus（JSON）和 kqlparser testdata（.kql 文本）抽取简单
- 含真实 Sentinel 表名（`DeviceProcessEvents`/`SecurityEvent`），**脱敏必要**

## 4. 微调项汇总

| 文档 | 微调点 |
|---|---|
| F1.S1 | 补 graph 边操作符（DASHDASH 等 7 个）+ TYPE token；采用 go/scanner 三层抽象（File/Position/Pos） |
| F1.S2 | 直接搬 kqlparser `token/token.go:70-220` 的 ~80 个关键字 |
| F1.S3 | 补 `Reset(offset)` 接口（lexer.go:93），用于 parser lookahead |
| F1.S4 | timespan 后缀完整化（day/hour/minute/second/ms/µs/tick） |
| F2.S1 | Node 接口用 `node()` 包私有标记方法（强制封闭），不是公开 Stringer |
| F2.S2 | dynamic 字面量单独节点 `DynamicLit` |
| F2.S3 | 表达式补 BetweenExpr/PipeExpr/StarExpr/NamedExpr/ToScalarExpr/ToTableExpr/MaterializeExpr |
| F2.S4 | 列出 kqlparser 全部 ~55 个 Operator 类型名（含 graph/scan/parse 等） |
| F7.S2 | 分类从 9 类改为对齐 kqlparser 的 23 类 |
| I1.S1 | Source 从"表名"升级为接口/枚举（预留 Datatable/Print/Range） |
| I1.S2 | Literal 用指针/nullable 表达 KQL null；Type 补 Decimal |
| O0.S1 | ColumnStats.corr_vs 改为指针（可选）；hist.kind 改 `equi_freq`（pg 是等频非等宽） |
| 新增 | `docs/stats-pg-mapping.md` 固化 O0 字段映射；O0.S6（可选）pg 采集脚本 |

## 5. 各参考项目的可引用位置清单

### kqlparser（`/home/nzinfo/src.erp-ext/kql/kqlparser`，Apache 2.0，cloudygreybeard）
**最直接的范本**，前端三线（F1/F2/F7）大量复用：

| 引用内容 | 位置 |
|---|---|
| token 枚举（~80 关键字 + 字面量/操作符/分隔符） | `token/token.go:9-220` |
| graph 边操作符（7 个） | `token/token.go:53-60` |
| Position 三层抽象（File/Position/Pos） | `token/position.go`（151 行） |
| Lexer 结构 + next/peek/Reset | `lexer/lexer.go:13-103` |
| scanNumber（含 hex + timespan 后缀） | `lexer/lexer.go:300-360` |
| scanString + scanVerbatimString | `lexer/lexer.go:371-418` |
| ErrorList 错误聚合 | `lexer/lexer.go:38-57` |
| Node/Expr/Stmt/Operator 接口（含包私有 node() 标记） | `ast/node.go:8-31` |
| 全部 AST 节点类型清单（~95 个） | `ast/node.go:34-130` |
| Operator 实现清单（~55 个） | `ast/node.go:167-219` |
| Visitor + Walk | `ast/visitor.go`（199 行） |
| AST pretty-print | `ast/print.go`（224 行） |
| 386 个 builtin 函数声明 | `builtin/functions.go`（988 行，23 类） |
| 函数构造器（NewScalarFunction/NewVariadicFunction/NewAggregateFunction） | `builtin/functions.go:10-17` + `symbol/` 包 |
| Binder（符号/scope/类型/schema流） | `binder/binder.go` + `binder/operator.go` |
| Diagnostic（带 code） | `diagnostic/diagnostic.go` |
| Parser（手写递归下降，1403 行） | `parser/parser.go` |
| Parser 各算子分发（2390 行） | `parser/operator.go` |
| 顶层 API（Parse/ParseAndAnalyze） | `kqlparser.go` |

### rust-kql（`/home/nzinfo/src.erp-ext/kql/rust-kql`，Apache 2.0，irtimmer）
**辅助参考**——验证 AST 结构合理性、CLI thin wrapper 模式：

| 引用内容 | 位置 |
|---|---|
| TabularExpression{source, operators} 结构 | `kqlparser/src/ast.rs:11-15` |
| enum Source（7 变体：Datatable/Externaldata/Find/Print/Range/Reference/Union） | `kqlparser/src/ast.rs:17-26` |
| enum Operator（31 变体） | `kqlparser/src/ast.rs:28-63` |
| enum Expr（基础算子/函数） | `kqlparser/src/ast.rs:65-84` |
| enum Type（9 类型，含 Decimal） | `kqlparser/src/ast.rs:86-97` |
| enum Literal（Option 包裹表 null） | `kqlparser/src/ast.rs:99-110` |
| CLI thin wrapper（clap + 直接调 ctx，56 行） | `kq/src/main.rs` |
| 文件扩展名决定加载方式 | `kq/src/main.rs:35-46` |
| AST→DataFusion plan 翻译 | `datafusion-kql/src/planner.rs` |
| 函数注册（UDF） | `datafusion-kql/src/function/` |

### kql-parser（`/home/nzinfo/src.erp-ext/kql/kql-parser`，MIT，CraftedSignal）
**测试语料来源**（不走它的 ANTLR 路线）：

| 引用内容 | 位置 |
|---|---|
| 真实 Sentinel 查询语料（~150+ 条，Go 字面量） | `fuzz_corpus_test.go`（1214 行） |
| 大规模语料（JSON 外部文件） | `large_corpus_test.go`（343 行） + `testdata/corpus.json` |
| parse tree → 结构化提取模式 | `extractor.go`（149365 字节） |

### Kusto-Query-Language（`/home/nzinfo/src.erp-ext/kql/Kusto-Query-Language`，MIT，Microsoft）
**语法权威**，仅引用 grammar，不抄 C# 代码：

| 引用内容 | 位置 |
|---|---|
| 官方 ANTLR 语法（权威基准） | `grammar/Kql.g4` |
| 词法 token 定义 | `grammar/KqlTokens.g4` |
| 绑定器工程量参考（50+ 文件） | `src/Kusto.Language/Binder/` |

## 6. 对 Wave A 启动的影响

| 阶段 | 影响 | 处理 |
|---|---|---|
| F1 | 微调（补 token/关键字/Reset/后缀） | 直接按校验表落地 |
| F2 | 微调（包私有 node()、补节点类型） | 直接按校验表落地 |
| F7 | 中等（分类改 23 类、能力位新增） | 分类直接搬 kqlparser；能力位先标 P0 ~15 个高频函数 |
| I1 | 微调（Source 枚举、Decimal、null 字面量） | 直接按校验表落地 |
| O0 | **重大修订**（corr_vs 降可选） | 先改 DESIGN.md 6.2 + O0.S1/S2 |
| S1 | 中等（简化 API 和 Engine） | 先改 S1.S1/S3 |
| T1 | 中等（fuzz 抽取需 go/ast、脱敏） | 分三个抽取子工具 |

**建议下一步**：先把 6 份校验报告的"修订建议"回写进对应的 phase 文档（F1/F2/F7/I1/O0/S1/T1），然后再开始 Wave A 实施。这样实施时文档与认知一致。

需要我继续做：① 把校验建议批量回写进 7 个 phase 文档；或 ② 直接开始实施某个具体子目标（如先做 O0 文档修订 + F1.S1 token 落地）？
