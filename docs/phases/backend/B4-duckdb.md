# B4 — duckdb 后端

> 范围：`internal/backend/duckdb/`
> 依赖：B1、S2
> 阶段目标：复用 B1 框架，列式友好优化

## 顺序化子目标

### B4.S1 — duckdb 方言
- 产出：`duckdb/dialect.go`（标识符 `"col"`、参数 `$1` 或 `?`、类型映射，duckdb 兼容 pg 风格）。
- 验收：与 pg 方言差异点明确（如 duckdb 特有函数）。
- 测试来源：手写。

### B4.S2 — 列式友好优化
- 产出：`duckdb/emit.go`（summarize 优先用 duckdb 内建聚合；避免行式回退）。
- 验收：summarize/count/avg 等用 duckdb 列式聚合；不退化到逐行。
- 测试来源：T3 P0。

### B4.S3 — driver 接线（原生 Arrow 零拷贝）
- 产出：`duckdb/conn.go`（duckdb-go，原生 Arrow 输出）、`duckdb/backend.go`。
- 验收：查询返回 arrow.Record 零拷贝路径打通；类型映射正确。
- 测试来源：本地 duckdb 文件。

### B4.S4 — P0 端到端冒烟
- 产出：`duckdb/smoke_test.go`（duckdb 文件 + P0 查询）。
- 验收：`orders | where id > 100 | take 10` 返回结果。
- 测试来源：本地 duckdb + 种子数据。

### B4.S5 — 内建聚合优先级
- 产出：`duckdb/aggregates.go`（duckdb 内建聚合清单；与 pg 差异标注）。
- 验收：能用 duckdb 内建的聚合（quantile_cont 等）优先用。
- 测试来源：F7 表对照。

## 阶段产出物
- `internal/backend/duckdb/`（dialect/emit/conn/backend/aggregates）
- 冒烟测试

## 风险与对策
| 风险 | 对策 |
|---|---|
| duckdb Arrow 零拷贝与行式接口不一致 | S3 exec 层统一 arrow.Record |
| duckdb 版本差异（聚合函数可用性） | S5 标注最低版本要求 |
| cgo 编译门槛 | 构建标签分离（S4） |
