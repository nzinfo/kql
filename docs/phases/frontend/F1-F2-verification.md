# F1/F2 认知校验报告（对照 kqlparser）

> 校验对象：F1-lexer.md / F2-ast-skeleton.md
> 参考：`/home/nzinfo/src.erp-ext/kql/kqlparser`（cloudygreybeard，Apache 2.0）

## 1. 校验结论

**认知基本成立**，F1/F2 设计与 kqlparser 高度吻合。kqlparser 是非常理想的直接范本。**3 处偏差需要补强**（已在第 3 节列明）。

## 2. 逐条校验表

| 校验点 | 我们认知 | kqlparser 实际做法（位置） | 偏差/补充 |
|---|---|---|---|
| F1.S1 token 枚举 | 标识符/关键字/字面量/操作符/分隔符 | `token/token.go:9-220` 完整枚举；含 ILLEGAL/EOF/COMMENT 特殊 token | **补**：我们漏了 graph 边操作符（DASHDASH/DASHGT/LTDASH/DASHLBRACK/LTDASHLBRACK/RBRACKDASH/RBRACKDASHGT，token.go:53-60）—— MVP 不实现 graph，但要预留 token |
| F1.S1 字面量类型 | int/long/real/string/datetime/timespan/dynamic/guid/bool | token.go:17-26 含 IDENT/INT/REAL/STRING/DATETIME/TIMESPAN/GUID/BOOL/DYNAMIC/TYPE | **一致**；额外有 TYPE（typeof(string)），建议加 |
| F1.S2 关键字表 | 大小写不敏感 | `token/token.go:70+` 列了 ~80 个关键字；用 LookupIdent 风格 | **补**：关键字数量比我们设想多（含 GRAPH*/PARSE*/PROJECT* 多变体），F1.S2 要直接搬这个清单 |
| F1.S3 主循环 | 手写 switch | `lexer/lexer.go` 手写 + `next()` rune 推进 + `peek()`；提供 `Reset(offset)` 用于 parser lookahead（lexer.go:93-103） | **借鉴**：Reset 接口设计好，F1.S1 应加；与 F3.S1 savedState 配合 |
| F1.S3 多字符操作符 | 优先匹配 | lexer 手写 switch，未集中优先级表 | 一致 |
| F1.S4 字符串 | verbatim `@"..."` | `lexer.go:372-418` scanString + scanVerbatimString 分两函数；verbatim 用双引号转义（`""` 表示一个 `"`） | **一致**；F1.S4 双引号转义规则要对齐 |
| F1.S4 timespan 后缀 | `1h`/`1d` | `lexer.go:319-345` + `isTimespanSuffix`（d/h/m/s/t 起始），允许 `1.5d` `1min` | **补**：F1.S4 要列出所有后缀（day/hour/minute/second/milli/micro/tick）及其缩写 |
| F1.S5 错误恢复 | 不 panic，进 ErrorList | `lexer.go:38-49` ErrorList + Err()；scanString 出错进 errors 不中断 | **一致**；ErrorList 模式可直接借鉴 |
| F2.S1 Node 接口 | Pos/End/String | `ast/node.go:8-13` Node{Pos, End, node()}（node() 是包私有标记方法） | **借鉴**：node() 包私有标记方法（不是 String），强制封闭实现——比我们设计的"interface + 公开 Stringer"更严，建议采纳 |
| F2.S1 Expr/Stmt 分接口 | 是 | node.go:15-25 Expr/Stmt/Operator 三接口，都嵌套 Node | **一致**；我们 F2.S4 TabularOp 与 kqlparser 的 Operator 接口一致 |
| F2.S2 字面量节点 | Lit | `BasicLit`（node.go:35） + DynamicLit（46） | **补**：dynamic 字面量单独节点（不是 BasicLit），F2.S2 要分开 |
| F2.S3 表达式节点 | BinOp/UnaryOp/FuncCall/Member/Index/Cast/Cond | `ast/node.go:34-50`：Ident/BadExpr/BasicLit/ParenExpr/UnaryExpr/BinaryExpr/CallExpr/IndexExpr/SelectorExpr/ListExpr/BetweenExpr/DynamicLit/StarExpr/NamedExpr/PipeExpr/ToScalarExpr/ToTableExpr/MaterializeExpr | **补**：kqlparser 节点更细——BetweenExpr（between）/PipeExpr（管道作参数）/StarExpr（`*`）/NamedExpr（命名参数）/ToScalarExpr/ToTableExpr（标量↔表转换）/MaterializeExpr。F2.S3 应吸纳这些 |
| F2.S4 TabularOp | Source/Where/Project/Extend/Take/Sort/Summarize/Join/Union/Distinct/Let | `ast/node.go:55-130` 共 ~55 个 Operator 实现，远超我们 F2.S4 列的 11 个 | **补**：F2.S4 至少要列出所有 Operator 类型名（哪怕 MVP 只实现 P0），便于 parser 预留扩展点 |
| F2.S5 顶层 | Statement/Pipeline/LetStmt | node.go:52-54 LetStmt/ExprStmt/QueryStmt；stmt.go 总文件 | **一致**；QueryStmt 是顶层 |
| F2.S6 Visitor | Visitor+Walk+BaseVisitor | `ast/visitor.go`（199 行）实现了 Visitor + Walk | **一致**；直接借鉴结构 |

## 3. 需要修订 F1/F2 文档的具体点

1. **F1.S1**：在 token 类型清单中补充 graph 边操作符（DASHDASH 等 7 个，token.go:53-60）和 TYPE token，标注"MVP 不实现但预留"。
2. **F1.S2**：直接搬 `token/token.go:70-220` 的关键字清单（~80 个），不要自己重新整理。
3. **F1.S3**：补 `Reset(offset)` 接口（lexer.go:93），用于 parser lookahead——这是 F3.S1 savedState 的基础设施。
4. **F1.S4**：timespan 后缀枚举完整化（day/hour/minute/second/millisecond/microsecond/tick 及缩写），与 lexer.go:363-369 对齐。
5. **F2.S1**：Node 接口用 `node()` 包私有标记方法（不是公开 Stringer），强制 AST 节点封闭实现——比设计文档更严，建议采纳。
6. **F2.S2**：dynamic 字面量单独节点 `DynamicLit`，不并入 BasicLit。
7. **F2.S3**：表达式节点补充 BetweenExpr/PipeExpr/StarExpr/NamedExpr/ToScalarExpr/ToTableExpr/MaterializeExpr。
8. **F2.S4**：列出 kqlparser 的全部 ~55 个 Operator 类型名（含 graph/scan/parse/mv-expand 等非 P0），标注 MVP 实现范围。

## 4. 其他可借鉴的工程做法

- **token 分组标记**：token.go 用 `literalBeg/literalEnd`、`operatorBeg/operatorEnd`、`keywordBeg/keywordEnd` 分组常量，便于 `IsLiteral()`/`IsOperator()` 范围判断（go/scanner 风格）。F1.S1 应采用。
- **Lexer.File() 暴露**：lexer.go:82 暴露 `*token.File` 供 parser/binder 共享位置信息。F1.S1 的 Position 设计应包含 File 抽象。
- **File/Position/Pos 三层**：`token/position.go`（151 行）有 Position（行:列人类可读）+ Pos（文件内偏移紧凑）+ File（行表）三层抽象——go/scanner 同款。F1.S1 应采用此三层而非单一 Pos。
