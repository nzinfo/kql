# KQL 解析与执行器 — 设计文档

> 状态：草稿 v1（2026-06-17）
> 作者：nzinfo
> 模块路径：`nzinfo/kql`

## 1. 目标

自研一套 Kusto Query Language (KQL) 的解析器与执行器，面向：

- **CLI**：`kql -d <dsn> -f csv/parquet 'KQL...'`
- **嵌入式库**：`kql.Exec(ctx, dsn, query, params) → Arrow`

覆盖范围为 **MVP 子集**（~15 个核心算子 + 常用标量函数），可增量扩展到接近完整 KQL。

## 2. 技术栈决策

| 维度 | 选择 | 理由 |
|---|---|---|
| 实现语言 | **Go** | cgo 接 sqlite/duckdb；pg 走 pgx 纯 Go 驱动 |
| 前端路线 | **手写递归下降**（仿 kqlparser） | 无 ANTLR 运行时依赖，可控 |
| 语法权威 | 官方 `Kusto-Query-Language/grammar/Kql.g4` | 唯一基准 |
| 执行路线 | **统一 IR + 多方言后端** | 一套 KQL，三后端可切换 |
| 翻译策略 | **最终执行性能最大化** | 能合进单 SELECT 就合；难表达的算子才断开为 CTE/UDF |
| 主后端 | **PostgreSQL** | sqlite/duckdb 为辅 |
| UDF 策略 | 必要时才引入（pg 走 plpgsql） | 避免污染 schema |
| 优化器 | **基于预定义统计描述的代价感知**，可切换决策策略 | 跨后端统一、可版本管理、不依赖运行时查系统表 |

## 3. 总体分层

```
┌─────────────────────────────────────────────────────────┐
│  外壳层  (cmd/kql + pkg/api)                              │
│   ├─ CLI    : kql -d <dsn> -f csv/parquet 'KQL...'      │
│   └─ Library: kql.Exec(ctx, dsn, query, params) → Arrow │
├─────────────────────────────────────────────────────────┤
│  前端 (internal/frontend/)   手写，无 ANTLR 依赖           │
│   token → lexer → parser → AST → binder                  │
├─────────────────────────────────────────────────────────┤
│  IR (internal/ir/)  方言无关关系代数 IR                    │
│   Pipeline : Source + []Stage                            │
│   Stage   : Filter | Project | Extend | Aggregate |       │
│             Join | Sort | Limit | Union | Let …          │
├─────────────────────────────────────────────────────────┤
│  优化器 (internal/optimizer/)  两段式                     │
│   rules/   : IR→IR 规则重写 (方言无关)                    │
│   stats/   : 预定义统计描述 (外部输入)                    │
│   cost/    : 代价模型 + 选择率估算                        │
│   decision/: 可替换决策策略 (保守/激进/置信度网关)         │
├─────────────────────────────────────────────────────────┤
│  后端 (internal/backend/)  IR → 方言最优 SQL/Plan         │
│   ├─ pg/      (主) pgx + 生 SQL, UDF 走 plpgsql          │
│   ├─ duckdb/  (辅) duckdb-go, 列式友好                   │
│   └─ sqlite/  (辅) mattn/go-sqlite3, 单机零依赖          │
├─────────────────────────────────────────────────────────┤
│  执行代理 (internal/exec/)  接 driver.DB, 拉回 Arrow      │
└─────────────────────────────────────────────────────────┘
```

`internal/` 锁死，对外只暴露 `pkg/kql`——CLI 和库共享同一入口。

## 4. 前端（internal/frontend/）

```
frontend/
  token/token.go        # 词法 token + 位置(用于诊断)
  lexer/lexer.go        # 手写 tokenizer
  parser/parser.go      # 递归下降 + 少量回溯(KQL 算子前缀歧义)
  ast/                  # node.go / expr.go / operator.go / stmt.go
  binder/               # 符号 + schema 流 + 类型推断 + 严格模式
  diagnostic/           # 带 code 的错误(KQL000+)
  builtin/              # 标量/聚合函数清单(从 kqlparser/builtin 复用 380+ 清单)
```

**取舍**：
- 不抄 `kql-parser` 那套 ANTLR——多一层 parse tree→AST，且绑定 ANTLR 运行时。
- 直接复用官方 `grammar/Kql.g4` 作为语法权威，对照实现。
- 测试语料：搬 `kql-parser/fuzz_corpus_test.go` 和 `large_corpus_test.go` 做前端回归。

## 5. IR（internal/ir/）

```go
type Pipeline struct { Source Source; Stages []Stage }
type Stage interface{ stage() }
// 每个 Stage 持有 Expr 树(不是 AST 节点——是简化后、类型已推算的 Expr)
type Expr interface{ expr() }   // Lit/Col/BinOp/FuncCall/Agg…
```

**IR 的定位（重要）**：
- **IR 是内部中间表示，不是运行时产物。** 查询的运行时输出永远是后端生成的 **SQL**。
- IR 的可读表示（pretty-print / YAML dump）**仅用于 `kql explain` 与测试 golden 快照**，不进入核心执行路径。
- 构建/运行时产 SQL 时可不依赖 IR 序列化代码；`pkg/kql` 公开 API 返回 `arrow.Record` 或 `*ExplainOutput`（含 IR 文本+SQL+代价），不直接返回 IR 对象。

设计要点：
- IR 必须接近 SQL 关系代数，否则翻译时仍要做重活。
- `FuncCall` 标注**能力位**：`CanFoldToSQL` / `NeedsUDF` / `NeedsPostProcess`。后端按能力位选翻译路径。
- 列引用绑定到**物理列 ID**（绑定器产物），避免方言大小写/引号差异。

## 6. 优化器（internal/optimizer/）

### 6.1 定位

**两段式**：
1. **规则重写**（rule-based）：方言无关的 IR→IR 变换，所有后端共享。
2. **代价选择**（cost-based）：读预定义统计描述，从多个等价物理方案中选最优，并主动控制执行策略。

两部分用清晰接口隔开，决策策略做成可替换策略对象——满足"架构允许切换决策策略"。

### 6.2 统计描述契约（Stats Catalog）

外部输入（DBA/部署脚本/采集脚本提供），优化器只读不写。版本化 YAML/JSON，执行前可热加载。

```yaml
schema: erp
version: 2026-06-17T10:00:00Z
source: manual | pg_analyze | sampling    # 标识来源,影响置信度

tables:
  orders:
    row_count: 48000000
    avg_row_bytes: 128
    columns:
      id:        {card: 48000000, nulls: 0, type: bigint}
      status:    {card: 8, nulls: 0, mcv: [["paid",0.42],["shipped",0.31]]}         # 最常用值+频率
      created_at:{card: 40000000, nulls: 0, hist: {kind: equi_freq, buckets: [...]}}    # 等频直方图(可选)
      user_id:   {card: 9000000, nulls: 120000, corr_vs: {col: created_at, rho: 0.82}}  # ⚠️ 可选:跨列相关性,pg 不提供,需 manual/采样
    indexes:
      - name: orders_pkey
        cols: [id]
        kind: btree
        unique: true
      - name: idx_orders_status_created
        cols: [status, created_at]
        kind: btree
        include: [user_id, total]            # covering index
    views:
      - name: orders_daily_summary
        replaces: "summarize count() by bin(created_at,1d)"

views:                                       # 全局物化视图
  - name: user_order_stats_mv
    refresh: incremental

cost_model:                                  # 估算代价常量(可不提供,用默认值)
  seq_page_cost: 1.0
  rand_page_cost: 4.0
  cpu_tuple_cost: 0.01
  cache_hit_rate: 0.3                        # ⚠️ 可选:pg 无对应,需估算或人工
```

**契约要点**：
- `mcv`/`hist`/`corr_vs` 全部**可选**——缺哪个优化器就用降级策略，保证可用。
- `version` + `source` → **置信度评分**：`manual`=0.6、`pg_analyze`=0.9、`sampling`=0.7。低于阈值的决策回退保守路径。
- 每个引擎后端单独一份 catalog（pg/duckdb/sqlite 各自）。
- **重要：corr_vs 与 cache_hit_rate 的来源限制**（校验补，详见 `docs/phases/optimizer/O0-verification.md`）：
  - `corr_vs`（跨列相关性 Pearson ρ）：**PostgreSQL 不直接提供**。pg 只有单列 `pg_stats.correlation`（物理排序 vs 逻辑值），跨列相关性要用 `CREATE STATISTICS (dependencies)` 创建扩展统计且只给函数依赖布尔提示（无 ρ）。对策：manual 优先（DBA 手填）/ 采集脚本读 `pg_statistic_ext` 给"是否相关"提示 / 缺失时优化器走独立假设（已设计在 O1.S5）。
  - `hist.kind`：pg 的 `histogram_bounds` 是**等频直方图**（每桶约等行数），不是等宽。YAML 用 `equi_freq` 标注以对齐真实语义。
  - `cache_hit_rate`：pg 无直接对应，需估算（基于 buffer cache 统计）或人工填。
  - 字段映射速查见 `docs/stats-pg-mapping.md`（O0.S6 产出）。

### 6.3 代价模型

不抄 pg 内部代价（也拿不到），自己定义**可解释**代价：

```go
type Cost struct {
    IO  float64   // 顺序/随机页 * row_count 选择率
    CPU float64   // tuple 处理 * 行数
    Net float64   // 仅 pg: 拉回客户端的字节
    Mem float64   // 排序/哈希表占用
}
func (c Cost) Total(w CostWeights) float64 { ... }
```

**选择率估算**（核心，决定 where/join 代价）：

| 谓词 | 选择率公式 | 数据来源 |
|---|---|---|
| `col = const` 且 const ∈ MCV | 该 MCV 频率 | `mcv` |
| `col = const` 不在 MCV | `1/card` | `card` |
| `col < const` | 直方图分位 | `hist` |
| `col in (...)` | Σ 单值选择率 | `mcv`/`card` |
| `col is null` | `nulls/row_count` | `nulls` |
| `t1.a = t2.a`（join） | `1/max(card_a, card_b)` | 双方 card |
| 复合谓词 AND | `s1 * s2`（独立假设，可被 corr 修正） | `corr_vs` |
| 无任何统计 | `0.1`（保守默认） | — |

`corr_vs` 用于修正"独立假设"在相关列上的高估（典型：`created_at` 与 `id` 强相关）。

### 6.4 优化器结构

```go
// 1) 规则重写阶段: 方言无关 IR→IR
type RewriteRule interface {
    Apply(p *ir.Pipeline, stats StatsReader) (*ir.Pipeline, bool)
}
// 内置 rules (按依赖顺序):
//   PredicatePushdown / ColumnPrune / ConstantFold
//   AggregateFold (summarize|summarize 合并)
//   LimitPushdown / DistinctElim
//   ViewMatch (匹配 stats.views, 改写 source)
//   CorrAwarePredicateJoin (用 corr_vs 修正独立假设)

// 2) 物理方案枚举: 为每个 IR 节点生成多个等价 AltPlan
type AltPlan interface {
    Cost(stats StatsReader, cm CostModel) Cost
    Emit(backend.Dialect) backend.PhysicalStep
}
// 例: JOIN 节点 → [HashJoin, MergeJoin, NestedLoop, IndexedLookup]
//    summarize → [GroupAggregate, HashAggregate, TwoStage]

// 3) 决策策略: 可替换的核心抽象
type DecisionPolicy interface {
    Choose(alts []AltPlan, stats StatsReader) AltPlan
}
// 实现:
//   ConservativePolicy  (默认): 不确定就回退交给目标引擎
//   AggressivePolicy    : 总选最低估算代价
//   ConfidenceGatedPolicy: 低于阈值的决策回退保守路径
//   ExplainFeedbackPolicy: 接收 EXPLAIN ANALYZE 反馈迭代(未来)
```

**触发流程**：

```
AST → IR → [RewriteRules] → IR'
              ↓
    StatsReader (读 catalog, 标置信度)
              ↓
    PhysicalPlanner (枚举 AltPlan)
              ↓
    DecisionPolicy.Choose (可热替换)
              ↓
    PhysicalPlan → backend.Emit → 方言 SQL/多阶段执行
```

### 6.5 主动控制执行策略

相对"只塑形 SQL"的关键增量，IR 节点带 `ExecuteStrategy` 字段：

1. **Join 算法提示**：pg hint 或 inner side 小时强制 broadcast。
2. **CTE 物化与否**：pg 14+ `WITH ... AS NOT MATERIALIZED`。
3. **两阶段聚合**：大表 `summarize` 先按分片局部聚合再合并（pg 自己不会做）。
4. **样本预筛 + 回查**：极选择性 `where` + 大 `take`，先拉匹配 rowid 集回引擎，再 `WHERE id = ANY(...)` 拉明细。
5. **物化视图改写**：`stats.views` 命中时直接换 source。
6. **Index-only scan 触发**：covering index 字段集 ≥ 所需列时强制 `INCLUDE` 索引。
7. **Split 半边查询**：`join` 一边极小（< 阈值）→ 客户端 nested loop（IN 列表批量查）。

策略对象可选启用/禁用这些动作，实现"保守=全交 pg、激进=主动接管"的切换。

### 6.6 降级与稳健性

- StatsReader 提供 `Confidence(table, col) → float`，规则和 AltPlan 计算时都问一下。
- 默认 `Conservative` 策略：关键统计缺失或置信度 < 0.5 时，**不生成激进 AltPlan**，直接走"最像 pg 自己会做的"那条。
- 所有 `AltPlan` 必须能**回退为一条可执行 SQL**——保证即便决策错也不会算错结果，只是慢。
- 优化器日志输出 `Explain`：每个决策附 `reason`（哪条统计、什么选择率、为什么选这条）——便于回看负优化案例。

### 6.7 与多后端的接口

```go
type StatsLoader interface {
    Load(schema string) (StatsCatalog, error)
}
// pg:     可选从 pg_stats/pg_class 自动采(作为 manual 的辅助)
// duckdb: 从 PRAGMA stats / INFORMATION_SCHEMA
// sqlite: 基本只有 row_count, 索引信息全; 分布需要采样

type CostWeights struct { IO, CPU, Net, Mem float64 }
// pg:     Net 较低(协议批量), Mem 较高(work_mem 限制)
// duckdb: IO 低(列式压缩), CPU 高(向量化)
// sqlite: 单机无 Net, IO 随机页代价高
```

## 7. 后端（internal/backend/）

IR → 方言最优 SQL。pg（主）翻译目标：**一条扁平或最少嵌套的 SELECT**，让 pg 优化器跑满。

| TabularOp 顺序 | pg 生成策略 |
|---|---|
| Source(tbl) | `FROM tbl` (或 `FROM (subquery)`) |
| where e | `WHERE e`（折叠到同一 SELECT） |
| project [c1,c2] | `SELECT c1, c2`（列裁剪已先做） |
| extend x = f(..) | `x AS f(..)`（作为 select 项） |
| summarize agg by k | `GROUP BY k` + 聚合列 |
| join kind=t | `JOIN/LEFT/INNER` |
| order by | `ORDER BY`（take 上推时配 `LIMIT`） |
| take N | `LIMIT N` |

**"能合就合"**：相邻能进单 SELECT 的算子合并；遇到 `summarize`/`join`/窗口算子，**才**断开为 CTE 或子查询。

**UDF 策略**（必要时才引入）：
- pg：`percentile`/`series_*`/自定义聚合 → 生成临时 plpgsql 函数或预装函数。
- duckdb：尽量用其内建聚合（比 pg 还全），缺的写 UDF。
- sqlite：能力受限，部分算子**降级到客户端 post-process**（IR 标记 `NeedsPostProcess`，拉回行集后在 Go 内算）。

## 8. 执行代理（internal/exec/）

- 统一接口 `Backend.Query(ctx, sql, args, schema) → arrow.Record`。
- pg：`pgx` + 批量拉取，按 schema 转 Arrow。
- duckdb：`duckdb-go` 原生 Arrow 输出（**最快路径，零拷贝**）。
- sqlite：行式拉取后在 Go 内转 Arrow（不分析友好，仅 MVP/嵌入演示用）。

## 9. 模块边界

```
kql/
  go.mod                       # module nzinfo/kql
  cmd/kql/main.go              # CLI
  internal/
    frontend/{token,lexer,parser,ast,binder,diagnostic,builtin}
    ir/
    optimizer/{stats,rules,cost,decision}
    backend/{pg,duckdb,sqlite}
    exec/
  pkg/kql.go                   # 公开 API: Exec/Explain/Validate
  stats/                       # 预定义统计描述 YAML
  testdata/corpus/             # 测试语料 (从 kql-parser 抽取)
```

## 10. MVP 算子优先级

| 优先级 | 算子/特性 | 翻译目标 |
|---|---|---|
| P0 | `where / project / take / order by / sort` | 单 SELECT |
| P0 | `extend` | SELECT 项 |
| P0 | `summarize ... by`（count/sum/avg/min/max） | GROUP BY |
| P0 | `join`（inner/left） | JOIN |
| P1 | `let` + 管道引用 | CTE |
| P1 | `union` | UNION ALL |
| P1 | `distinct` | DISTINCT |
| P2 | `mv-expand` / 窗口函数 | UNNEST / 窗口 + 可能 UDF |
| P2 | `evaluate`/插件 | 客户端 post-process |

## 11. 取舍说明

1. **为什么不仿 rust-kql 直接生成 DataFusion plan？** 主后端是 pg——pg 没有列式 plan API，必须走 SQL 文本。所以 IR 设计成"易生 SQL"。
2. **为什么 IR 在优化器层做重写而不是各后端做？** 三后端共享同一组 pass，避免三份代码；后端只管"把 IR 翻成方言最优文本"。
3. **为什么不直接 AST→SQL？** 要"最终性能最大化"，必须做谓词下推/列裁剪/聚合折叠——这些重写在 AST 上做极痛苦。IR 不可省。
4. **UDF 何时引入？** 仅当某函数/算子在目标引擎无法用纯 SQL 表达（pg 的 percentile、series_*、自定义聚合）。
5. **为什么用 YAML 而不是运行时查系统表？** "预定义为主"。YAML 让 DBA 显式声明、可版本管理、跨环境一致；pg_stats 采集脚本可作为生成 YAML 的工具，不进入运行时热路径。
6. **保守优先的实现要点**：保守策略不是"不做优化"，而是"只做 pg 一定会做或不会更差的优化"（谓词下推、列裁剪），代价敏感的决策（join 重排、两阶段）才交还 pg。

## 12. 参考项目价值速查

| 子项目 | 价值点 |
|---|---|
| `Kusto-Query-Language/grammar/Kql.g4` | 语法权威基准 |
| `Kusto-Query-Language/src/Kusto.Language/Binder/` | 绑定器工程量参考 |
| `kql-parser/fuzz_corpus_test.go` + `large_corpus_test.go` | 真实 Sentinel 语料回归集 |
| `kql-parser/extractor.go` | parse tree → 结构化提取模式参考 |
| `kqlparser/`（Go 手写） | 前端分层直接范本（lexer/parser/ast/binder/types/diagnostic） |
| `kqlparser/builtin/functions.go` | 380+ 内建函数清单可复用 |
| `rust-kql/datafusion-kql/planner.rs` | AST → 执行 plan 翻译思路（即便我们走 SQL 而非 DataFusion） |
