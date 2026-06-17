# B7 — 三后端 SQL 输出快照测试

> 范围：`internal/backend/snapshot_test.go` + golden file
> 依赖：B2/B4/B5、T3、T4
> 阶段目标：同一 IR → 各方言 SQL 对照，防止后端漂移

## 顺序化子目标

### B7.S1 — 快照测试框架
- 产出：`backend/snapshot_test.go`（IR → 各方言 SQL 文本对比 golden file；接 T4 golden）。
- 验收：框架可批量跑 T3 P0 子集；diff 输出可读。
- 测试来源：T4 golden 框架。

### B7.S2 — P0 子集覆盖
- 产出：T3 P0 子集 → 三方言 SQL golden 文件（`testdata/corpus/p0/*.golden.sql.{pg,duckdb,sqlite}`）。
- 验收：改 IR 或优化器后三后端 SQL golden 不漂移（除预期重构）。
- 测试来源：T3 P0。

### B7.S3 — 跨后端语义等价
- 产出：mock dataset 上同一 KQL 在 pg/duckdb/sqlite 结果对比。
- 验收：P0 子集三后端结果一致；已知差异（NULL 排序、类型转换）文档化。
- 测试来源：T6 mock dataset。

### B7.S4 — 已知差异文档
- 产出：`docs/backend-differences.md`（NULL 排序、类型转换、聚合语义差异）。
- 验收：差异文档覆盖测试观察到的所有不一致。
- 测试来源：B7.S3 观察。

### B7.S5 — CI 集成
- 产出：CI job 跑 snapshot + 跨后端对比。
- 验收：CI 绿；golden 漂移 fail 并提示 `-update`。
- 测试来源：CI 配置。

## 阶段产出物
- `internal/backend/snapshot_test.go`
- golden file 集合
- `docs/backend-differences.md`

## 风险与对策
| 风险 | 对策 |
|---|---|
| golden 大面积漂移 | 区分预期（重构）与意外（bug） |
| 跨后端语义差异漏判 | S3 mock dataset + S4 文档双保险 |
| CI 跨后端环境复杂 | mock dataset 替代真实 pg（真实 pg 可选 job） |
