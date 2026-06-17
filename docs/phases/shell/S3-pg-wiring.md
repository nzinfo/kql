# S3 — pg 后端接线（最早可用链路）

> 范围：`internal/exec/dsn.go` + `cmd/kql/main.go`（最小版）
> 依赖：S1、S2、B2、F4、I2、O3
> 阶段目标：打通端到端最小链路：CLI → frontend → IR → pg

## 顺序化子目标

### S3.S1 — DSN 解析
- 产出：`exec/dsn.go`（识别 pg/duckdb/sqlite scheme；提取连接参数）。
- 验收：`postgres://user:pass@host/db` 解析正确；未知 scheme 报错并给支持列表。
- 测试来源：手写。

### S3.S2 — pg driver 注册到 Engine
- 产出：`exec/pg_registry.go`（pg backend 注册；Engine 通过 registry 查找）。
- 验收：Engine 用 pg DSN 能拿到 pg Backend。
- 测试来源：手写。

### S3.S3 — 最小 CLI
- 产出：`cmd/kql/main.go`（`kql -d <dsn> '<query>'`，输出 csv/arrow 之一）。
- 验收：CLI 能解析参数；调用 pkg/kql.Exec；输出结果。
- 测试来源：手写冒烟。

### S3.S4 — 端到端冒烟（本地 pg）
- 产出：`cmd/kql/smoke_test.go`（本地 pg + 一张表 + P0 查询）。
- 验收：`kql -d postgres://... 'orders | where id > 100 | take 10'` 真实返回结果。
- 测试来源：本地 pg + 种子数据（可选 CI job）。

### S3.S5 — 错误透出
- 产出：CLI 错误渲染（diagnostic → `file:line:col: KQL001: ...`）。
- 验收：解析错误/绑定错误/执行错误都正确显示。
- 测试来源：手写负例。

## 阶段产出物
- `internal/exec/dsn.go` + `pg_registry.go`
- `cmd/kql/main.go`（最小版）
- 冒烟测试

## 风险与对策
| 风险 | 对策 |
|---|---|
| 本地 pg 环境依赖 | S4 可选 CI job；默认走 mock |
| DSN 格式多样 | S1 支持标准 URL + key=value |
| CLI 输出 Arrow 不可读 | S3 默认 csv，arrow 作为 --format 选项 |
