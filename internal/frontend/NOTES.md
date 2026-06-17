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

## 4. F1 实现进度

| 子目标 | 状态 | 文件 |
|---|---|---|
| F1.S1 token 枚举 + Position/Pos/File 三层 | ✅ | `token/token.go`, `token/position.go` |
| F1.S2 关键字表 + 大小写不敏感 Lookup | ✅ | `token/keywords.go` |
| F1.S3 lexer 主循环 + Reset/File 接口 | ✅ | `lexer/lexer.go` |
| F1.S4 字符串/数值/timespan/类型字面量分组 | ✅ | `lexer/string.go`, `lexer/number.go`, `lexer/operator.go` |
| F1.S5 错误恢复（ErrorList 不 panic） | ✅ | `lexer/lexer.go`（ErrorList + scanTypeLiteral 等错误路径） |
| F1.S6 throughput benchmark | ✅ | `lexer/lexer_bench_test.go`（~120 MB/s） |
| F1.S6 流式 Reader | ⏸ 推迟（见 §3） | — |

验收（来自 F1-lexer.md）：`StormEvents | where State == "TEXAS" | take 10` 切出 9 个 token，
位置连续无重叠 —— ✅ 由 `TestAcceptanceQuery` + `TestPositionsContiguous` 保证。
`go test ./internal/frontend/...` 全绿。
