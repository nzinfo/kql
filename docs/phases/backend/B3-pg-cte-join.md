# B3 — pg summarize/join 断 CTE 路径

> 范围：`internal/backend/pg/cte.go` + `pg/emit_join.go`
> 依赖：B2、O4（join altplan）
> 阶段目标：遇到 summarize/join/窗口算子时断开为 CTE 或子查询

## 顺序化子目标

### B3.S1 — CTE 生成器
- 产出：`pg/cte.go`（CTE 命名、嵌套深度控制、MATERIALIZED/NOT MATERIALIZED 由 optimizer 决策）。
- 验收：多层 summarize 生成嵌套 CTE；深度可控避免无限嵌套。
- 测试来源：手写 + T3 含多 summarize。

### B3.S2 — summarize emit
- 产出：`pg/emit_aggregate.go`（GROUP BY + 聚合列；后续算子时断 CTE）。
- 验收：`T | summarize c = count() by k | where c > 10` → CTE + 外层 WHERE。
- 测试来源：T3 P0。

### B3.S3 — join emit
- 产出：`pg/emit_join.go`（JOIN/LEFT/RIGHT/FULL；含 join hint 写入由 optimizer 决策；ON 条件）。
- 验收：`T1 | join kind=inner (T2) on k` → INNER JOIN；hint 正确写入。
- 测试来源：T3 P0 + O4 altplan。

### B3.S4 — CTE 物化策略
- 产出：pg 14+ `MATERIALIZED` / `NOT MATERIALIZED` 显式标注（由 optimizer 决策）。
- 验收：小 CTE 标 NOT MATERIALIZED 让 pg 内联；大 CTE 标 MATERIALIZED。
- 测试来源：手写 + O5 代价对比。

### B3.S5 — 集成测试
- 产出：`pg/integration_test.go`（summarize/join/CTE 组合查询）。
- 验收：复杂管道生成正确 SQL；本地 pg 执行结果正确。
- 测试来源：T3 P0 + 本地 pg。

## 阶段产出物
- `internal/backend/pg/{cte,emit_aggregate,emit_join}.go`
- 集成测试

## 风险与对策
| 风险 | 对策 |
|---|---|
| CTE 嵌套过深 | 嵌套深度阈值 + 必要时拍平为子查询 |
| pg 默认 MATERIALIZED 拖慢 | S4 显式标注 NOT MATERIALIZED |
| join hint pg 支持有限 | 优先用结构性优化（子查询形状），hint 作辅助 |
