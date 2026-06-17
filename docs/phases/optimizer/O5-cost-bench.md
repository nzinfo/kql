# O5 — 优化前后代价对比基准

> 范围：`internal/optimizer/cost/bench_test.go` + Explain CLI 入口
> 依赖：O2、O3、O4、T3
> 阶段目标：把 Explain 与代价对比做成可观测工具；负优化检测

## 顺序化子目标

### O5.S1 — 优化前后 IR 树 dump
- 产出：`cost/dump.go`（dump Pipeline + 每 Stage 代价标注）。
- 验收：dump 输出含 IR 文本（I4）+ 代价数值。
- 测试来源：手写。

### O5.S2 — 基准测试
- 产出：`cost/bench_test.go`（固定 catalog 跑 T3 P0 一组查询，记录优化前后代价对比）。
- 验收：T3 P0 上优化后代价 ≤ 优化前；记录到基准报告。
- 测试来源：T3 P0 + stats catalog 示例。

### O5.S3 — 负优化检测
- 产出：当规则导致代价上升时 warn（不 fail，记录原因）。
- 验收：负优化场景有 warn 日志。
- 测试来源：手写负例（如某规则在小表上劣化）。

### O5.S4 — Explain CLI 入口（与 S5 协作）
- 产出：Explain 输出格式（IR 树 + 优化前后代价 + 决策日志 + 最终 SQL），供 S5 集成。
- 验收：`kql explain` 输出完整可读。
- 测试来源：S5 集成测试。

### O5.S5 — 性能基线记录
- 产出：解析吞吐、优化器延迟的 benchmark；记录到 `docs/perf-baseline.md`。
- 验收：F1.S6 / I2 / O2 各自 benchmark 数据归档。
- 测试来源：F1.S6 lexer bench + T5 fuzz。

## 阶段产出物
- `internal/optimizer/cost/{dump,bench_test}.go`
- Explain 入口
- `docs/perf-baseline.md`

## 风险与对策
| 风险 | 对策 |
|---|---|
| 代价估算与真实执行差距 | 标注"估算代价"非真实；未来 EXPLAIN ANALYZE 反馈 |
| 基准漂移 | O5 在 CI 跑 + 历史趋势记录 |
| 负优化噪声 | 区分"规则固有代价"与"统计不准" |
