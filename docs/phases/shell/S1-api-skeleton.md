# S1 — pkg/kql API 骨架

> 范围：`pkg/kql.go`
> 依赖：无（接口先行）
> 阶段目标：定义公开 API 签名与错误类型，让其他线有目标可对接
>
> **校验状态**：已完成对 `rust-kql/kq/src/main.rs`（56 行 thin CLI）校验，详见 `S1-verification.md`。**重大修订**：原设计的 4 个公开 API + Engine + DI 容器对 MVP 过度。简化为 **2 个公开 API（Exec + Explain）**，Engine 用简单 struct（非 DI 容器）。参考 rust-kql thin wrapper 哲学——MVP 阶段控制总代码量。

## 顺序化子目标

### S1.S1 — 公开 API 签名（简化为 2 个）
- 产出：`pkg/kql.go`：
  - **`Exec(ctx, engine *Engine, query string, args ...) (arrow.Record, error)`**（主 API）
  - **`Explain(ctx, engine *Engine, query string) (*ExplainOutput, error)`**（Explain/调试）
- **简化（校验改，原设计 4 个）**：删去 `Validate`（用 Exec 的 dry-run 模式实现，或独立轻量函数 `ValidateQuery` 不在主 API）和 `Optimize`（改为内部函数不导出——优化是 Exec 的内部步骤，不应作公开 API）。
- 验收：API 编译通过；2 个签名稳定（其他线对接）；总 pkg/kql.go 控制在 ~150 行内。
- 测试来源：手写编译期断言。

### S1.S2 — Options 配置（精简）
- 产出：`pkg/options.go`（Options 核心字段 + 函数式选项）。
- **核心字段（校验精简，避免 MVP 过度）**：DSN / Backend 名 / StatsCatalogPath / DecisionPolicy 名 / Strict 模式。
- 函数式选项：`WithStats(path)` / `WithPolicy(name)` / `WithStrict()` / `WithLogger(l)`。
- 验收：默认值合理（空 catalog + conservative policy + 非 strict）；不引入未来才需要的字段。
- 测试来源：手写。

### S1.S3 — Engine 结构（简化，非 DI 容器）
- 产出：`pkg/engine.go`（Engine 简单 struct + NewEngine 构造器）。
- **简化（校验改，原设计是 DI 容器）**：
  ```go
  type Engine struct {
      backend    Backend           // 由 DSN 解析后注入
      stats      StatsReader       // 可空（nil 时走空 catalog）
      policy     DecisionPolicy    // 默认 Conservative
      opts       Options
      // frontend/ir/optimizer 是无状态组件，Exec 内部按需构造，不持久化
  }
  ```
- 构造器直接赋值（`NewEngine(opts)` 解析 DSN 后建立 backend 连接），**不引入 DI 容器**——MVP 阶段不需要。DI 推迟到真正需要（多 catalog 切换、运行时重配置）时再加。
- 验收：mock backend 可构造 Engine；Exec/Explain 调用链路打通（用 mock）；总 pkg 代码（kql+options+engine+errors+types）≤300 行。
- 测试来源：手写 mock。

### S1.S4 — 公开错误类型
- 产出：`pkg/errors.go`（KqlError：诊断码 + 位置 + phase；实现 error/Error/Unwrap）。
- 验收：错误从内部 diagnostic 透出到公开 KqlError；可被库用户类型断言。
- 测试来源：手写。

### S1.S5 — 公开类型导出（精简）
- 产出：`pkg/types.go`。
- **公开类型精简（校验改）**：`ExplainOutput` / `KqlError` / `Options` / `Engine`；**不导出** `StatsCatalog`（用 `StatsReader` 接口暴露）/ `PhysicalPlan` / IR 节点（内部类型一律不导出）。
- 验收：公开类型字段稳定；外部用户无法依赖内部结构。
- 测试来源：手写。

### S1.S6 — 输入数据文件加载（借鉴 rust-kql）
- 产出：`pkg/datasource.go`（按文件扩展名分发：csv/json/parquet/arrow 注册到 backend；参考 `rust-kql/kq/src/main.rs:35-46`）。
- 适用场景：CLI 用 `-f data.csv` 加载文件到 backend（pg 用临时表、duckdb/sqlite 用 register）。
- 验收：csv/parquet 文件能被加载并查询；扩展名未知时报错给支持列表。
- 测试来源：手写。

## 阶段产出物
- `pkg/`（kql.go / options.go / engine.go / errors.go / types.go / datasource.go）
- API 编译测试

## 风险与对策
| 风险 | 对策 |
|---|---|
| API 演进破坏兼容 | S1 签名稳定 + 版本化；不导出内部类型 |
| **MVP 过度设计**（校验新增） | S1.S1 2 个 API、S1.S3 简单 struct；rust-kql 56 行可跑，我们 pkg 应 ≤300 行 |
| 错误类型泄漏内部 | S4 公开 KqlError 只含必要字段 |
| **DI 容器复杂度**（校验新增） | S3 不引入 DI，推迟到真正需要 |
