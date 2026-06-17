# I4 — IR Pretty-Printer（仅 Explain/调试用）

> 范围：`internal/ir/print.go`
> 依赖：I1
> 阶段目标：IR 可读输出，**仅用于 Explain 与调试快照**，不作为运行时产物

## 顺序化子目标

### I4.S1 — Pretty-print（文本形式，主用）
- 产出：`ir/print.go`（Print(p *Pipeline) string，缩进 + 可选 ANSI 颜色）。
- 验收：人工可读；包含 Stage 类型、Expr 树、能力位标记（debug 模式）。
- 测试来源：手写快照。

### I4.S2 — YAML dump（可选，仅 Explain 请求时）
- 产出：`ir/print_yaml.go`（MarshalYAML，仅在 Explain 显式请求时调用）。
- 验收：YAML 输出稳定；**核心执行路径（frontend→ir→optimizer→backend→SQL）不调用此函数**。
- 测试来源：手写。

### I4.S3 — 构建隔离验证
- 产出：构建标签或条件编译验证"产 SQL 不依赖 print_yaml"。
- 验收：移除 print_yaml.go 后 `kql <query>` 仍能产出 SQL 并执行（仅 `kql explain --format yaml` 失效）。
- 测试来源：构建脚本 + 冒烟测试。

### I4.S4 — Explain 集成（与 S5 协作）
- 产出：`ir/print.go` 提供 Explain 所需文本接口（供 shell/S5 调用）。
- 验收：`kql explain` 输出 IR 树（默认文本，`--format yaml` 切 YAML）。
- 测试来源：S5 集成。

## 阶段产出物
- `internal/ir/print.go` + `internal/ir/print_yaml.go`
- 构建隔离测试

## 风险与对策
| 风险 | 对策 |
|---|---|
| YAML 序列化进入核心路径 | S3 构建隔离验证 |
| Pretty-print 与 IR 漂移 | S1 跟随 I1/I2 测试更新 |
| Explain 输出冗长 | S1 提供 verbose/compact 模式 |
