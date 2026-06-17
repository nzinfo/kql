# F3 — Parser 核心（表达式）

> 范围：`internal/frontend/parser/`（表达式路径）
> 依赖：F1、F2
> 阶段目标：Pratt parser + 递归下降处理标量表达式

## 顺序化子目标

### F3.S1 — Parser 骨架与错误恢复
- 产出：`parser/parser.go`（Parser 结构、peek/next/expect、savedState 用于回溯、错误同步到 `|` 或 `;`）。
- 验收：错误后能继续到下一语句边界，收集多条 diagnostic。
- 测试来源：手写负例。

### F3.S2 — 字面量与引用解析
- 产出：`parser/primary.go`（Lit / Ident / Column / Path / Star / 括号表达式）。
- 验收：F1 各类 token 都能解析为对应 AST 节点。
- 测试来源：F1 单元用例的镜像。

### F3.S3 — 函数调用与参数列表
- 产出：`parser/call.go`（FuncCall{Name, Args, NamedArgs}，命名参数如 `join kind=inner`）。
- 验收：`summarize count() by State`、`bin(created_at, 1h)`、`iff(x > 0, 1, 0)` 解析正确。
- 测试来源：手写 + Kql.g4 functionCall 规则。

### F3.S4 — Pratt 二元/一元运算符
- 产出：`parser/expr.go`（precedence 表 + parseBinary/parseUnary），覆盖算术/比较/逻辑/字符串操作符（has/contains/startswith/endswith/match/hasprefix 等）。
- 验收：`1 + 2 * 3` → 优先级正确；`a has "x" and b > 0` 组合正确；与 Kql.g4 优先级一致。
- 测试来源：官方 grammar 表达式示例 + 手写优先级用例。

### F3.S5 — 特殊字面量与后缀
- 产出：`parser/special.go`（datetime/timespan/dynamic/guid 字面量、Member `a.b`、Index `a[0]`、Cast `todouble(x)`、Postfix）。
- 验收：`datetime(2020-01-01T00:00:00Z)`、`dynamic({"k":1})`、`1h`、`T.col`、`arr[0]` 解析正确。
- 测试来源：手写 + Kql.g4。

### F3.S6 — 表达式集成测试与 golden
- 产出：表达式层 golden 测试（T3 P0 表达式子集 → AST 文本对照）。
- 验收：T3 中纯表达式用例 golden 通过。
- 测试来源：T3 P0 回归集（表达式部分）。

## 阶段产出物
- `internal/frontend/parser/`（parser/primary/call/expr/special）
- 表达式 golden 测试

## 风险与对策
| 风险 | 对策 |
|---|---|
| 字符串操作符优先级与 SQL 习惯不同 | 严格按 Kql.g4，优先级表集中维护 |
| 命名参数与位置参数混用歧义 | S3 区分 namedArgs map 与 args slice |
| 回溯开销 | 仅在算子前缀歧义时回溯，记录 savedState |
| 与官方语法漂移 | S6 golden + T5 fuzz 双保险 |
