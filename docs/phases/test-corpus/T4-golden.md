# T4 — Golden file 机制

> 范围：`internal/testutil/golden.go` + 各线 golden 文件
> 依赖：T3
> 阶段目标：查询 → AST/IR/SQL 快照对比，防漂移

## 顺序化子目标

### T4.S1 — Golden 框架
- 产出：`internal/testutil/golden.go`（Update flag + diff 输出 + 文件命名约定）。
- 验收：`go test -update` 刷新；否则对比；diff 可读。
- 测试来源：手写。

### T4.S2 — AST golden
- 产出：F4 输出对照（`*.golden.ast`）。
- 验收：F4 改动后 AST golden 不漂移（除预期重构）。
- 测试来源：T3 P0。

### T4.S3 — IR golden
- 产出：I2 输出对照（`*.golden.ir`）。
- 验收：I2 改动后 IR golden 不漂移。
- 测试来源：T3 P0。

### T4.S4 — SQL golden（三后端）
- 产出：B7 三后端对照（`*.golden.sql.{pg,duckdb,sqlite}`）。
- 验收：B7 改动后 SQL golden 不漂移。
- 测试来源：T3 P0。

### T4.S5 — CI 集成
- 产出：CI 跑 golden 对比；漂移 fail 并提示 `-update`。
- 验收：CI 绿；预期漂移时 `-update` 刷新。
- 测试来源：CI 配置。

## 阶段产出物
- `internal/testutil/golden.go`
- 各线 golden 文件

## 风险与对策
| 风险 | 对策 |
|---|---|
| golden 大面积漂移 | 区分预期（重构）与意外（bug）；review diff |
| golden 文件爆炸 | 按算子分目录 + 压缩无关字段 |
| 非确定性输出（时间戳） | golden 框架规范化非确定字段 |
