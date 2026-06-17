# I5 — IR 等价性测试（快照用途）

> 范围：`internal/ir/equiv.go` + golden 框架接线
> 依赖：I2、T3（语料）、T4（golden 机制）
> 阶段目标：保证 AST→IR 翻译与重写不破坏语义；golden 快照是测试/调试工具，非产物

## 顺序化子目标

### I5.S1 — IR 规范化（canonical form）
- 产出：`ir/canonical.go`（规范化函数：列 ID 重编号、稳定排序、消除位置差异），用于等价对比。
- 验收：两个语义等价但写法不同的 IR 规范化后相同。
- 测试来源：手写等价对。

### I5.S2 — Golden file 集成
- 产出：`ir/golden_test.go`（T3 P0 子集 → IR 文本对照；接 T4 golden 框架）。
- 验收：F4/I2 改动后 golden 不应有非预期漂移；`-update` flag 可批量刷新。
- 测试来源：T3 P0 + T4。

### I5.S3 — 重写前后语义等价验证
- 产出：`ir/equiv.go`（语义等价判定：用 SQL 执行结果对比，而非仅 IR 形状）+ 测试框架。
- 验收：O2 规则重写前后，跑同一 SQL（mock backend）结果一致。
- 测试来源：mock dataset（S6）+ T3 P0。

### I5.S4 — 反例测试（捕获意外漂移）
- 产出：明显语义变化的改动应被测试捕获的用例集（如谓词删改、聚合函数替换）。
- 验收：反例测试在错误改动时失败。
- 测试来源：手写。

## 阶段产出物
- `internal/ir/canonical.go` + `internal/ir/equiv.go`
- golden 测试 + 反例测试

## 风险与对策
| 风险 | 对策 |
|---|---|
| 等价判定漏判 | S3 用 SQL 结果对比兜底 |
| 规范化过强导致假等价 | S1 仅消除位置/命名差异，不动语义 |
| golden 大面积漂移 | S2 区分预期漂移（重构）与意外漂移（bug） |
