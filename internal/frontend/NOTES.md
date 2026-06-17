# 前端实现笔记（Grammar Alignment Notes）

> 持久化"来之不易的认知"。本文件记录在实现 `internal/frontend/` 时，
> 对照**权威语法** `.source-projects/Kusto-Query-Language/grammar/` 发现的、
> 与模板项目 `.source-projects/kqlparser/` 的偏差。
>
> **原则**：语法金标准永远优先；kqlparser 只是分层范本，不是语法权威。
> 新发现随时追加到本文件，避免上下文摘要后丢失。

## 1. 三个参考来源的优先级

| 来源 | 路径 | 角色 | 信任度 |
|---|---|---|---|
| **金标准** | `.source-projects/Kusto-Query-Language/grammar/Kql.g4` + `KqlTokens.g4` | 语法/词法权威，一切争议以此为准 | **最高** |
| 模板 | `.source-projects/kqlparser/` | 工程分层范本（lexer/parser/ast/binder 分包结构、`Reset(offset)`、`File/Pos/Position` 三层抽象） | 中（结构与接口可借鉴，**语法细节要校验**） |
| 语料 | `.source-projects/kql-parser/fuzz_corpus_test.go` | 真实语料回归 | 仅测试输入 |

**操作规则**：实现任何词法/语法规则前，先查 `KqlTokens.g4`（词法）或 `Kql.g4`（语法）。
模板里看似合理的做法若与 g4 冲突，**改模板的做法，不改 g4**。

## 2. kqlparser 模板的已知偏差（已在本项目修正）

以下偏差在 F1 实现时发现，已在 `internal/frontend/token/` 与 lexer 中修正。

### 2.1 关键字大小写：必须大小写不敏感 ✅ 已修

- **g4**：`KqlTokens.g4` 的 `BOOLEANLITERAL` 显式列 `true|false|TRUE|FALSE|True|False`；
  ANTLR lexer 默认大小写敏感但 KQL 词法对关键字本就归一（参考官方文档）。
- **kqlparser**：`token.Lookup(ident)` 做精确匹配，不归一化 → `WHERE`/`Where` 不被识别为关键字。
- **本项目修正**：`token/keywords.go` 的 `Lookup` 先 `strings.ToLower` 再查表。
  **代价**：每次 lookup 一次 ToLower 分配（可后续用 ASCII 快路径优化）。

### 2.2 `<typekeyword>(...)` 是词法分组，不是函数调用 ⚠️ lexer 必须实现

- **g4**（`KqlTokens.g4`）：`DATETIMELITERAL`、`GUIDLITERAL`、`TIMESPANLITERAL`、
  `LONGLITERAL`、`REALLITERAL`、`DECIMALLITERAL`、`BOOLEANLITERAL` 都是 **lexeme 级 token**，
  内容用 `LparenGooRparen: '(' (~')')* ')'` 整段吞下。例如：
  - `datetime(2020-01-01T00:00:00Z)` → 整个是一个 `DATETIME` token（内容含 `-`/`:`，无法正常重新分词）
  - `guid(xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx)` → 整个是一个 `GUID` token
  - `timespan(1.02:03:04)` → 整个一个 `TIMESPAN` token
  - `long(...)` / `int(...)` / `int32(...)` → `INT` token
  - `real(...)` / `double(...)` → `REAL` token
  - `decimal(...)` → 单独的 DECIMAL（我们 token 表暂无 DECIMAL 字面量 token，见 §3 TODO）
  - `bool(true)` → `BOOL` token
- **kqlparser**：**不**做这个分组 —— `datetime` 当普通标识符、`(...)` 当括号。
  导致 `datetime(2020-01-01T00:00:00Z)` 被 kqlparser 切成
  `[IDENT "datetime", LPAREN, INT 2020, SUB, ...]` 一堆烂 token。
- **本项目**：lexer 在识别到类型关键字后，若紧跟 `(`，则用 `scanTypeLiteral` 整段
  吞到匹配的 `)`，产出对应字面量 token。这是 F1.S4 必须做的，**不能省**。

### 2.3 `dynamic(...)` 不是词法 token ✅ lexer 当普通关键字

- **g4**（`Kql.g4:1501`）：`dynamicLiteralExpression: DYNAMIC '(' jsonValue ')'` ——
  在 **parser 层**组装，DYNAMIC 是个普通 token（KqlTokens.g4:312 `'dynamic'`）。
- **结论**：lexer 遇到 `dynamic` 输出 `DYNAMICTYPE` 关键字 token，`(...)` 里的 json
  由 parser 解析。**不要**在 lexer 把 `dynamic(...)` 当整体字面量。
- **本项目**：token 表里 `DYNAMIC`（literal）目前**未被 lexer 使用** ——
  lexer 不会产出它；真正的动态字面量走 parser。`DYNAMIC` token 留着是占位，
  F2 的 `DynamicLit` 节点由 parser 构造，不依赖 lexer 产出 `DYNAMIC` token。
  （后续若发现无出处可删。）

### 2.5 关键字折叠拼写：只 g4 列的才算

- **g4**（`KqlTokens.g4`）：只有 `MVAPPLY: 'mvapply'` 和 `MVEXPAND: 'mvexpand'`
  提供**无连字符折叠形式**（对应带连字符的 `MV_APPLY: 'mv-apply'` / `MV_EXPAND: 'mv-expand'`）。
  其他算子如 `make-series`、`make-graph`、`project-away`、`graph-match` **没有**折叠形式。
- **kqlparser**：`keywords` init 里错误地加了 `makegraph`/`graphmatch`/`makeseries` 等
  折叠形式（实际 KQL 不接受这些）—— 模板 bug。
- **本项目**：`token/keywords.go` 只登记 g4 真正接受的折叠/别名形式
  （`mvapply`/`mvexpand`/`int64`/`boolean`/`date`/`time`/`external_data`/`with_source`/
  `notcontains`/`notcontainscs`/`assertschema`/`macroexpand`/`executeandcache`/`execute_and_cache`/
  `__partitionby`）。**不要**照搬 kqlparser 的折叠表。

### 2.6 TIMESPAN 后缀比 kqlparser 更全

- **g4**（`KqlTokens.g4` `TIMESPANLITERAL`）：
  - `m` → `m` / `min` / `minute` / `minutes`
  - `s` → `s` / `sec` / `second` / `seconds`
  - `d` → `d` / `day` / `days`
  - `h` → `h` / `hour` / `hours` / `hr` / `hrs`
  - `ms` / `milli(s/sec/second/seconds)` / `micro(...)` / `nano(...)`
  - `tick` / `ticks`
  - 小数 timespan：`TimespanNumber: ('0'..'9')+ ('.' ('0'..'9')+)?` —— 支持 `1.5d`
- **kqlparser**：`isTimespanSuffix` 只判起始字母 `d/h/m/s/t`，靠 `for isLetter` 吃后续，
  覆盖面其实够，但**不验证**后缀合法性（`1xyz` 也会被当 timespan）。
- **本项目**：lexer 用"吃首字母 + 吃后续字母"的宽松策略（与 kqlparser 一致），
  合法性校验留到 parser/语义层。优先保证吞吐，不提前拒错。

### 2.7 字符串：`h`/`H` 前缀 + 多行字符串 + verbatim

- **g4**（`KqlTokens.g4` `STRINGLITERAL`）：
  - 可选 `h`/`H` 前缀（hint "hashed"）
  - 普通 `"..."`/`'...'`：`EscapeSequence` 转义
  - verbatim `@"..."`/`@'...'`：双引号转义（`""`→`"`），**不**处理 `\`
  - 多行 ```` ```...``` ```` 和 `~~~...~~~`
- **kqlparser**：上述都有，逻辑可直接借鉴（`lexer.go:371-418` + 多行 421-478）。
- **本项目**：照搬 kqlparser 的 `scanString`/`scanVerbatimString`/`scanMultiLineString`，
  已校验与 g4 一致。

### 2.8 表达式优先级：严格按 g4，**不沿用 token.Precedence** ⚠️ F3 关键

- **g4**（`Kql.g4:883-987`）优先级阶梯（低→高）：
  1. `logicalOrExpression` → OR
  2. `logicalAndExpression` → AND
  3. `equalityExpression` → `==`/`<>`/`!=` + in/!in/in~/has_any/has_all + between/!between
  4. `relationalExpression` → `<`/`>`/`<=`/`>=`
  5. `additiveExpression` → `+`/`-`
  6. `multiplicativeExpression` → `*`/`/`/`%`
  7. `stringOperatorExpression` → has/contains/startswith/.../matches regex/`=~`/`!~`/`:`（**比乘法还高！**）
  8. `invocationExpression` → 一元 `+`/`-`（**无 not**，见 §2.9）
  9. `functionCallOrPathExpression` → 后缀 `.`/`[]`
  10. `primaryExpression` → 字面量/名引用/括号/datatable
- **kqlparser `token.Precedence()` 偏差**：把字符串操作符与比较操作符放在同一层（都返回 3），
  且 AND/OR 也混进来了。**与 g4 不符**。
- **本项目 F3 做法**：parser **不**用 token.Precedence 做 Pratt，改用显式分层方法
  `parseExpr → parseOr → parseAnd → parseEquality → parseRelational → parseAdditive →
   parseMultiplicative → parseStringOp → parseUnary → parsePostfix → parsePrimary`。
  in/between 在 equality 层特殊处理（它们带 `(...)`/`..` 范围语法）。
  token.Precedence 在 token 包留着（不影响 lexer），但 parser 不依赖它。

### 2.9 一元 `not`：KQL **没有**一元 not 运算符 ⚠️

- **g4**：`invocationExpression: ('+'|'-')? functionCallOrPathExpression` —— 只有 `+`/`-`。
  逻辑非走 **`not(...)` 函数**（builtin），不是运算符。
- **本项目 F2 偏差**：`ast.UnaryExpr` 注释写了 `Op: ADD, SUB, or NOT` —— `NOT` 是错的。
  **修正**：UnaryExpr 的 Op 只允许 `ADD`/`SUB`；`not()` 走 CallExpr（函数调用）。

## 3. 待办 / 遗留问题（TODO）

- **DECIMAL 字面量 token**：g4 有 `DECIMALLITERAL: DECIMAL '(' ... ')'`，
  我们 token 表没 `DECIMAL` 字面量类型（只有 `DECIMALTYPE` 关键字）。
  F1 暂不处理 decimal 字面量（MVP 算子不需要），待用到时补 `DECIMAL` literal token。
- **`h`/`H` 前缀串与标识符歧义**：`has`/`hours` 等以 h 开头的关键字 vs `h"..."` 串。
  lexer 必须**在 isLetter 之前**检查 h/H 后跟引号的情况（kqlparser 已这样做，照搬）。
- **关键字大小写 lookup 的性能**：`strings.ToLower` 每标识符一次分配。
  BenchmarkLexer 显示 ~120 MB/s、68 allocs/op —— 主要分配点在此。
  后续若成瓶颈，换 ASCII in-place 小写化（KQL 标识符基本 ASCII）。
- **`h`/`H` 前缀串的字面量范围**：scanString 必须用 `scanStringFrom(startOffset)`
  把 `h` 包进 Lit（已实现，见 `lexer/string.go`）。
- **流式 Reader 推迟**：F1.S6 提到 "Reader 流式接口避免一次性载入超大查询"。
  首版**不实现**——曾写过一版但跨 chunk 的位置语义有缺陷（File 偏移不稳定），
  属于过早抽象。KQL 查询几乎都小到能整体入内存（`New(filename, src string)` 够用）。
  真有超大脚本文件需求时再设计带全局偏移的 Reader。吞吐量目标（F1.S6 真正的验收项）
  由 `BenchmarkLexer` 满足：~120 MB/s、~36 ns/token。
- **吞吐基线缺位**：F1 文档要求 "≥ kqlparser lexer 的 50%"，但 kqlparser 无 benchmark，
  无法量化对比。当前 ~120 MB/s 对 parser 热路径足够；待 T5 大语料上线后可重测。

## 4. 实现进度

### F1 词法层 ✅ 完成

| 子目标 | 状态 | 文件 |
|---|---|---|
| F1.S1 token 枚举 + Position/Pos/File 三层 | ✅ | `token/token.go`, `token/position.go` |
| F1.S2 关键字表 + 大小写不敏感 Lookup | ✅ | `token/keywords.go` |
| F1.S3 lexer 主循环 + Reset/File 接口 | ✅ | `lexer/lexer.go` |
| F1.S4 字符串/数值/timespan/类型字面量分组 | ✅ | `lexer/string.go`, `lexer/number.go`, `lexer/operator.go` |
| F1.S5 错误恢复（ErrorList 不 panic） | ✅ | `lexer/lexer.go` |
| F1.S6 throughput benchmark | ✅ | `lexer/lexer_bench_test.go`（~120 MB/s） |
| F1.S6 流式 Reader | ⏸ 推迟（见 §3） | — |

验收：`StormEvents | where State == "TEXAS" | take 10` 切出 9 个 token，位置连续无重叠 ✅

### F2 AST 骨架 ✅ 完成

Node/Expr/Stmt/Operator 接口（包私有 marker 闭集）+ 全部 P0 节点 + Visitor/BaseVisitor。
`internal/frontend/ast/`（node/literal/ref/expr/operator/stmt/visitor/visit_base）+ 测试。

### F3 parser 表达式 ✅ 完成（F4 tabular 待做）

| 子目标 | 状态 | 文件 |
|---|---|---|
| F3.S1 parser 骨架 + 错误恢复（save/restore + sync） | ✅ | `parser/parser.go` |
| F3.S2 字面量/引用解析 | ✅ | `parser/primary.go` |
| F3.S3 函数调用 + 命名参数 | ✅ | `parser/primary.go`（parseCall/parseArgument） |
| F3.S4 分层二元/一元（g4 优先级阶梯） | ✅ | `parser/expr.go` |
| F3.S5 后缀 `.`/`[]` + in/between | ✅ | `parser/expr.go`（parsePostfix/parseInList/parseBetween） |
| F3.S6 表达式集成测试 | ✅ | `parser/expr_test.go` |

**附带产出**：`internal/frontend/diagnostic/`（F6 提前到 F3 做，因为 parser 依赖）——
Diagnostic/Severity/Code（KQL000/005/001/...）/List（Add/Dedup/HasErrors/Render）+ 测试。

关键决策（详见 §2.8/§2.9）：
- parser **不**用 token.Precedence 做 Pratt，改用显式 10 层方法链严格对齐 g4 优先级阶梯
  （string 操作符比乘法**还高**，kqlparser 的 Precedence 把它放错了层）。
- KQL **无**一元 not（`not()` 是函数）；UnaryExpr 只允许 +/-。

⚠️ **save/restore 的关键陷阱**：lexer 每次 Scan 后已推进到 cur 的**下一个** token，
所以 `save()` 时 `lx.Offset()` 指向 cur 之后。要回滚到 cur 重解析，必须按 `p.pos`（cur 的
字节起始）重置 lexer（`Reset(int(pos)-1)`）再 `next()`，而不是用 `lx.Offset()`。
见 `parser.go` 的 savedState/restore。这条踩过坑，记下来防再犯。

### F4 parser tabular P0 ✅ 完成

| 子目标 | 状态 | 文件 |
|---|---|---|
| F4.S1 Pipeline 顶层（`|` 分割算子） | ✅ | `parser/pipeline.go` |
| F4.S2 where / project / extend | ✅ | `parser/op_simple.go` |
| F4.S3 take / sort(order) / top | ✅ | `parser/op_simple.go`, `parser/op_sort.go` |
| F4.S4 summarize（agg by keys, bin 函数） | ✅ | `parser/op_summarize.go` |
| F4.S5 join（kind=…, on conditions） | ✅ | `parser/op_join.go` |
| F4.S6 let / union / distinct / count | ✅ | `parser/op_simple.go`, `parser/op_union.go`, `parser/script.go` |
| F4.S7 端到端集成测试 | ✅ | `parser/tabular_test.go` |

验收（来自 F4-parser-tabular.md）：能解析
`T | where x > 0 | extend y = x*2 | summarize count() by y | order by y desc | take 10`
✅（TestEndToEndFullQuery，5 个算子齐全）。

**F4 期间发现并修正的 3 个关键问题（防再犯）：**

1. **JOIN 关键字漏 tokenStrings 条目**（F1 遗留 bug）：JOIN 常量存在但 `tokenStrings[JOIN]`
   为空 → `Lookup("join")` 返回 IDENT → join 算子解析失败。根因：从 kqlparser 复制 token 表
   时漏了这一行。**已修**：补 `JOIN: "join"`，并加 `TestKeywordRoundTrip` 审计测试 ——
   每个关键字 const 必须能在 `tokenStrings` 里找到、且 `Lookup` 大小写双向 round-trip。
   **教训**：新增关键字 const 时必须同时加 tokenStrings 条目，测试会拦。

2. **operator param 值不能走 parseIdentFollowed**：`kind=inner (T2)` 里 `inner` 是值、
   `(T2)` 是 join 的右表。若 param value 走 `parseIdentFollowed`，会把 `inner (T2)` 当函数
   调用吞掉。**修**：`parseParamValue` 只吃单个 token（IDENT/keyword/字面量），不进 postfix/call。
   对齐 g4 queryOperatorProperty（值是单 identifier 或 literal）。

3. **tryParamName 不能对任意 IDENT 探测**：曾用 "IDENT 后跟 '=' 就当 param 名" 的启发，
   结果把 `summarize c = count()` 的 `c`（聚合别名）误判成 param。**修**：只认 g4 的
   keyword 形 param 名（kind/withsource/datascope）；hint.* 形（g4 HINT_* token）我们 token
   表暂无，按 IDENT 留给 body/binder 处理。**原则**：strict param 名是封闭关键字集，不是任意 IDENT。

**Pipeline 顶层**：`parseStatement` → `let` 走 `parseLetStmt`（RHS 用 parsePipeline 兼顾
标量 let 与表 let），否则走 `parsePipeline` → `QueryStmt{Pipeline}`。
let 的 RHS 若无 `|`，回退为纯标量 Expr（`let n = 5` 的 Expr 是 BasicLit 而非 Pipeline）。

### 后续待做（F5/F7）
- F5 binder：符号/类型/schema 流、列绑定到物理列 ID、严格模式。
- F7 builtin：从 kqlparser/builtin 抽 380+ 函数清单。

## 5. 语料覆盖（T1–T3）

**语料来源**：`.source-projects/kql-parser/fuzz_corpus_test.go`（Microsoft Sentinel / Defender
真实狩猎查询）→ 抽取 89 个 `.kql` 文件到 `pkg/kql/testdata/corpus/sentinel/`。
提取器用 go/ast walker，见 `testdata/corpus/README.md`。

**回归测试**：`pkg/kql/corpus_test.go`
- `TestCorpusCoverage`：全量 89 个查询过 parse→translate→emit，记录通过率（**当前 83/89 = 93%**）。
- `TestCorpusP0Subset`：排除 P1+ 算子（parse/mv-expand/make-series/consume/getschema/...）后的 P0 子集，要求 ≥90%（**当前 65/67 = 97%**）。
- 这两个测试是 parser/translator 的回归护栏：任何能解析的查询不能因为重构而退步。

**一轮修复（39%→72%，2026-06-17）从语料挖出的真实缺口**：

1. **join 子管道**：`join kind=... (T | where ... | ...) on ...` 的右括号里是完整管道，不是标量表达式。`parseJoinRight` 现在用 lookahead 区分 `(管道)` vs `(表达式)`，translator 的 `translateJoin` unwrap ParenExpr 找内层 Pipeline。
2. **`project-reorder`**：高频算子。P0-adjacent，暂复用 ProjectOp 表示（语义有损——需要 F5 binder 知道完整输入列才能真正重排）。
3. **数组字面量 `[...]`**：`dynamic([...])` 和独立数组。`parsePrimary` 新增 `case token.LBRACKET` → ListExpr。（影响 7 个查询，最大单一缺口）
4. **`in~` / INCI token**：case-insensitive IN（g4 `IN_CI: 'in~'`）。原 lexer 只处理 `!in~`，正向 `in~` 把 `~` 留成游离 token → ILLEGAL。token 表加 INCI，scanIdentifier 在识别到 IN 后检查尾随 `~` 升级为 INCI。emit 把 `in~`/`has_any`/`has_all` 都按 IN-list 处理。

## 6. P1/P2 算子 + 语料遗留缺口

**P1/P2 算子已落地（85%→93%）**：mv-expand / make-series / parse / parse-where /
render / consume / getschema / serialize / externaldata / evaluate 全部能解析。
其中多数用 **passthrough 策略**（translate 成 Project\{\*\}，emit 出 SELECT \*），
保证下游 stage 的列引用不断；真实语义（mv-expand 的 UNNEST、make-series 的时序填充、
parse 的 regex 抽取）留到各后端线 + NeedsPostProc 标记时实现。

**关键解析修复**：
- **函数调用的管道参数**：`materialize(T | where ...)` —— 管道直接出现在调用括号里
  （不是包在另一层 `()` 里）。`parseArgument` 用 `isPipelineArg` lookahead 检测
  `<expr> |`，命中则按管道解析。也覆盖 `(T | ...)` 双层括号形式。
- **P2 算子 passthrough**：top-nested / partition / fork / lookup / facet / sample /
  sample-distinct / reduce 用 `parsePassthroughOp` 捕获算子名 + 跳过到下一 stage 边界
  （平衡括号），translate 成 passthrough。

**剩余 6 个 parse 失败（真复杂 P2，下一轮）**：
- **函数式 lambda `let f = (x:int) {...}`**（22）：`(params) {body}` 语法 + `:` 类型注解。
- **`datatable(name:type, ...)` 作为 source**（89）：schema 带 `:` 类型。
- **`union isfuzzy=true (subq)` 作为函数式 source**（02）：union 既算子又函数调用。
- **`\` 字符**（54）：verbatim 串里的反斜杠边界（lexer edge）。
- **`| externaldata` storage 子句**（72）：`(`schema`)` 后的 `[StorageAccounts=...]`。
- 这些都需要更深的 grammar 分支（lambda 体、datatable 求值、union 双重身份）。

## 7. builtin 函数表（F7）

**结构**：`internal/frontend/builtin/builtin.go` —— `Spec{Name, MinArgs, MaxArgs,
IsAggregate, SQLite, NeedsPostProc}` 表 + `Lookup(name)`（大小写不敏感）。
**不是**从 kqlparser 抄全部 380+ 函数，而是按语料实际使用频率（ago/tostring/
dcount/make_set/split/isnotempty/sum/...）建表，逐步补。

**emit 接线**：`sqlite/emit_expr.go` 的 `emitFuncCall` 先查 builtin 表：
- 有 SQLite 模板 → 用模板（%s 占位填入各参数的 SQL）。
- `strcat`（变长）→ `a || b || c`；`coalesce`（变长）→ `coalesce(a,b,c)`。
- 标 NeedsPostProc 的（split/extract/make_set/percentile）→ 记录到 emitter.postProc
  （hook，留 B5 客户端 post-process 框架用），best-effort 透传。
- 表里没有的函数 → 沿用旧 best-effort 透传 `NAME(args)`。

**翻译正确性示例**：ago→datetime('now','-'||x)、tostring→CAST AS TEXT、
iff/iif→CASE、dcount→COUNT(DISTINCT)、countif→SUM(CASE WHEN)、
make_set→group_concat(DISTINCT)、toint→CAST AS INTEGER、isnotempty→(x!='')。

## 8. summarize 裸列 group-key 保留原名 ⚠️

`summarize ... by state`（裸列 group key，无别名）原本 emit 成 `state AS key0`，
导致后续 `sort by state` 找不到列（"no such column"）。**修**：裸 *ir.Col 的
group key 用原列名做别名（`state AS state`）；只有计算表达式且无名的 key 才用
`key%d`。这是跨 stage 列解析的权宜——真正解决要 F5 binder 跟踪每 stage 的输出 schema。
