# O0 认知校验报告（pg 系统表字段映射）

> 校验对象：O0-stats-catalog.md + DESIGN.md 第 6.2 节 YAML 契约
> 参考：PostgreSQL 官方文档 [pg_stats](https://www.postgresql.org/docs/current/view-pg-stats.html)、pg_class、pg_index、pg_matviews

## 1. 校验结论

**YAML 契约总体可由 pg 采集脚本生成，但有 1 个重大问题（corr_vs 跨列相关性 pg 不直接给）+ 2 个需降级为可选的字段**。建议把 corr_vs 从必需字段降为可选/估算字段，其他字段映射明确。

## 2. 逐字段映射表

| YAML 字段 | pg 来源 | 转换公式 | 可行性 |
|---|---|---|---|
| `tables.row_count` | `pg_class.reltuples`（float8，需 ANALYZE 后准确） | 直接读 | ✅ 直接 |
| `tables.avg_row_bytes` | `pg_class.relpages * current_setting('block_size') / NULLIF(reltuples,0)` | 计算得出 | ✅ 估算 |
| `columns.card` | `pg_stats.n_distinct`（**正数=绝对值，负数=比例**，需 `* reltuples` 转换） | `n_distinct>0 ? n_distinct : -n_distinct*reltuples` | ✅ 直接（需转换） |
| `columns.nulls` | `pg_stats.null_frac * reltuples` | 计算得出 | ✅ 直接 |
| `columns.mcv` | `pg_stats.most_common_vals`（text[]）+ `most_common_freqs`（float8[]） | 两数组配对 | ✅ 直接 |
| `columns.hist` | `pg_stats.histogram_bounds`（text[]，等频分桶边界） | 与 YAML `{kind: equi, buckets: [...]}` 直接对应 | ✅ 直接（注意是等频 not 等宽，YAML 注释要改） |
| `columns.corr_vs` | **pg 不直接给跨列相关性** | 仅 `pg_stats.correlation`（单列物理排序 vs 逻辑值） | ⚠️ **重大问题** |
| `indexes.cols` | `pg_index.indkey`（int2vector，指向 `pg_attribute.attnum`） | 解析 + join pg_attribute | ✅ 直接 |
| `indexes.include` | `pg_index.indnatts` vs `indnkeyatts`（pg 11+，差值 = INCLUDE 列数） | `indnatts - indnkeyatts` | ✅ 直接（pg 11+） |
| `indexes.unique` | `pg_index.indisunique` | 直接读 | ✅ 直接 |
| `indexes.kind` | `pg_am.amname`（join pg_index.indexrelid→pg_class.relam→pg_am） | btree/hash/gin/gist/brin | ✅ 直接 |
| `views`（物化视图） | `pg_matviews`（matviewname + definition） | 直接读 | ✅ 直接 |
| `cost_model.seq_page_cost` | `current_setting('seq_page_cost')` | 直接读 | ✅ 直接 |
| `cost_model.rand_page_cost` | `current_setting('random_page_cost')` | 直接读 | ✅ 直接 |
| `cost_model.cpu_tuple_cost` | `current_setting('cpu_tuple_cost')` | 直接读 | ✅ 直接 |
| `cost_model.cache_hit_rate` | 无直接对应 | 需估算或人工填 | ⚠️ **可选/人工** |

## 3. 重大问题：corr_vs 跨列相关性

**问题**：DESIGN.md 6.2 节 YAML 写了 `user_id: {corr_vs: {col: created_at, rho: 0.82}}`，用于修正"独立假设在相关列上的高估"。但 PostgreSQL **不提供跨列相关性**：
- `pg_stats.correlation` 只是**单列**物理排序 vs 逻辑值的相关性（用于 index scan 代价估算）
- 跨列相关性要用 `CREATE STATISTICS (dependencies)` 创建扩展统计，存于 `pg_statistic_ext` / `pg_statistic_ext_data`，且只给"函数依赖"（不连 buff）和"ndistinct 系数"（不给 Pearson rho）

**对策（建议）**：
1. **降级为可选字段**：corr_vs 从 catalog 契约的"必需"降为"可选增强"。
2. **manual 优先**：DBA 在 YAML 手填（基于业务知识，如 `created_at` 与 `id` 强相关）。
3. **采样估算 fallback**：采集脚本可选采样计算 Pearson 相关系数（仅对小表或采样可行）。
4. **pg_statistic_ext dependencies 利用**：采集脚本读 `pg_statistic_ext` 的 dependencies（如果 DBA 创建了），转换为粗略的"列依赖关系"标记（不是 rho 数值，但是否相关的布尔提示）。
5. **保守策略兜底**：缺 corr_vs 时优化器走"独立假设 + 0.1 默认选择率"（已设计在 O1.S5）。

## 4. duckdb / sqlite 能采集到的字段

| 字段 | duckdb | sqlite |
|---|---|---|
| row_count | `PRAGMA storage_info` 或 `select count(*)` | `select count(*)` 或 DBSTAT |
| card | `PRAGMA stats`（column_stats） | 无，需采样 |
| mcv/hist | duckdb 不维护类似 MCV/直方图 | 无 |
| nulls | 采样 | 采样 |
| indexes | `duckdb_indexes()` | `sqlite_master` + `PRAGMA index_info` |
| cost_model | duckdb 无类似配置 | 无 |

**结论**：duckdb/sqlite 的 catalog 主要靠人工 + 采样，pg 是唯一可程序化采集的后端。

## 5. 修订 O0 / DESIGN.md 的具体建议

1. **DESIGN.md 6.2 节**：把 `corr_vs` 标注为"可选增强字段"，注明 pg 不直接提供、靠 manual 或采样。
2. **DESIGN.md 6.2 节**：`hist.kind: equi` 改为 `equi_freq`（等频），与 pg histogram_bounds 真实语义对齐。
3. **O0.S1**：ColumnStats 结构里 corr_vs 改为 `*CorrVs`（指针表可选）。
4. **O0.S2 置信度**：corr_vs 缺失时不降置信度（它本就可选）；mcv/hist 缺失才降。
5. **O0.S3 YAML 加载器**：unknown 字段警告而非报错（pg 采集脚本可能写额外字段如 `pg_oid`）。
6. **新增 O0.S6（可选）**：pg 采集脚本 `cmd/kql-collect-pg-stats/main.go`，从 pg 系统表生成 YAML（pg_analyze 来源），作为 manual 的辅助工具。
7. **新增 docs/stats-pg-mapping.md**：固化本表的字段映射，作为采集脚本规格说明。

## Sources

- [PostgreSQL pg_stats documentation](https://www.postgresql.org/docs/current/view-pg-stats.html)
- [pg_stats - pgPedia](https://pgpedia.info/p/pg_stats.html)
- [Hacking the Postgres Statistics Tables (Crunchy Data)](https://www.crunchydata.com/blog/hacking-the-postgres-statistics-tables-for-faster-queries)
- [Understanding statistics in PostgreSQL (AWS)](https://aws.amazon.com/blogs/database/understanding-statistics-in-postgresql/)
- [PostgreSQL ANALYZE and optimizer statistics (Cybertec)](https://www.cybertec-postgresql.com/en/postgresql-analyze-and-optimizer-statistics/)
- [The Postgres Planner and CREATE STATISTICS (Citus Data)](https://www.citusdata.com/blog/2018/03/06/postgres-planner-and-its-usage-of-statistics/)
