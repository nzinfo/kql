# 阶段拆解总览（跨线汇总）

> 6 条线各自的阶段拆解见子目录 `frontend/` / `ir/` / `optimizer/` / `backend/` / `shell/` / `test-corpus/`，每线有 `README.md` 总览 + 每阶段一个 `<phase-id>.md` 文件。
> 本文档做跨线依赖关系、启动顺序、统一测试来源、统一验收口径的汇总。

## 1. 六条线一览

| 线 | 阶段数 | 文档 | 核心交付 |
|---|---|---|---|
| 前端 | F1–F7 | frontend.md | token→lexer→parser→ast→binder→diagnostic→builtin |
| IR | I1–I5 | ir.md | Pipeline/Stage/Expr + AST→IR 翻译 + 能力位 + pretty + 等价测试 |
| 优化器 | O0–O6 | optimizer.md | StatsCatalog + 选择率/代价 + 规则重写 + 决策策略 + Join altplan + Explain |
| 后端 | B1–B7 | backend.md | Backend 接口 + pg/duckdb/sqlite emit + UDF + 三方言快照 |
| 外壳 | S1–S6 | shell.md | pkg/kql API + exec Backend + pg 接线 + duckdb/sqlite + CLI + mock + e2e |
| 测试语料 | T1–T6 | test-corpus.md | 调研格式 + 抽取分类 + P0 回归集 + golden + fuzz + e2e |

## 0. 全局重要约定：IR 不是产物，SQL 才是产物

- **运行时输出**：后端从 PhysicalPlan 生成的 **SQL**，发给 pg/duckdb/sqlite 执行，返回 `arrow.Record`。
- **IR 是内部中间表示**：不作为查询的最终输出；其可读表示（pretty-print / YAML dump）**仅用于 `kql explain` 与测试 golden 快照**。
- **构建/运行时不依赖 IR 序列化即可产出 SQL**：IR 的 YAML 序列化器是调试工具，可按需启停；核心执行路径 `frontend → ir → optimizer → backend → SQL` 中 IR 始终是内存对象。
- **公开 API**：`pkg/kql` 返回 `arrow.Record`（结果）或 `*ExplainOutput`（含 IR 文本+SQL+代价），不直接返回 IR 对象。

## 2. 跨线依赖关系（关键路径）

```
T1 ──┬─> T2 ──> T3 ──┬─> T4
     │              │
F1 ──> F2 ──> F3 ──> F4 ─────────┐
                       │          │
F7 ────────────────────┤          │
                       v          │
                       F5 <───────┤
                       │          │
I1 <─ F2               │          │
I2 <─ F4,F5,F7         │          │
I3 <─ I2               │          │
I4 <─ I1               │          │
I5 <─ I2,T3,T4         │          │
                       │          │
O0                     │          │
O1 <─ O0               │          │
O2 <─ I1,O0,I5         │          │
O3 <─ O1,O2            │          │
O4 <─ O3               │          │
O5 <─ O2,O3,O4,T3      │          │
                       │          │
B1 <─ I1,O3            │          │
B2 <─ B1,S2            │          │
B3 <─ B2,O4            │          │
B4 <─ B1,S2            │          │
B5 <─ B1,S2            │          │
B6 <─ B2,B3,B4,B5      │          │
B7 <─ B2,B4,B5,T3,T4   │          │
                       │          │
S1                     │          │
S2 <─ S1               │          │
S3 <─ S1,S2,B2,F4,I2,O3 <─────────┘
S4 <─ S3,B4,B5
S5 <─ S3,S4,O3,O5
S6 <─ S3,T3,T4,B7

O6(可选) <─ O2,O5
T6(后置) <─ S3,S4,S6,B7,T3
```

## 3. 推荐启动顺序（首批可并行）

**Wave A（基础设施，可并行启动）**：
- F1 词法层
- F2 AST 骨架（仅接口）
- F7 builtin 函数清单（独立，可纯数据工作）
- I1 IR 核心数据结构（依赖 F2 接口形态）
- O0 StatsCatalog 结构 + YAML 加载器
- S1 pkg/kql API 骨架（接口先行）
- T1 语料调研 + 格式确定

**Wave B（解析主干）**：
- F3 parser 表达式（依赖 F1,F2）
- T2 语料抽取分类（依赖 T1）

**Wave C（解析完成 + 翻译开始）**：
- F4 parser tabular P0（依赖 F3）→ F5 binder（依赖 F4,F7）
- I2 AST→IR 翻译（依赖 F4,F5,F7,I1）
- T3 P0 最小回归集（依赖 T2）

**Wave D（优化器 + 后端起步）**：
- O1 选择率/代价（依赖 O0）
- O2 三条规则（依赖 I1,I5,O0）→ O3 决策策略（依赖 O1,O2）
- S2 exec Backend（依赖 S1）
- T4 golden 机制（依赖 T3）

**Wave E（首个端到端）**：
- B1 Backend 接口 → B2 pg P0（依赖 B1,S2）
- S3 pg 接线（依赖 S1,S2,B2,F4,I2,O3）← **首次端到端可跑**
- I3 能力位 / I4 pretty / I5 等价测试（并行打磨）
- O4 Join altplan（依赖 O3）

**Wave F（多后端 + 完善）**：
- B3 pg CTE/join（依赖 B2,O4）
- B4 duckdb / B5 sqlite（依赖 B1,S2）
- S4 duckdb/sqlite 接线（依赖 S3,B4,B5）
- T5 大语料 fuzz（依赖 F4,T2）

**Wave G（收尾）**：
- B6 UDF / B7 三后端快照
- S5 CLI 全功能 + Explain
- O5 代价基准 / O6（可选）增强规则
- S6 mock + e2e + CI
- T6（后置）跨后端 e2e

## 4. 统一测试集来源（所有线共用）

| 测试集 | 来源 | 路径 | 用途 |
|---|---|---|---|
| **手写单元用例** | 自行构造，每条覆盖单一行为 | 各包 `*_test.go` | 边界、错误路径、单元正确性 |
| **P0 算子回归集** | 手写 + 从 kql-parser 抽取脱敏 | `testdata/corpus/p0/*.yaml` | F4/F5/I2/B2 等所有 P0 阶段的功能验收基础集 |
| **Sentinel 真实语料** | kql-parser `fuzz_corpus_test.go` + `large_corpus_test.go`（MIT，NOTICE 注明） | `testdata/corpus/sentinel/*.yaml` | T5 fuzz 压力测试（不验证结果，只验证不 panic + 算子识别） |
| **官方 grammar 示例** | `Kusto-Query-Language/grammar/Kql.g4` 注释里的例子 | `testdata/corpus/official/*.yaml` | 语法权威性回归（与官方对齐） |
| **golden file 快照** | T3/T4 生成，CI `go test -update` 刷新 | `testdata/corpus/<set>/*.golden.{ast,ir,sql.pg,sql.duckdb,sql.sqlite}` | F4 AST / I2 IR / B7 三方言 SQL 防漂移（**测试/调试用，非产物**） |
| **stats catalog 示例** | 手写（DESIGN.md 6.2 节样例）+ 未来 pg 采集脚本生成 | `stats/*.yaml` | O0/O1/O2/O4/O5 代价感知测试输入 |
| **mock dataset** | 手写小规模固定数据集（CSV/JSON） | `testdata/mock/<set>/` | S6/T6 跨后端等价性验证（无需真实 pg） |
| **真实 pg（可选）** | 本地/CI pg 实例 + 种子数据 | 通过环境变量 `KQL_TEST_PG_DSN` 启用 | S3/B2/B3 pg 集成测试（CI 中可选 job） |

**许可证合规**：仅抽取 MIT/Apache 项目语料；NOTICE 文件注明 kql-parser（MIT）来源；Sentinel 真实查询脱敏（表名/字段替换为 `T1`/`col_a`）。

## 5. 统一功能验收口径

每个阶段的"验收标准"必须满足以下口径之一（不可含糊）：

### 5.1 解析正确性验收（前端 F1–F4）
- **基线**：对 T3 P0 回归集 100% 解析成功（产出 AST，不 panic）。
- **错误处理**：错误输入产出带 code 的 Diagnostic（KQL000+），不 panic。
- **golden**：AST 快照对比通过（`go test`，非 `-update` 模式）。

### 5.2 绑定正确性验收（F5）
- 列引用 100% 绑定到物理列 ID（缺失时报 KQL001）。
- 类型推断对 P0 高频模式（算术/比较/聚合）正确率 100%（手写用例覆盖）。
- 严格模式下未知列/函数全部报错。

### 5.3 IR 翻译正确性验收（I2/I3/I5）
- **等价性**：T3 P0 子集 AST→IR 不丢字段、不丢算子、能力位正确。
- **golden**：IR 快照对比通过。
- **能力位**：标注函数（percentile/series_*）的能力位与 builtin 表一致。

### 5.4 优化器正确性验收（O2/O3/O4）
- **语义保持**：规则重写前后 IR 跑同 SQL 同结果（I5 等价性测试）。
- **代价单调**：O5 基准测试中，优化后代价 ≤ 优化前（或 warn 并记录原因）。
- **策略可切换**：Conservative / Aggressive / ConfidenceGated 三策略在同一 IR 上选不同 AltPlan（O4 join 案例验证）。
- **Explain 可读**：每个决策附 reason（哪条统计、什么选择率、为什么选这条）。

### 5.5 后端正确性验收（B2/B3/B7）
- **SQL 形状**：P0 算子相邻时合并进单 SELECT；summarize/join 断 CTE（B7 golden 验证）。
- **三方言等价**：T6 mock dataset 上，同一 KQL 在 pg/duckdb/sqlite 结果一致（NULL 排序等差异文档化除外）。
- **golden**：三方言 SQL 快照对比通过。

### 5.6 端到端验收（S3/S6）
- **冒烟**：`kql -d <pg-dsn> 'orders | where id > 100 | take 10'` 真实返回结果（S3）。
- **跨后端**：同一查询 `--backend pg|duckdb|sqlite` 切换可执行（S4）。
- **Explain**：`kql explain` 输出 IR + 优化前后代价 + 决策 reason（S5）。
- **CI 绿**：S6 e2e 框架在 mock backend 上跑通 T3 P0 全集。

### 5.7 性能验收（O5 + 可选）
- 解析吞吐：≥ kqlparser 基准的 50%（手写 parser 预期接近，量化基线见 O5 bench）。
- 优化后 SQL 在 stats catalog 给定场景下，pg 执行计划不劣于未优化（用 `EXPLAIN` 对比预估代价，O5 输出）。

## 6. 阶段验收检查清单（每阶段 Definition of Done）

每个阶段完成判定必须满足：
1. **代码**：实现 + `go vet` + `go test ./...` 通过。
2. **测试**：单元测试覆盖关键路径；若涉及 T3/T4，golden 对比通过。
3. **文档**：阶段文档（本文档树）对应小节标注完成；公共接口有 godoc。
4. **依赖**：上游依赖阶段已 completed；下游不受阻塞。
5. **验收口径**：满足第 5 节对应小节。
6. **回归**：T5 fuzz（如已就绪）在本阶段改动后无新增 panic。

## 7. 阶段进度跟踪

各阶段状态由 TaskCreate/TaskUpdate 维护（细到子目标级，进入实施阶段时建立）。本汇总文档不做进度记录，只做规划快照。

---

## 文档树

```
docs/phases/
  README.md              ← 本文档（跨线汇总）
  frontend/
    README.md            ← 前端线 F1–F7 总览
    F1-lexer.md
    F2-ast-skeleton.md
    F3-parser-expr.md
    F4-parser-tabular.md
    F5-binder.md
    F6-diagnostic.md
    F7-builtin.md
  ir/
    README.md            ← IR 线 I1–I5 总览
    I1-core.md
    I2-translate.md
    I3-capabilities.md
    I4-pretty.md
    I5-equivalence.md
  optimizer/
    README.md            ← 优化器线 O0–O6 总览
    O0-stats-catalog.md
    O1-selectivity-cost.md
    O2-rules-core.md
    O3-decision-policy.md
    O4-join-altplan.md
    O5-cost-bench.md
    O6-advanced-rules.md
  backend/
    README.md            ← 后端线 B1–B7 总览
    B1-backend-framework.md
    B2-pg-p0.md
    B3-pg-cte-join.md
    B4-duckdb.md
    B5-sqlite.md
    B6-udf.md
    B7-snapshot.md
  shell/
    README.md            ← 外壳线 S1–S6 总览
    S1-api-skeleton.md
    S2-exec-backend.md
    S3-pg-wiring.md
    S4-duckdb-sqlite.md
    S5-cli-full.md
    S6-mock-e2e.md
  test-corpus/
    README.md            ← 测试语料线 T1–T6 总览
    T1-format-survey.md
    T2-extract.md
    T3-p0-regression.md
    T4-golden.md
    T5-fuzz.md
    T6-e2e.md
```

每个阶段文件统一结构：**阶段目标 / 依赖 / 顺序化子目标（S1, S2, …）每条含产出+验收 / 阶段产出物 / 风险对策**。
