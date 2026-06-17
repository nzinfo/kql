# UDF vs SQL-inline vs PostProc — 计算下推策略分析

> 回答"哪些计算下推为 UDF 更好"的系统性分析。
> 三条路径：SQL inline（emit 成表达式）、UDF（后端预装函数）、PostProc（客户端 Go 计算）。

## 决策矩阵

### 高频函数（语料出现 ≥5 次）

| 函数 | 频次 | 当前路径 | pg 原生 | sqlite 原生 | **最优路径** | 理由 |
|---|---|---|---|---|---|---|
| `split` | 12 | NeedsPostProc | `regexp_split_to_array` | ❌ | **pg: SQL inline** / sqlite: PostProc | pg 有原生，改 emit 即可 |
| `extract` | 7 | NeedsPostProc | `regexp_match` | 需扩展 | **pg: SQL inline** / sqlite: PostProc | pg 有原生 regexp |
| `make_set` | 14 | group_concat+PostProc | `array_agg(DISTINCT)` | `group_concat(DISTINCT)` | **SQL inline**（去掉 PostProc 标记） | 两后端都有原生聚合 |
| `make_list` | 6 | group_concat+PostProc | `array_agg` | `group_concat` | **SQL inline** | 同上 |
| `array_length` | 4 | `json_array_length` | `array_length(arr,1)` | `json_array_length` | **SQL inline**（已是） | 正确 |
| `countif` | 6 | `SUM(CASE WHEN...)` | `COUNT(*) FILTER` (pg14+) | `SUM(CASE WHEN...)` | **SQL inline**（当前对；pg14+ 可优化 FILTER） | 已正确 |
| `dcount` | 19 | `COUNT(DISTINCT)` | `COUNT(DISTINCT)` 或 HLL | `COUNT(DISTINCT)` | **SQL inline**（已是） | 精确 dcount 用 COUNT(DISTINCT) 够；近似 dcount 需 UDF(HLL) |
| `case` | 4 | `CASE`（inline） | `CASE` | `CASE` | **SQL inline** | 已正确 |

### 真正需要 UDF 或 PostProc 的

| 函数/算子 | pg 原生? | sqlite 原生? | **最优路径** | 理由 |
|---|---|---|---|---|
| `mv-expand` | `UNNEST` / `json_array_elements` | ❌ | **pg: SQL inline(UNNEST)** / sqlite: PostProc 或 UDF | 行展开是核心算子；pg 可 inline，sqlite 需 PostProc |
| `parse` (regex→列) | `regexp_match` in SELECT | 需扩展 | **pg: SQL inline** / sqlite: PostProc | pg 原生 regexp 够用 |
| `make-series` | ❌ | ❌ | **UDF 或 PostProc** | 无 SQL 等价；时间序列 bucketing + 缺值填充 |
| `series_decompose_anomalies` | ❌ | ❌ | **UDF 或 PostProc** | 纯数学计算，无 SQL 等价 |
| `base64_encode/decode` | ❌ | ❌ | **UDF(plpgsql)** 或 PostProc | 简单但无 SQL 等价 |
| `hash` (KQL 语义) | ❌ (pg hash 不同) | ❌ | **UDF** | KQL 的 hash 算法 ≠ pg 的 md5/sha |
| `bag_unpack` | `jsonb_each` | `json_each` | **SQL inline** (lateral join) | 两后端都有 JSON 展开 |
| `parse_json` | `::jsonb` | `json()` | **SQL inline** | 类型转换 |

## 结论

### 不需要 UDF 的（当前或改进后直接 SQL inline）

绝大多数高频函数（split/extract/make_set/make_list/dcount/countif/case）——pg 有原生等价，
改进 emit 模板即可（去掉 NeedsPostProc 标记 + 加 pg 模板）。

### 需要 UDF 的（极少数）

只有 **KQL 语义 ≠ SQL 原生** 的函数：
1. **`hash()`** — KQL 用自己的 hash 算法（非 md5/sha），需 UDF 保证语义一致
2. **`make-series` + `series_*`** — 时序分析无 SQL 等价，需 UDF（plpgsql 过程式）或 PostProc
3. **`base64_encode/decode`** — pg 无原生（但 Go 标准库有，PostProc 更简单）

### 需要 PostProc（客户端 Go 计算）的

1. **`mv-expand`（sqlite）** — sqlite 无 UNNEST，必须客户端展开
2. **`make-series` + `series_*`** — 如果不走 UDF 路线
3. **`base64/hash`** — 如果不值得装 UDF

### 推荐改进优先级

1. **去掉 make_set/make_list/split/extract 的 NeedsPostProc**，给 pg 加原生模板
   → **最大收益**：语料里这 4 个函数共 ~40 次出现，目前全走 PostProc（或什么都不做）
2. **pg mv-expand → UNNEST inline**：pg 有 `json_array_elements`，可以直接 emit
3. **sqlite mv-expand → PostProc**（exec.mvExpandRows 已实现，需接线）
4. UDF 方案留到有真实 hash/series 需求时
