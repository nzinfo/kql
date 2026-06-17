# F4 — Parser（Tabular 算子 P0）

> 范围：`internal/frontend/parser/`（tabular 路径）
> 依赖：F3
> 阶段目标：解析管道化查询语句，覆盖 MVP P0 算子

## 顺序化子目标

### F4.S1 — Pipeline 顶层解析
- 产出：`parser/pipeline.go`（按 `|` 分割算子序列、TabularExpression 顶层）。
- 验收：`T | op1 | op2 | op3` 解析为 Pipeline{Ops:[...]}。
- 测试来源：手写 + Kql.g4 tabularExpression 规则。

### F4.S2 — where / project / extend
- 产出：`parser/op_filter.go`（Where{Expr}）、`parser/op_project.go`（Project{Cols []ProjectColumn}，含 `project-rename`/`project-away` 占位）、`parser/op_extend.go`（Extend{Assigns []Assign}）。
- 验收：`where x > 0 and y != ""` / `project a, b = c + 1` / `extend r = rank()` 解析正确。
- 测试来源：手写 + T3 P0。

### F4.S3 — take / order by / sort / top
- 产出：`parser/op_limit.go`（Take{N}）、`parser/op_sort.go`（Sort{Keys []SortKey}，含 asc/desc/nulls 修饰）、Top{N, Key}。
- 验收：`take 10` / `order by created_at desc nulls first` / `top 5 by score desc` 解析正确。
- 测试来源：手写 + T3 P0。

### F4.S4 — summarize
- 产出：`parser/op_aggregate.go`（Summarize{Aggregates []Assign, By []Expr, Hint...}）。
- 验收：`summarize c = count() by status, bin(created_at, 1h)` 解析正确，含 `by` 子句、`bin` 函数。
- 测试来源：手写 + T3 P0 + Kql.g4 summarize。

### F4.S5 — join
- 产出：`parser/op_join.go`（Join{Kind, Right Pipeline, Conditions, Hint}）。
- 覆盖：`inner`/`left`/`right`/`full`/`innerunique` kind、`on` 条件、`hint.strategy`/`hint.distribution`。
- 验收：`join kind=inner (T2) on k1, k2` 解析正确。
- 测试来源：手写 + T3 P0。

### F4.S6 — let / union / distinct
- 产出：`parser/op_let.go`（LetStmt{Name, Expr, Params}）、`parser/op_set.go`（Union{Tables, WithSource}）、Distinct{Cols}。
- 验收：`let X = T | where x > 0; X | take 10` 与 `T | union T2 | distinct k` 解析正确。
- 测试来源：手写 + T3 P0。

### F4.S7 — 端到端解析集成测试与 golden
- 产出：F4 端到端测试（T3 P0 全集 → AST golden）。
- 验收：T3 P0 全集 100% 解析成功；AST golden 不漂移。
- 测试来源：T3 P0 回归集 + golden 机制（T4）。

## 阶段产出物
- `internal/frontend/parser/`（pipeline + 各算子 parser）
- P0 端到端 golden 测试

## 风险与对策
| 风险 | 对策 |
|---|---|
| 算子前缀歧义（`join` vs `join kind=...`） | 显式 parse 上下文状态；先尝试带参数形式失败回退 |
| tabular vs scalar 上下文混淆 | Tabular 位置入口独立于表达式入口 |
| `let` 作用域跨语句 | F5 binder 维护作用域栈处理 |
| 算子参数多样导致 parser 膨胀 | 每算子一文件（S2-S6），单一职责 |
