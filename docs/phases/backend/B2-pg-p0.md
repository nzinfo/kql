# B2 — pg 后端 P0 算子（单 SELECT 生成）

> 范围：`internal/backend/pg/`
> 依赖：B1、S2（exec Backend 接口）
> 阶段目标：pg 上把相邻 P0 算子合并进单 SELECT

## 顺序化子目标

### B2.S1 — pg 方言
- 产出：`pg/dialect.go`（标识符 `"col"`、参数 `$1`、类型映射、字符串字面量）。
- 验收：标识符正确引用；保留字处理；参数绑定 $1..$n。
- 测试来源：手写。

### B2.S2 — Source/Filter/Project/Extend emit
- 产出：`pg/emit_select.go`（单 SELECT 各子句生成）。
- 验收：`T | where x > 0 | extend y = x*2 | project y` → 扁平 SELECT。
- 测试来源：T3 P0。

### B2.S3 — Sort/Limit emit
- 产出：`pg/emit_sort.go`（ORDER BY + LIMIT，含 NULLS FIRST/LAST）。
- 验收：`order by created_at desc nulls first | take 10` → ORDER BY ... LIMIT 10。
- 测试来源：T3 P0。

### B2.S4 — driver 接线
- 产出：`pg/conn.go`（pgx 连接池配置 + 批量拉取）、`pg/backend.go`（实现 backend.Backend 接口）。
- 验收：能连本地 pg；执行生成的 SQL；返回 driver.Rows。
- 测试来源：本地 pg 冒烟（可选 CI）。

### B2.S5 — P0 端到端冒烟
- 产出：`pg/smoke_test.go`（本地 pg + 一张表 + P0 查询）。
- 验收：`orders | where id > 100 | take 10` 真实返回结果。
- 测试来源：本地 pg + 种子数据。

## 阶段产出物
- `internal/backend/pg/`（dialect/emit_select/emit_sort/conn/backend）
- 端到端冒烟测试

## 风险与对策
| 风险 | 对策 |
|---|---|
| pg 标识符大小写折叠 | 列引用统一加双引号（B1.S2） |
| 参数绑定顺序错乱 | sqlbuild 集中管理参数编号 |
| pg 连接池配置不当 | S4 默认池大小可调 + context 超时 |
