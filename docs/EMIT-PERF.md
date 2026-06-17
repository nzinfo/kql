# Emit 性能分析：当前问题与改进路线

## 问题诊断

### 当前 emit 策略：每 stage 一层嵌套子查询

```
events | where state == "TX" | extend d = damage*2 | summarize sum(d) by state | sort desc | take 5
```

生成：
```sql
SELECT * FROM                              -- take
  (SELECT * FROM                           -- sort
    (SELECT state, SUM(d) AS total FROM    -- summarize
      (SELECT *, damage*2 AS d FROM        -- extend
        (SELECT * FROM                     -- where
          (SELECT * FROM events) AS _k0
          WHERE state = $1) AS _k0
      ) AS _k0
      GROUP BY state) AS _k0
    ORDER BY total DESC) AS _k0
  LIMIT 5
```

**5 个 stage → 5 层嵌套**。问题：

1. **pg planner 无法完全展平**：虽然 pg 能 flatten 简单的 subquery pull-up，但
   带 GROUP BY / ORDER BY 的子查询会被当作物化屏障，生成不必要的 Materialize 节点。
2. **别名重复 `_k0`**：合法但让 explain 难读，也可能影响 planner 的等价类推导。
3. **我们做谓词下推，pg 也做**——双重下推可能产生冗余，但更大的问题是：
   我们的 optimizer 的下推能力（只穿 extend/project）远弱于 pg 原生 planner。
4. **真实查询 10-20 stage**（Sentinel 狩猎查询常见），嵌套 10-20 层——
   pg 的 plan 几乎必然退化。

### DESIGN 的设计意图 vs 当前实现

DESIGN 说：
> **"能合就合"**：相邻能进单 SELECT 的算子合并；遇到 summarize/join/窗口算子，
> **才**断开为 CTE 或子查询。

当前实现：**完全没合并**。每个 stage 都断。这是从 e2e 最小闭环时的"先跑通再优化"
遗留的债务——正确性优先、性能待优化的状态。

## 改进路线

### 方案 A：Stage 合并（O2 新规则 / emit 层重构）—— 推荐

把 `where + extend + project` 这类不改变行集语义的相邻 stage 合并进**单 SELECT**：
```sql
SELECT state, damage*2 AS d
FROM events
WHERE state = $1
```
只在 `summarize`(GROUP BY) / `join` / `distinct` / 窗口算子处断开为 CTE 或子查询。

**实现位置**：emit 层（不是 IR rewrite）—— emit 时分析 stage 序列，按"断点"分组。
或：O2 新规则 MergeStages（把 where+extend 合并成一个 Project{exprs+predicate}）。

**预期收益**：10-stage 查询从 10 层子查询 → 2-3 个 CTE（只在聚合/join 处断）。

### 方案 B：CTE 替代嵌套子查询

把 `WITH t1 AS (...), t2 AS (...)` 替代 `SELECT * FROM (SELECT * FROM (...))`。
pg 对 CTE 的 inline 优化（pg12+ 默认 `NOT MATERIALIZED`）比嵌套子查询更友好。
但 CTE 本身不解决层数问题——只是换了表达方式。

### 方案 C：依赖 pg planner 展平

什么都不做，信任 pg 的 subquery pull-up。**不可靠**——pg 的展平能力有限，
尤其对带 GROUP BY / window 的子查询。不能依赖。

### 推荐：A + B 组合

1. emit 层按"断点"(aggregate/join/distinct) 分组，每组生成一个 CTE 节点。
2. 组内的 where/extend/project/sort/limit 合并进单 SELECT。
3. 最终 SQL 形如：
```sql
WITH s0 AS (
  SELECT state, damage*2 AS d FROM events WHERE state = $1
), s1 AS (
  SELECT state, SUM(d) AS total FROM s0 GROUP BY state
)
SELECT * FROM s1 ORDER BY total DESC LIMIT 5
```

这比当前方案少很多层，pg planner 也更容易优化。

## 优先级

**高**——这是生产可用性的核心障碍。当前方案对简单查询够用（3-5 stage），
但真实 Sentinel 查询（10-20 stage）在 pg 上几乎必然产生次优 plan。

### 验收基准
- `EXPLAIN` 对比：合并前 vs 合并后的 plan 层数 + 估计成本。
- `O5 benchmark`：emit 时间（合并逻辑增加 emit 开销，需控制在可接受范围）。
- 跨后端等价性不变（sqlite/duckdb/pg 结果仍一致）。

## 实测结论（2026-06-18）

用 pg 16 的 `EXPLAIN` 对比验证：

### 测试 1：5 stage（where + extend + summarize + sort + take）
**结果：pg planner 完全展平**。嵌套 5 层 vs 合并 1 层生成**完全相同的 plan**
（Seq Scan → GroupAggregate → Sort → Limit）。cost 一致。

### 测试 2：10 stage（where + 3×extend + where + extend + project + sort + take）
**结果：pg planner 仍然完全展平**。两个 plan 结构一致
（Bitmap Heap Scan → Sort → Limit），cost 差异仅 ~7%（25.19 vs 23.44）。

### 结论

pg 的 subquery pull-up 能力**比预期强得多**：
- 纯 Filter/Project/Extend 嵌套 → pg **总能展平**（无论层数）
- GROUP BY/JOIN 处自然断开 → 这正是我们的 emit 已经做的
- 主要损耗在 **planning time**（嵌套多时 planner 花更长时间展平）而非执行

### 改进优先级调整

原以为 stage 合并是高优先级。实测后调整为**中低优先级**：
1. 当前方案在 pg 上**执行性能基本无损**（planner 展平了）
2. 损耗主要在 planning time（~0.35ms → 可接受）
3. 真正的风险在 **edge case**（极深嵌套 + 复杂表达式可能让 planner 放弃展平）
4. 别名重复 `_k0` 可能让 explain 难读，但不影响 plan 质量

### 仍值得做的改进（按 ROI）

1. **唯一别名**（`_k0` → `_s0/_s1/...`）—— 让 EXPLAIN 可读、避免潜在 planner edge case
2. **stage 合并 emit**（把 where+extend+project 合进单 SELECT）—— 减少嵌套层、降低 planning time
3. **CTE 替代嵌套**—— 对 pg 12+ 的 NOT MATERIALIZED CTE，planner 更友好
