# S6 — mock backend + 端到端测试框架

> 范围：`internal/exec/mock.go` + e2e 框架 + CI
> 依赖：S3、T3、T4、B7
> 阶段目标：脱离真实数据库也能跑回归；CI 集成

## 顺序化子目标

### S6.S1 — mock backend
- 产出：`exec/mock.go`（固定数据集 + 记录生成的 SQL；实现 Backend 接口）。
- 验收：mock backend 能被 Engine 使用；返回固定行集。
- 测试来源：手写。

### S6.S2 — 端到端测试框架
- 产出：`testutil/e2e.go`（语料 KQL → 各后端 SQL 对照 + mock 执行结果）。
- 验收：T3 P0 子集端到端跑通。
- 测试来源：T3 P0 + mock dataset。

### S6.S3 — CI 配置
- 产出：`.github/workflows/ci.yml`（lint + unit + snapshot + 可选 pg 集成）。
- 验收：CI 绿；矩阵覆盖 cgo 与非 cgo。
- 测试来源：CI 运行。

### S6.S4 — mock dataset 管理
- 产出：`testdata/mock/<set>/`（小规模固定数据集，三后端加载）。
- 验收：mock dataset 可被三后端加载。
- 测试来源：手写。

### S6.S5 — 回归测试覆盖
- 产出：mock backend 让前端/IR/优化器改动不依赖真实 pg 也能验证；T5 fuzz 也接入。
- 验收：CI 跑通 T3 P0 + T5 fuzz。
- 测试来源：T3 + T5。

## 阶段产出物
- `internal/exec/mock.go`
- `internal/testutil/e2e.go`
- CI 配置 + mock dataset

## 风险与对策
| 风险 | 对策 |
|---|---|
| mock 与真实后端行为差异 | T6（后置）真实 pg 对比兜底 |
| CI 环境复杂 | mock 优先，真实 pg 可选 job |
| 测试慢 | mock dataset 小；fuzz 限时 |
