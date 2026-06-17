# F5 — Binder（符号 + 类型 + schema 流）

> 范围：`internal/frontend/binder/`
> 依赖：F4、F7（builtin 函数签名）、stats catalog（表 schema 来源，O0）
> 阶段目标：在 AST 上做名称解析、类型推断、列来源追踪，绑定列引用到物理列 ID

## 顺序化子目标

### F5.S1 — 符号与作用域
- 产出：`binder/scope.go`（Scope 栈：全局 let、表 schema、列引用；Symbol{Kind, Type, Decl Pos}）、`binder/symbol.go`。
- 验收：嵌套 `let` 作用域正确；同名符号内层遮蔽外层；未知符号按严格模式报 KQL001。
- 测试来源：手写作用域用例。

### F5.S2 — 表 schema 加载（依赖 stats catalog）
- 产出：`binder/schema.go`（从 O0 StatsCatalog 读取表/列定义；构造列符号）。
- 验收：`orders.status` 解析时能查到 `orders` 表的 `status` 列；未知列在严格模式报错。
- 测试来源：stats catalog 示例（DESIGN.md 6.2 节样例）。

### F5.S3 — 列引用绑定到物理列 ID
- 产出：`binder/column_binding.go`（每个 Column/Path 节点绑定 ColID，供 IR/后端用）。
- 验收：相同列名跨表不冲突；view/CTE 边界重新绑定。
- 测试来源：手写 + T3 P0。

### F5.S4 — 类型推断
- 产出：`binder/types.go`（标量类型 bool/int/long/real/string/datetime/timespan/dynamic + tabular 类型；推断规则）。
- 覆盖：算术（int+int=int，int+real=real）、比较（→bool）、聚合（sum→同参数数值、count→long）、字符串操作符（→bool）。
- 验收：`extend y = x*2` 中 `y` 类型推断为 `x` 同类型；类型不匹配报 KQL002。
- 测试来源：手写 + builtin 表（F7）签名驱动。

### F5.S5 — schema 流（每个 Stage 输出列集合）
- 产出：`binder/flow.go`（按管道顺序传播列集合；project 改列集；extend 增列；summarize 重置列集为 by+聚合）。
- 验收：`T | extend x = 1 | project x | where x > 0` 中 `where` 能看到 `x`；引用不存在列报错。
- 测试来源：T3 P0 端到端。

### F5.S6 — 函数调用解析（依赖 builtin F7）
- 产出：`binder/call.go`（按 F7 builtin 表查签名；参数数量/类型校验；返回类型填入）。
- 验收：`count()`、`sum(x)`、`bin(x,1h)`、`iff(c,a,b)` 签名匹配；未知函数 KQL003。
- 测试来源：F7 builtin 表 + 手写。

### F5.S7 — 严格模式开关与诊断透出
- 产出：`binder/binder.go`（Binder 主入口 + StrictMode 选项；产出带 code 的 Diagnostic）。
- 验收：非严格模式未知列/函数为 warning；严格模式为 error。
- 测试来源：手写正反例。

## 阶段产出物
- `internal/frontend/binder/`（scope/symbol/schema/column_binding/types/flow/call/binder）
- 绑定单元测试 + 集成测试

## 风险与对策
| 风险 | 对策 |
|---|---|
| 物理列 ID 在 view/CTE 边界失效 | S3 在 view 引入新命名空间时重新绑定 |
| schema 流与 stats catalog 耦合 | S2 通过接口抽象 schema 源，便于 mock |
| 类型推断规则复杂 | 集中在 types.go + F7 表驱动 |
| 作用域跨 `let` 语句顺序 | S1 作用域栈先扫一遍 let 声明再解析引用 |
