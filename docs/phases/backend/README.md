# 后端线阶段拆解

> 范围：`internal/backend/{pg,duckdb,sqlite}` + 共享 emit 框架
> 总目标：把优化器输出的 PhysicalPlan 翻成各方言最优 SQL/Plan；pg 为主，duckdb/sqlite 为辅

## 阶段

### Phase B1 — Backend 接口与 Emit 框架
**目标**：定义统一后端接口 + PhysicalPlan → SQL 的通用骨架。

子目标：
- Backend 接口（`backend.go`：Dialect() / Emit(plan) → (sql, args, error) / Capabilities()）。
- SQL builder 工具（`sqlbuild/`：标识符引用、参数占位符、CTE 嵌套、类型映射）。
- PhysicalStep 抽象（optimizer 输出，后端只读）。

验收：mock PhysicalPlan 能生成"形状正确"的 SQL（无需真实执行）；参数绑定正确。
产出物：backend.go + sqlbuild + 测试。
依赖：I1（IR）、O3（PhysicalPlan/AltPlan）。

### Phase B2 — pg 后端 P0 算子（单 SELECT 生成）
**目标**：pg 上把相邻 P0 算子合并进单 SELECT。

子目标：
- pg 方言（`pg/dialect.go`：标识符 `"col"`、参数 `$1`、类型映射）。
- Source/Filter/Project/Extend → 单 SELECT 各子句。
- Sort/Limit → ORDER BY / LIMIT。
- driver 接线（`pg/conn.go`：pgx 连接池 + 批量拉取）。

验收：`T | where x > 0 | extend y = x*2 | take 10` 生成一条扁平 SELECT；可在本地 pg 执行。
产出物：pg P0 emit + driver + 集成测试。
依赖：B1、S2（exec Backend 接口）。

### Phase B3 — pg summarize/join 断 CTE 路径
**目标**：遇到 summarize/join/窗口算子时断开为 CTE 或子查询。

子目标：
- CTE 生成器（`pg/cte.go`：命名、嵌套深度控制）。
- summarize → GROUP BY（断 CTE 当后续还有算子时）。
- join → JOIN 子句（含 join hint 写入，由 optimizer 决定）。
- CTE 物化策略（pg 14+ `MATERIALIZED` / `NOT MATERIALIZED`，由 optimizer 决定）。

验收：`T | summarize c = count() by k | where c > 10` 生成 CTE；join 带 hint。
产出物：pg cte/join emit + 测试。
依赖：B2、O4（join altplan）。

### Phase B4 — duckdb 后端
**目标**：复用 B1 框架，列式友好优化。

子目标：
- duckdb 方言（`duckdb/dialect.go`：标识符 `"col"`、参数 `$1`/`?`、类型映射）。
- 列式优化：summarize 优先用 duckdb 内建聚合（比 pg 全）。
- driver 接线（`duckdb/conn.go`：duckdb-go，原生 Arrow 零拷贝输出）。

验收：P0 算子在 duckdb 上能执行；Arrow 输出零拷贝路径打通。
产出物：duckdb emit + driver + 测试。
依赖：B1、S2。

### Phase B5 — sqlite 后端 + NeedsPostProcess 降级
**目标**：sqlite 单机零依赖，能力受限时降级到客户端 post-process。

子目标：
- sqlite 方言（`sqlite/dialect.go`：标识符 `"col"`、参数 `?`）。
- 能力受限识别：window/series/mv-expand 等标 NeedsPostProc。
- 客户端 post-process 框架（`sqlite/postproc.go`：拉回行集后在 Go 内算窗口/缺失函数）。
- driver 接线（`sqlite/conn.go`：mattn/go-sqlite3 cgo）。

验收：window 函数查询在 sqlite 上走 post-process；结果与 pg 一致。
产出物：sqlite emit + postproc + driver + 测试。
依赖：B1、S2。

### Phase B6 — UDF 生成
**目标**：必要时引入 UDF 补足各后端缺失函数。

子目标：
- pg：临时 plpgsql 函数生成（`pg/udf.go`：percentile / series_* / 自定义聚合）。
- duckdb：优先内建，缺的写 UDF（`duckdb/udf.go`）。
- sqlite：尽量走 post-process，避免 UDF 复杂度。

验收：`summarize percentile(x, 90)` 在 pg 上生成临时函数 + 调用；duckdb 用内建 dpercentile。
产出物：各后端 udf 包 + 测试。
依赖：B2/B3/B4/B5。

### Phase B7 — 三后端 SQL 输出快照测试
**目标**：同一 IR → 各方言 SQL 对照，防止后端漂移。

子目标：
- 快照测试框架（`backend/snapshot_test.go`：IR → 各方言 SQL 文本对比 golden file）。
- 覆盖 T3 P0 子集。
- 跨后端语义等价性验证（mock 数据，三后端结果一致）。

验收：改 IR 或优化器后三后端 SQL golden 不漂移；结果等价。
产出物：snapshot test + golden file。
依赖：B2/B4/B5、T3、T4。

## 关键决策记录

1. **pg 为主**：生产场景是 pg。pg 走 pgx 纯 Go 驱动无 cgo 门槛；pg 优化器成熟，能接住"能合就合"生成的扁平 SELECT。
2. **"能合就合"而非管道直译 CTE 链**：相邻 P0 算子合并进单 SELECT，让 pg 优化器看到全貌做谓词下推/列裁剪。只有 summarize/join/窗口才断 CTE。CTE 链虽结构清晰但增加嵌套、阻碍 pg 部分优化。
3. **UDF/降级各后端不同**：pg 走 plpgsql 临时函数；duckdb 优先内建（聚合比 pg 全）；sqlite 能力受限走客户端 post-process（Go 内算窗口/缺失函数）。这是 DESIGN.md 第 7 节"必要时才引入 UDF"的具体化。
4. **标识符与参数占位符方言差异集中处理**：sqlbuild 工具统一封装，后端只声明方言策略，不在多处硬编码。
5. **driver 统一接口**：B1 接口下三后端实现同一 Backend，让 S2（exec）不关心具体后端。

## 风险与对策

| 风险 | 对策 |
|---|---|
| pg 标识符大小写（unquoted 折叠为小写） | 列引用用物理列 ID，emit 时统一加双引号 |
| 参数绑定 `$1`(pg) vs `?`(duckdb/sqlite) | sqlbuild 抽象参数占位符，后端实现 |
| covering index 触发条件 | optimizer 标 ExecuteStrategy，后端按 hint 生成 |
| pg CTE 默认 MATERIALIZED 拖慢 | optimizer 决策，emit 时显式 `NOT MATERIALIZED` |
| cgo 编译门槛（sqlite/duckdb） | CLI 构建标签分离纯 pg 版（`-tags pgonly`） |
| duckdb Arrow 零拷贝与 pg/sqlite 行式接口不一致 | exec 层抽象为 `arrow.Record`，内部适配 |
| 三后端语义差异（NULL 排序、类型转换） | snapshot test 覆盖 + 已知差异文档化 |
