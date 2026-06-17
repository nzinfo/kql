# I2 — AST → IR 翻译器（P0）

> 范围：`internal/ir/translate.go`
> 依赖：I1、F4（AST 节点）、F5（binder 提供列 ID）、F7（builtin Caps）
> 阶段目标：把绑定后的 AST 翻成 IR，覆盖 P0 算子

## 顺序化子目标

### I2.S1 — 翻译器入口与表达式翻译
- 产出：`ir/translate.go`（Translate(ast.Node, *binder.Result) → (*Pipeline, error)）、`ir/translate_expr.go`（AST Expr → IR Expr，保留物理列 ID）。
- 验收：F3 表达式测试用例的 AST 都能翻成等价 IR；列引用 ColID 正确传递。
- 测试来源：F3 单元用例镜像。

### I2.S2 — P0 单算子翻译
- 产出：`ir/translate_stage.go`（按算子类型分函数：translateWhere/translateProject/translateExtend/translateTake/translateSort/translateSummarize/translateJoin/translateUnion）。
- 验收：每个 P0 算子翻译前后语义等价（手写对比例）。
- 测试来源：F4 P0 子集镜像。

### I2.S3 — Pipeline 拼装与列集合传播
- 产出：`ir/translate_pipeline.go`（按管道顺序组装 Stages；每个 Stage 输入列集合来自上游输出）。
- 验收：`T | extend x = 1 | project x | where x > 0` 中 `where` 的 ColExpr 引用到 `extend` 新建的列 ID。
- 测试来源：T3 P0。

### I2.S4 — FuncCall 能力位填充
- 产出：翻译 FuncCall 时从 F7 builtin 表查 Caps 填入。
- 验收：`summarize percentile(x, 90)` 的 FuncCall.Caps.NeedsUDF 在 pg 上下文中为 true；`count()` SQLExpr=true。
- 测试来源：F7 表 + 手写。

### I2.S5 — 翻译错误诊断
- 产出：翻译失败时产出 diagnostic（KQL010+ 翻译错误码），不 panic。
- 验收：未支持的算子/构造给出清晰错误。
- 测试来源：手写负例（如未实现的 P2 算子）。

### I2.S6 — 翻译集成测试与 golden
- 产出：I2 端到端测试（T3 P0 全集 → IR 文本对照 golden）。
- 验收：T3 P0 全集翻译成功；IR golden 不漂移。
- 测试来源：T3 P0 + T4 golden。

## 阶段产出物
- `internal/ir/translate*.go`
- 翻译 golden 测试

## 风险与对策
| 风险 | 对策 |
|---|---|
| AST 与 IR 漂移 | S6 golden + 双向测试 |
| 列 ID 传递中断 | S3 显式列集合传播 + 单元测试 |
| 能力位填写遗漏 | S4 集中查表 + 测试覆盖高频函数 |
