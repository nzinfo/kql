# S4 — duckdb / sqlite 后端接线

> 范围：`internal/exec/{duckdb,sqlite}_registry.go`
> 依赖：S3、B4、B5
> 阶段目标：扩展支持另两个后端

## 顺序化子目标

### S4.S1 — duckdb 注册（cgo）
- 产出：`exec/duckdb_registry.go`（duckdb backend 注册；构建标签控制）。
- 验收：Engine 用 duckdb DSN 拿到 duckdb Backend。
- 测试来源：手写。

### S4.S2 — sqlite 注册（cgo）
- 产出：`exec/sqlite_registry.go`（sqlite backend 注册；构建标签控制）。
- 验收：Engine 用 sqlite DSN 拿到 sqlite Backend。
- 测试来源：手写。

### S4.S3 — 构建标签分离
- 产出：`pgonly` 构建标签（纯 Go 版无 cgo，仅 pg）、默认全后端。
- 验收：`-tags pgonly` 构建无 cgo 依赖；默认构建含三后端。
- 测试来源：CI 矩阵覆盖。

### S4.S4 — 跨后端切换冒烟
- 产出：同一查询三后端切换执行测试。
- 验收：`kql -d <dsn> '<query>'` 三种 DSN 都能跑。
- 测试来源：T6 mock dataset。

## 阶段产出物
- `internal/exec/{duckdb,sqlite}_registry.go`
- 构建标签配置
- 跨后端冒烟测试

## 风险与对策
| 风险 | 对策 |
|---|---|
| cgo 编译门槛 | pgonly 标签；CI 矩阵 |
| 三后端语义差异 | T6 跨后端对比 + 差异文档（B7.S4） |
| 注册表冲突 | 构建标签隔离注册代码 |
