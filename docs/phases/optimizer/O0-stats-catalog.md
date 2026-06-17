# O0 — 统计描述数据结构与加载器

> 范围：`internal/optimizer/stats/`
> 依赖：无
> 阶段目标：定义 StatsCatalog Go 结构 + manual YAML 加载器
>
> **校验状态**：已完成对 PostgreSQL 系统表的字段映射校验（WebSearch 查证官方文档），详见 `O0-verification.md`。**重大修订**：`corr_vs` 跨列相关性 pg 不提供，从必需降为可选；`hist.kind` 改 `equi_freq`（pg 是等频非等宽）；新增可选 pg 采集脚本子目标。

## 顺序化子目标

### O0.S1 — 数据结构定义
- 产出：`stats/catalog.go`。
- **关键修订（校验补，corr_vs 降可选）**：
  - `ColumnStats.CorrVs` 字段类型用 `*CorrVs`（指针表可选），不是值类型——pg 不提供此数据，缺失是常态
  - `Hist.Kind` 枚举改为 `equi_freq`（对齐 pg histogram_bounds 真实语义：等频非等宽）；保留 `equi_width` 枚举值供未来扩展（采样估算可用）
  - `CostModel.CacheHitRate` 用 `*float64`（可选），pg 无直接对应
- 数据结构：Catalog{Schema, Version, Source, Tables, Views, CostModel} / Table{RowCount, AvgRowBytes, Columns, Indexes, Views} / ColumnStats{Card, Nulls, Type, MCV, Hist, CorrVs*} / IndexDef / ViewDef / CostModel
- 验收：能表达 DESIGN.md 6.2 节样例 YAML 的所有字段；缺失可选字段时结构有效。
- 测试来源：DESIGN.md 样例 + 手写 + 缺字段负例。

### O0.S2 — 置信度评分
- 产出：`stats/confidence.go`（Confidence(table, col) float，按 source 计算：manual=0.6, pg_analyze=0.9, sampling=0.7；缺失字段降低置信）。
- **重要修订（校验补）**：**corr_vs 缺失不降置信度**（它本就可选，pg 不提供），只有 mcv/hist/card/nulls 这类核心统计缺失才降。corr_vs 是"增强字段"，不是"基础字段"。
- 验收：manual + 缺 mcv/hist 的列置信 < 0.5；pg_analyze + 全基础字段 ≈ 0.9；**任何 source 缺 corr_vs 都不影响置信度**。
- 测试来源：手写。

### O0.S3 — YAML 加载器
- 产出：`stats/loader.go`（Load(path) → Catalog；缺字段走降级默认）。
- **修订（校验补）**：未知字段**警告而非报错**——pg 采集脚本可能写额外字段（如 `pg_oid`/`stats_target`），报错会阻碍程序化生成。
- 验收：缺失 mcv/hist/corr_vs 时不报错；版本/来源字段可读；未知字段进 warning。
- 测试来源：DESIGN.md 样例 + 缺字段负例。

### O0.S4 — 多后端 catalog 隔离
- 产出：按 backend 名加载（`stats/loader.go` 提供 LoadFor(backend, path)）；目录约定 `stats/<backend>/<schema>.yaml`。
- 验收：pg/duckdb/sqlite 三份 catalog 并存不互相覆盖。
- 测试来源：手写。

### O0.S5 — StatsReader 接口
- 产出：`stats/reader.go`（StatsReader 接口：Table(name)/Column(table,col)/Confidence/Indexes/CorrVs(table,col,otherCol) 等），供优化器只读访问。
- 验收：mock reader 可注入；真实 Catalog 实现 StatsReader；CorrVs 缺失时返回 nil（调用方按 nil 走独立假设）。
- 测试来源：手写。

### O0.S6 — pg 采集脚本（可选辅助工具）
- 产出：`cmd/kql-collect-pg-stats/main.go`（连 pg，从系统表采集生成 YAML，source 标 `pg_analyze`）。
- **字段映射**（详见 `docs/stats-pg-mapping.md`，校验固化）：
  - `row_count` ← `pg_class.reltuples`
  - `avg_row_bytes` ← `relpages * block_size / reltuples`
  - `card` ← `pg_stats.n_distinct`（正数直接，负数 `* reltuples`）
  - `nulls` ← `pg_stats.null_frac * reltuples`
  - `mcv` ← `pg_stats.most_common_vals` + `most_common_freqs`
  - `hist` ← `pg_stats.histogram_bounds`（标 `equi_freq`）
  - `corr_vs` ← **不采集**（pg 不提供）；可读 `pg_statistic_ext` dependencies 给"是否相关"布尔提示（无 ρ）
  - `indexes` ← `pg_index` + `pg_am` + `pg_attribute`
  - `indexes.include` ← `indnatts - indnkeyatts`（pg 11+）
  - `cost_model` ← `current_setting('seq_page_cost')` 等
- 验收：脚本对本地 pg 生成有效 YAML；source=pg_analyze 时置信度自动 0.9。
- 测试来源：本地 pg 实例 + DESIGN.md 样例对照。

### O0.S7 — pg 字段映射文档
- 产出：`docs/stats-pg-mapping.md`（固化 O0-verification.md 第 2 节的逐字段映射表 + 转换公式）。
- 验收：每个 YAML 字段都有对应的 pg 来源或标注"manual only"。
- 测试来源：O0-verification.md。

## 阶段产出物
- `internal/optimizer/stats/`（catalog/confidence/loader/reader）
- 示例 YAML + 加载测试

## 风险与对策
| 风险 | 对策 |
|---|---|
| YAML 字段漂移 | S3 严格 yaml 标签 + 未知字段报错 |
| 置信度公式主观 | S2 文档化 + 可调权重 |
| 多后端 catalog 维护成本 | S4 目录约定 + 未来 pg 采集脚本生成 |
