# B6 — UDF 生成

> 范围：`internal/backend/{pg,duckdb,sqlite}/udf.go`
> 依赖：B2/B3/B4/B5
> 阶段目标：必要时引入 UDF 补足各后端缺失函数

## 顺序化子目标

### B6.S1 — UDF 需求识别
- 产出：基于 IR FuncCall.Caps.NeedsUDF 标记，收集需要 UDF 的函数清单。
- 验收：能从 PhysicalPlan 提取 UDF 需求集。
- 测试来源：手写含 NeedsUDF 的 plan。

### B6.S2 — pg plpgsql 临时函数
- 产出：`pg/udf.go`（生成 `CREATE TEMP FUNCTION ... LANGUAGE plpgsql`；连接结束时自动清理）。
- 验收：`summarize percentile(x, 90)` 在 pg 上生成临时函数 + 调用。
- 测试来源：手写 + 本地 pg。

### B6.S3 — duckdb UDF
- 产出：`duckdb/udf.go`（优先内建；缺的写 UDF，duckdb-go 注册）。
- 验收：能用内建 dpercentile；缺失函数走 UDF。
- 测试来源：手写 + 本地 duckdb。

### B6.S4 — sqlite 尽量走 post-process
- 产出：`sqlite/udf.go`（默认空实现，走 post-process；仅极少数场景考虑 sqlite UDF）。
- 验收：sqlite 上 NeedsUDF 函数走 post-process 而非 UDF。
- 测试来源：手写 + B5.S3。

### B6.S5 — UDF 生命周期管理
- 产出：临时函数注册/清理；连接复用时不重复创建。
- 验收：多次查询同一 UDF 只创建一次。
- 测试来源：手写。

## 阶段产出物
- `internal/backend/{pg,duckdb,sqlite}/udf.go`
- UDF 生命周期测试

## 风险与对策
| 风险 | 对策 |
|---|---|
| 临时函数泄漏 | S5 连接结束清理 + 显式 DROP |
| UDF 性能差 | 仅在无内建替代时使用 |
| 三后端 UDF 语义不一致 | T6 跨后端结果对比验证 |
