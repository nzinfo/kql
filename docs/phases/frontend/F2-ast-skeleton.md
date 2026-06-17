# F2 — AST 节点骨架

> 范围：`internal/frontend/ast/`
> 依赖：F1（用 token 类型与 Position）
> 阶段目标：定义 AST 节点接口与基础类型，供 parser 与 binder 共用

## 顺序化子目标

### F2.S1 — 顶层 Node 接口
- 产出：`ast/node.go`（Node 接口 + Expr/Stmt/Operator 子接口；基础节点 `Bad`、`Comment`）。
- **关键设计（校验补，对齐 `kqlparser/ast/node.go:8-31`）**：
  - Node 接口：`Pos() token.Pos` / `End() token.Pos` / **`node()`（包私有标记方法，不是公开 Stringer）**
  - 用包私有标记方法 `node()`/`expr()`/`stmt()`/`operator()` **强制实现封闭在本包**——外部包无法新增 AST 节点，避免类型蔓延。这比设计文档原版的"interface + 公开 Stringer"更严，**采纳 kqlparser 模式**。
  - Expr/Stmt/Operator 都嵌套 Node。
- 验收：所有后续节点实现 Node；接口编译期约束；外部包无法构造自定义节点。
- 测试来源：手写编译期断言。

### F2.S2 — 字面量与引用节点
- 产出：`ast/literal.go`（BasicLit：int/long/real/string/datetime/timespan/bool/guid）、**`ast/dynamic.go`（DynamicLit，独立节点，不并入 BasicLit——校验补，对齐 `kqlparser/ast/node.go:46`）**、`ast/ref.go`（Ident、Column、Path `a.b.c`、Star `*`）。
- 验收：节点携带原始 token、字面值已解析为 Go 值、位置正确；dynamic 字面量保留嵌套结构（array/dict/scalar）。
- 测试来源：手写用例 + kqlparser BasicLit/DynamicLit 对照。

### F2.S3 — 表达式节点
- 产出：`ast/expr.go`。
- **节点清单（校验补全，对齐 `kqlparser/ast/node.go:34-50`）**：
  - 基础：Ident / BadExpr / BasicLit / ParenExpr / UnaryExpr / BinaryExpr / CallExpr
  - 复合：IndexExpr（`a[0]`）/ SelectorExpr（`a.b`）/ ListExpr / **BetweenExpr（`x between (a .. b)`，校验补）** / DynamicLit / StarExpr（`*`）/ **NamedExpr（命名参数 `kind=inner`，校验补）**
  - 特殊：**PipeExpr（管道作参数，校验补）** / **ToScalarExpr / ToTableExpr（标量↔表转换，校验补）** / **MaterializeExpr（materialize() 函数特殊节点，校验补）**
- 覆盖：BinaryExpr 存 Op 字符串（`+`、`has`、`contains`、`startswith` 等）。
- 验收：表达式节点携带左右子节点引用与位置范围；上述所有节点类型有构造器。
- 测试来源：手写 + Kql.g4 expression 规则 + kqlparser ast/expr.go 对照。

### F2.S4 — Tabular 算子节点骨架（仅字段定义）
- 产出：`ast/operator.go`（Operator 接口 + 各算子结构）。先只字段，parser 在 F4 填充。
- **类型清单（校验补，列出 kqlparser `ast/node.go:167-219` 全部 ~55 个 Operator 类型名，便于 parser 预留扩展点；MVP 只实现 P0 标 ★）**：
  - **P0 算子（MVP 必需）★**：WhereOp / ProjectOp / ExtendOp / TakeOp / SortOp / SummarizeOp / JoinOp / UnionOp / DistinctOp / TopOp / CountOp
  - **P1 算子**：LetStmt（statement 但与算子相关）/ AsOp / SerializeOp / PrintStmt / RangeStmt / DatatableStmt
  - **project 多变体**：ProjectAwayOp / ProjectKeepOp / ProjectRenameOp / ProjectReorderOp / ProjectSmartOp / TopNestedOp / TopHittersOp
  - **parse 系列**：ParseOp / ParseWhereOp / ParseKvOp
  - **mv 系列**：MvExpandOp / MvApplyOp
  - **sample / scan / evaluate**：SampleOp / SampleDistinctOp / ScanOp（含 ScanStep/ScanAssign）/ EvaluateOp / ReduceOp / InvokeOp
  - **make-series / search / find / facet / fork / partition**：MakeSeriesOp / SearchOp / FindOp（含 FindColumn）/ FacetOp / ForkOp / PartitionByOp
  - **graph 系列（MVP 不实现）**：MakeGraphOp（含 MakeGraphWith）/ GraphMatchOp（含 GraphMatchPattern/GraphPatternNode/GraphPatternEdge/EdgeRange/WhereClause/ProjectClause）/ GraphShortestPathsOp / GraphMarkComponentsOp / GraphToTableOp（含 GraphToTableOutput）/ GraphWhereNodesOp / GraphWhereEdgesOp
  - **特殊**：LookupOp / RenderOp / ConsumeOp / GetSchemaOp / AssertSchemaOp / ExecuteAndCacheOp / MacroExpandOp / ExternalDataOp / GenericOp（兜底未知算子）
- 覆盖：每个算子带输入列集合占位（binder 在 F5 填）。
- 验收：节点结构能表达 DESIGN.md 第 10 节 P0 算子的所有参数；类型清单覆盖 kqlparser 全部 Operator（即便 MVP 不实现也有空结构预留）。
- 测试来源：手写结构构造测试 + kqlparser Operator 清单对照。

### F2.S5 — 顶层 Statement / Pipeline 节点
- 产出：`ast/stmt.go`（Statement 接口 + Pipeline{Ops []TabularOp} + LetStmt{Name, Expr} + TabularExpression{Source, Ops}）。
- 验收：能表达完整查询（含 `let` 前缀）的顶层结构。
- 测试来源：手写用例。

### F2.S6 — Visitor 接口
- 产出：`ast/visitor.go`（Visitor 接口 + Walk 函数 + BaseVisitor），便于后续 binder/diagnostic 遍历。
- 验收：遍历测试覆盖所有节点类型。
- 测试来源：手写。

## 阶段产出物
- `internal/frontend/ast/`（node/literal/ref/expr/operator/stmt/visitor）
- 节点构造单元测试

## 风险与对策
| 风险 | 对策 |
|---|---|
| 接口过宽导致空方法满天飞 | BaseVisitor 提供空默认实现 |
| 节点字段后续频繁变更 | 用结构体指针 + options 构造；避免过早抽象 |
| Tabular vs Scalar 节点混淆 | TabularOp 与 Expr 分接口，编译期分离 |
