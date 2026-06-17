# F1 — 词法层

> 范围：`internal/frontend/token/` + `internal/frontend/lexer/`
> 依赖：无
> 阶段目标：把 KQL 文本切成带位置的 token 流，错误 token 不 panic 而进 diagnostic 流

## 顺序化子目标

### F1.S1 — token 类型与位置定义
- 产出：`token/token.go`（TokenType 枚举 + Token 结构）、`token/position.go`。
- 覆盖（直接对齐 `kqlparser/token/token.go:9-220`，参考校验报告 `F1-F2-verification.md`）：
  - **字面量**：IDENT / INT / REAL / STRING / DATETIME / TIMESPAN / GUID / BOOL / DYNAMIC / **TYPE**（typeof(string)，KQL 真有此 token，校验补）
  - **算术/比较操作符**：ADD/SUB/MUL/QUO/REM/EQL/NEQ/LSS/GTR/LEQ/GEQ/TILDE/NTILDE
  - **分隔符**：PIPE/ASSIGN/COLON/SEMI/COMMA/DOT/DOTDOT/ARROW/LPAREN/RPAREN/LBRACKET/RBRACKET/LBRACE/RBRACE
  - **graph 边操作符**（MVP 不实现 graph，但 token 必须预留，否则 lexer 把它们误识别为标识符组合）：DASHDASH（`--`）/DASHGT（`-->`）/LTDASH（`<--`）/DASHLBRACK（`-[`）/LTDASHLBRACK（`<-[`）/RBRACKDASH（`]-`）/RBRACKDASHGT（`]->`），见 `kqlparser/token/token.go:53-60`
  - **关键字**：见 F1.S2（独立子目标）
- **采用 go/scanner 三层位置抽象**（校验补，参考 `kqlparser/token/position.go`）：
  - `Position`：人类可读（File/Filename/Line/Column）
  - `Pos`：紧凑文件内偏移（int），用于 AST 节点
  - `File`：行表（offset→Line/Col 映射），供 Position/Pos 互转
- 验收：每个 token 类型有 godoc；`IsLiteral()/IsOperator()/IsKeyword()` 范围判断可用（用 `literalBeg/literalEnd` 等分组常量实现 go/scanner 风格）。
- 测试来源：手写单元用例 + 官方 grammar `Kql.g4` 中 token 规则对照 + kqlparser token 枚举对齐。

### F1.S2 — 关键字表与保留字
- 产出：`token/keywords.go`（关键字 map + IsKeyword / LookupIdent 大小写处理）。
- **直接搬 `kqlparser/token/token.go:70-220` 的 ~80 个关键字清单**（校验补，不要自己重新整理）。涵盖：查询算子（WHERE/PROJECT/EXTEND/SUMMARIZE/JOIN/...）、多变体（PROJECT/PROJECTAWAY/PROJECTKEEP/PROJECTRENAME/PROJECTREORDER/PROJECTSMART）、GRAPH*（GRAPHMATCH/MAKEGRAPH/...）、PARSE*（PARSE/PARSEKV/PARSEWHERE）、数据类型关键字、控制关键字（LET/SET/DECLARE/PATTERN/ALIAS）等。
- 覆盖：KQL 关键字大小写不敏感（`WHERE`/`where`/`Where` 等价）。
- 验收：未知标识符不被误判为关键字；关键字大小写归一化；与 kqlparser 关键字清单完全对齐。
- 测试来源：手写用例 + Kql.g4 关键字列表 + kqlparser 关键字清单对照。

### F1.S3 — 手写 tokenizer 主循环
- 产出：`lexer/lexer.go`（Lexer 结构、Next()/All()/Reset(offset) 接口、scanToken switch）。
- 覆盖：跳空白/注释（`//`、`/* */`）、识别多字符操作符优先于单字符。
- **关键设计（校验补，对齐 `kqlparser/lexer/lexer.go:13-103`）**：
  - Lexer 用 `ch rune` + `offset` + `rdOffset` 推进，`next()` 解 UTF-8（处理 RuneError）
  - **`Reset(offset int)` 接口**：lexer 暴露重置能力，**供 parser 做 lookahead/回溯**（F3.S1 的 savedState 基础设施）。这是 kqlparser 验证过的关键接口。
  - `File()` 暴露 `*token.File`，供 parser/binder 共享位置信息
  - 错误进 `ErrorList`（不 panic），见 F1.S5
- 验收：`StormEvents | where State == "TEXAS" | take 10` 切出 9 个 token；位置连续无重叠；Reset 后能从指定 offset 重新扫描。
- 测试来源：手写 + 官方 grammar 注释里的最小示例 + kqlparser lexer_test 镜像。

### F1.S4 — 字符串与字面量扫描
- 产出：`lexer/string.go`（普通字符串、verbatim、转义）、`lexer/number.go`（int/long/real/timespan 后缀）、`lexer/literal.go`（datetime/dynamic/guid 字面量识别）。
- **字符串规则（对齐 `kqlparser/lexer/lexer.go:371-418`）**：
  - 普通字符串 `"..."` / `'...'`：`\` 转义
  - **verbatim 字符串 `@"..."` / `@'...'`：双引号转义**（`""` 表示一个 `"`），**不处理 `\`**。与 kqlparser scanVerbatimString 完全一致。
  - 未闭合字符串进 ErrorList 不 panic
- **数值与 timespan 后缀（对齐 `kqlparser/lexer/lexer.go:300-369`）**：
  - 支持 hex（`0x1F`）
  - **timespan 后缀完整列表**（校验补全，原 F1.S4 只列了 `1h`/`1d`）：`d`(day) / `h`(hour) / `m`(minute/milli/micro) / `s`(second) / `t`(tick) 起始，**接受完整词与缩写**：`1day`/`1d`/`1hour`/`1hr`/`1minute`/`1min`/`1ms`/`1second`/`1sec`/`1microsecond`/`1tick`
  - **小数 timespan 也支持**：`1.5d` / `1.5h`（kqlparser scanNumber 第 338 行处理此情况）
- 验收：`@"C:\path"`、`datetime(2020-01-01T00:00:00Z)`、`dynamic([1,2,3])`、`1h`、`1.5d`、`1min`、`100L`、`0x1F` 正确切出。
- 测试来源：手写 + Kql.g4 literal 规则 + kqlparser lexer_test。

### F1.S5 — 错误恢复与 diagnostic 输出
- 产出：lexer 错误路径产出 `diagnostic.Diagnostic`（KQL000 词法错误码占位），不 panic。
- 覆盖：未知字符、未闭合字符串、非法数值后缀。
- 验收：错误 token 进 diagnostic 流且 lexer 继续（跳过坏字符到下一个合法 token）。
- 测试来源：手写负例。

### F1.S6 — 性能基线与流式接口
- 产出：`Reader` 流式接口（避免一次性载入超大查询）、benchmark 对比 kqlparser lexer（`lexer_bench_test.go`）。
- 验收：吞吐 ≥ kqlparser lexer 的 50%（量化基线记录到 O5）。
- 测试来源：T5 大语料做吞吐压测。

## 阶段产出物
- `internal/frontend/token/`（token.go / position.go / keywords.go）
- `internal/frontend/lexer/`（lexer.go / string.go / number.go / literal.go）
- 单元测试 + benchmark

## 风险与对策
| 风险 | 对策 |
|---|---|
| 字符串操作符与标识符混淆（`has`/`contains`） | 在 S2 关键字表登记，S3 主循环优先匹配关键字 |
| verbatim 字符串与插值字符串边界 | S4 单独函数处理，明确状态机 |
| 错误恢复吃掉太多输入 | 跳到下一个空白/分隔符即重试，记录诊断 |
