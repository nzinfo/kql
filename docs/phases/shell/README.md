# 外壳线阶段拆解（CLI + 嵌入式库）

> 范围：`cmd/kql/main.go` + `pkg/kql.go` + `internal/exec/`
> 总目标：对外只暴露 `pkg/kql`（internal 锁死），CLI 与库共享同一入口，返回 `arrow.Record`

## 阶段

### Phase S1 — pkg/kql API 骨架
**目标**：定义公开 API 签名与错误类型，让其他线有目标可对接。

子目标：
- 公开 API（`pkg/kql.go`）：
  - `Exec(ctx, dsn, query string, args ...) (arrow.Record, error)`
  - `Explain(ctx, dsn, query string) (*ExplainOutput, error)`
  - `Validate(ctx, query string) []Diagnostic`
  - `Optimize(ctx, query string, catalog StatsCatalog) (*PhysicalPlan, error)`
- 配置结构（Options：backend 名、stats catalog 路径、decision policy 名、strict 模式）。
- 错误类型（公开 `KqlError`：诊断码 + 位置 + 链路 phase）。
- Engine 结构持有各线组件（frontend/ir/optimizer/backend），构造器 + 依赖注入。

验收：API 签名编译通过；mock 各组件可跑空查询。
产出物：pkg/kql.go + 错误类型 + 测试骨架。
依赖：无（接口先行）。

### Phase S2 — internal/exec Backend 接口 + Arrow 转换
**目标**：统一三后端的执行与结果转换。

子目标：
- Backend 接口（`exec/backend.go`：Query(ctx, sql, args, schema) → arrow.Record）。
- schema 描述（列名/类型，从 IR 推导）。
- Arrow 记录构造器（`exec/arrow.go`：driver.Rows → arrow.Record）。
  - pg：pgx 批量拉取 → Arrow。
  - duckdb：duckdb-go 原生 Arrow 零拷贝。
  - sqlite：行式拉取 → Arrow。
- 资源管理（Record 生命周期、释放器）。

验收：mock backend 返回固定行集 → Arrow；列类型正确。
产出物：exec 包 + Arrow 转换测试。
依赖：S1（用 Engine 结构）。

### Phase S3 — pg 后端接线（最早可用链路）
**目标**：打通端到端最小链路：CLI → frontend → IR → pg。

子目标：
- pg driver 注册到 Engine。
- DSN 解析（`exec/dsn.go`：识别 pg/duckdb/sqlite scheme）。
- 最小 CLI（`cmd/kql/main.go`：`-d <dsn> '<query>'`，输出 arrow/csv 之一）。
- 端到端冒烟：本地 pg + 一张表 + P0 查询。

验收：`kql -d postgres://... 'orders | where id > 100 | take 10'` 真实返回结果。
产出物：pg 接线 + CLI 最小版 + 冒烟测试。
依赖：S1、S2、B2、F4、I2、O3。

### Phase S4 — duckdb / sqlite 后端接线
**目标**：扩展支持另两个后端。

子目标：
- duckdb 注册（cgo 构建）。
- sqlite 注册（cgo 构建）。
- 构建标签分离（`pgonly` 纯 Go 版本无 cgo）。

验收：同一查询三后端可切换执行；cgo 构建标签工作。
产出物：duckdb/sqlite 接线 + 构建配置。
依赖：S3、B4、B5。

### Phase S5 — CLI 输出格式 + Explain 子命令
**目标**：完善 CLI 用户体验。

子目标：
- 输出格式（`cmd/kql/output.go`：arrow/csv/parquet/json）。
- Explain 子命令：输出 IR 树 + 优化前后代价 + 决策 reason。
- Validate 子命令：只解析不执行，输出诊断。
- 统计 catalog 加载选项（`--stats <path>`）。
- decision policy 切换选项（`--policy conservative|aggressive|gated`）。

验收：`kql explain -d ... '<query>'` 输出可读 IR + 代价；`--policy` 切换有效。
产出物：CLI 全功能 + Explain。
依赖：S3、S4、O3（Explain）、O5（代价对比）。

### Phase S6 — mock backend + 端到端测试框架
**目标**：脱离真实数据库也能跑回归。

子目标：
- mock backend（`exec/mock.go`：固定数据集 + 记录生成的 SQL）。
- 端到端测试（T3 P0 子集 → 各后端 SQL 对照 + mock 执行结果）。
- CI 集成（lint + unit + snapshot + 可选 pg 集成测试）。

验收：CI 跑通；mock backend 让前端/IR/优化器改动不依赖真实 pg 也能验证。
产出物：mock backend + e2e 框架 + CI 配置。
依赖：S3、T3、T4、B7。

## 关键决策记录

1. **对外只暴露 pkg/kql，internal 锁死**：避免外部依赖实现细节，便于重构；CLI 与库共享同一入口保证一致行为。
2. **CLI 与库共享入口**：CLI 是 thin wrapper，调用 `pkg/kql.Exec`；库用户能用同样能力。维护成本一份。
3. **Arrow 输出 vs 其他格式边界**：核心 API 返回 `arrow.Record`（列式、跨语言友好、零拷贝对接 duckdb）；CSV/Parquet/JSON 是 CLI 层的格式适配，不进核心 API。
4. **错误如何透出**：内部 diagnostic（带 code+位置）→ 公开 `KqlError`（保留 code+位置+phase）。CLI 渲染为 `file:line:col: KQL001: ...`。
5. **DSN 解析决定后端**：`postgres://` → pg；`duckdb://`/文件路径 → duckdb；`sqlite://`/`.db` → sqlite。后端注册表按 scheme 查找。
6. **构建标签分离 cgo**：`-tags pgonly` 构建纯 Go 版（无 sqlite/duckdb），降低部署门槛；默认全后端。

## 风险与对策

| 风险 | 对策 |
|---|---|
| cgo 编译门槛 | 构建标签 `pgonly`；CI 矩阵覆盖 cgo 与非 cgo |
| Arrow 不可直接 cat | CLI 层默认 csv，arrow 作为 `--format arrow` 选项 |
| DSN 解析错误难定位 | DSN 解析失败时给出支持的 scheme 列表 |
| Engine 依赖注入复杂 | 构造器带默认值 + Options 函数式选项 |
| 资源泄漏（Record/连接池） | Record 释放器 + context 超时 + driver pool 配置 |
| 库用户与 CLI 行为漂移 | 共享 Engine 构造，CLI 只是薄壳 |
