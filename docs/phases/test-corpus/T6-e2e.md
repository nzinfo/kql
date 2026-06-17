# T6（后置）— 端到端执行结果对比

> 范围：e2e 对比测试 + 差异文档
> 依赖：S3、S4、S6、B7、T3
> 阶段目标：真实数据库（或 mock）执行结果跨后端等价性验证

## 顺序化子目标

### T6.S1 — mock dataset
- 产出：`testdata/mock/<set>/`（小规模固定数据集，三后端加载）。
- 验收：dataset 可被 pg/duckdb/sqlite 加载；数据一致。
- 测试来源：手写 + 种子脚本。

### T6.S2 — 端到端对比
- 产出：语料 KQL → 各后端执行 → 结果对比。
- 验收：T3 P0 子集三后端结果一致。
- 测试来源：T3 P0 + mock dataset。

### T6.S3 — 已知差异文档化
- 产出：`docs/backend-differences.md`（NULL 排序、类型转换、聚合语义差异）。
- 验收：差异文档覆盖测试观察到的所有不一致。
- 测试来源：T6.S2 观察。

### T6.S4 — 真实 pg 对比（可选）
- 产出：本地/CI pg 实例 + 种子数据，对比 mock 与真实 pg 结果。
- 验收：真实 pg 结果与 mock 一致。
- 测试来源：本地 pg + 环境变量 `KQL_TEST_PG_DSN`。

### T6.S5 — 回归守护
- 产出：CI job 跑 T6（mock 默认，真实 pg 可选）。
- 验收：CI 绿；差异变化时 fail。
- 测试来源：CI 配置。

## 阶段产出物
- `testdata/mock/<set>/`
- e2e 对比测试
- `docs/backend-differences.md`

## 风险与对策
| 风险 | 对策 |
|---|---|
| 真实 pg 环境依赖 | T6 默认 mock；真实 pg 可选 job |
| 三后端语义差异漏判 | T6.S2 + T6.S3 双保险 |
| mock dataset 与真实数据偏差 | T6.S4 真实 pg 对比兜底 |
