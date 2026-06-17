# B5 — sqlite 后端 + NeedsPostProcess 降级

> 范围：`internal/backend/sqlite/`
> 依赖：B1、S2
> 阶段目标：sqlite 单机零依赖，能力受限时降级到客户端 post-process

## 顺序化子目标

### B5.S1 — sqlite 方言
- 产出：`sqlite/dialect.go`（标识符 `"col"`、参数 `?`、类型映射，sqlite 类型系统宽松）。
- 验收：与 pg/duckdb 方言差异明确。
- 测试来源：手写。

### B5.S2 — 能力受限识别
- 产出：`sqlite/caps.go`（window/series/mv-expand/部分聚合标 NeedsPostProc；基于 F7 + I3 能力位）。
- 验收：window 函数查询在 sqlite 上走 post-process 标记。
- 测试来源：手写。

### B5.S3 — 客户端 post-process 框架
- 产出：`sqlite/postproc.go`（拉回行集后在 Go 内算窗口/缺失函数；结果按原 schema 组装）。
- 验收：window 函数查询结果与 pg 一致。
- 测试来源：T6 mock dataset 跨后端对比。

### B5.S4 — driver 接线
- 产出：`sqlite/conn.go`（mattn/go-sqlite3 cgo）、`sqlite/backend.go`。
- 验收：能打开 .db 文件；执行 SQL；返回行集。
- 测试来源：本地 sqlite 文件。

### B5.S5 — P0 端到端冒烟
- 产出：`sqlite/smoke_test.go`（sqlite 文件 + P0 查询）。
- 验收：P0 查询返回结果；与 pg 结果一致（mock dataset）。
- 测试来源：T6 mock dataset。

## 阶段产出物
- `internal/backend/sqlite/`（dialect/caps/postproc/conn/backend）
- 冒烟测试

## 风险与对策
| 风险 | 对策 |
|---|---|
| post-process 性能差 | 仅在能力缺失时启用；P0 主路径不走 post-process |
| sqlite 类型宽松导致结果偏差 | S3 显式类型转换 |
| cgo 编译门槛 | 构建标签分离（S4） |
