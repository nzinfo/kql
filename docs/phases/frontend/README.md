# 前端线阶段拆解

> 范围：`internal/frontend/{token,lexer,parser,ast,binder,diagnostic,builtin}`
> 总目标：手写递归下降解析器，把 KQL 文本翻成带绑定信息的 AST（无 ANTLR 运行时依赖）

## 阶段

### Phase F1 — 词法层
**目标**：把 KQL 文本切成带位置的 token 流。

子目标：
- 定义 token 类型与位置（`token/token.go`、`token/position.go`）。验收：单元测试覆盖标识符/关键字/数值/字符串/datetime/timespan/dynamic/操作符/管道符。
- 手写 tokenizer（`lexer/lexer.go`）。验收：吃下 `StormEvents | where State == "TEXAS" | take 10` 切出 9 个 token；位置正确。
- 错误 token 不 panic，产出 diagnostic。验收：未知字符进入 diagnostic 流。

产出物：token 包、lexer 包、词法单元测试。
依赖：无。

### Phase F2 — AST 节点骨架
**目标**：定义 AST 节点接口与基础类型，供 parser 与 binder 共用。

子目标：
- Node/Expr/Stmt/Operator 接口（`ast/node.go`）。验收：所有节点实现 `Pos()/End()/String()`。
- 字面量与标识符节点（`ast/expr.go`：Lit/Ident/Col/Path）。
- 表达式节点（BinOp/UnaryOp/FuncCall/Member）。
- Tabular 算子节点骨架（`ast/operator.go`：Source/Where/Project/Extend/Take/Sort/Summarize/Join 的节点结构，先只字段）。
- 顶层 Statement/Pipeline 节点（`ast/stmt.go`：TabularExpression、Let、Pipeline）。

产出物：ast 包接口与节点定义。
依赖：F1（用 token 类型）。

### Phase F3 — Parser 核心（表达式）
**目标**：用 Pratt parser + 递归下降处理标量表达式。

子目标：
- 主 parser 骨架（`parser/parser.go`：Parser 结构、回溯/lookahead、错误恢复）。
- 字面量/标识符/路径解析。
- 二元/一元运算符优先级（Pratt）。
- 函数调用与参数列表。
- 字符串操作符（contains/has/startswith/endswith/match(regex)）。
- datetime/timespan/dynamic 字面量。

验收：覆盖 `1+2*3`、`a.b.c`、`f(x, y)`、`State has "TEX"`、`datetime(2020-01-01T00:00:00Z)`。
产出物：parser 表达式路径 + 测试。
依赖：F1、F2。

### Phase F4 — Parser（Tabular 算子 P0）
**目标**：解析管道化的查询语句，覆盖 MVP P0 算子。

子目标：
- TabularExpression 顶层（管道符分割的算子序列）。
- `where <expr>` / `project <cols>` / `extend <assignments>`。
- `take <n>` / `order by <key> [desc]` / `sort`。
- `summarize <aggs> by <keys>`。
- `join kind=inner|left (<subquery>) on <keys>`。
- `let Name = ...` 与管道引用。

验收：能解析 `T | where x > 0 | extend y = x*2 | summarize count() by y | order by y desc | take 10`；T3 语料的 P0 子集不 panic。
产出物：parser tabular 路径 + 集成测试。
依赖：F3。

### Phase F5 — Binder（符号+类型+schema流）
**目标**：在 AST 上做名称解析、类型推断、列来源追踪。

子目标：
- 符号与作用域（`binder/scope.go`：全局 let、表 schema、列引用）。
- 列绑定到物理列 ID（供 IR 与后端用，避免方言差异）。
- 类型推断（标量类型 + tabular 类型）。
- 严格模式开关（未知列/函数时报错 vs 警告）。
- schema 流：每个 Stage 输出的列集合，供后续 Stage 校验。

验收：`T | where x > 0 | extend y = x*2` 中 `y` 类型推断为 `x` 的同类型；未知列在严格模式下报 KQL001。
产出物：binder 包 + 单元测试。
依赖：F4、F7（builtin 函数签名）。

### Phase F6 — Diagnostic 系统
**目标**：结构化错误/警告，带 code 与位置。

子目标：
- Diagnostic 结构（`diagnostic/diagnostic.go`：Code/Severity/Pos/Message/Suggestions）。
- 错误码命名空间（KQL000+，参考 kqlparser/diagnostic）。
- 错误聚合与去重（同一位置多条诊断只出最相关）。

验收：解析失败时返回带 code 的 Diagnostic 列表，可被 CLI 渲染成 `file:line:col: KQL001: ...`。
产出物：diagnostic 包。
依赖：F1（位置）。

### Phase F7 — 内建函数清单
**目标**：维护标量/聚合函数签名清单，供 binder 类型推断与 IR 能力位使用。

子目标：
- 从 `kqlparser/builtin/functions.go` 抽出 380+ 函数清单（`builtin/functions.go`）。
- 标注每个函数：参数/返回类型、是否聚合、是否窗口。
- 能力位预留（CanFoldToSQL / NeedsUDF / NeedsPostProcess）—— 暂留占位，由 IR 线填充。

验收：count/sum/avg/min/max/now/datetime/isnull 等高频函数签名正确；按名称查找 O(1)。
产出物：builtin 包 + 函数表测试。
依赖：无（可并行 F1–F4）。

## 关键风险与对策

| 风险 | 对策 |
|---|---|
| 算子前缀歧义（`join` / `join kind=inner` / `lookup`） | 上下文相关：tabular 位置与 scalar 位置分开 parse 入口 |
| tabular vs scalar 上下文混淆 | 用显式状态标记当前 parse 上下文 |
| `let` 作用域与管道引用顺序 | binder 维护作用域栈，let 先于引用解析 |
| 字符串操作符优先级与 SQL 习惯不同 | 严格按 `Kql.g4`，不照搬 SQL |
| 错误恢复（一处错导致全盘崩） | panic/recover + 同步 token：失败时跳到下一个管道符或分号 |
| 手写 parser 与官方语法漂移 | T5 大语料 fuzz + golden file 对照 |
