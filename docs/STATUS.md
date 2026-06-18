# 项目现状全景分析（2026-06-17）

> 本文件是对 kql 项目当前状态的**详细快照**，供上下文摘要后恢复用。
> 由 29 个 commit、~11.2k 行非测试代码 + ~5k 行测试、17 个包构成。

## 1. 项目定位

`nzinfo/kql`：自研一套 **Kusto Query Language (KQL)** 的解析器、IR 翻译器、优化器和 SQL 后端。
- **module**：`nzinfo/kql`（Go 1.25）
- **远程仓库**：`git@github.com:nzinfo/kql.git`（分支 main，29 commits）
- **金标准语法**：`.source-projects/Kusto-Query-Language/grammar/Kql.g4`（只读参考，本地保留，git-ignored）
- **后端**：PostgreSQL（生产主力，pgx 纯 Go 驱动）+ SQLite（原型/测试，modernc.org/sqlite 纯 Go 驱动）

## 2. 架构分层与数据流

```
KQL 文本
  ↓ F1 lexer (token/lexer)        词法分析，金标准对齐
  ↓ F3 parser (parser)            手写递归下降，g4 优先级阶梯
  ↓ F2 AST (ast)                  Node/Expr/Stmt/Operator 接口
  ↓ I2 translate (ir/translate)   AST → IR Pipeline
  ↓ F5 binder (binder)            ColID 物理绑定 + 列校验
  ↓ O2 rules (optimizer/rules)    谓词下推/常量折叠/列裁剪
  ↓ O3 decision (optimizer/decision)  代价决策（可换策略）
  ↓ B2 emit (backend/{sqlite,pg}) IR → 方言 SQL
  ↓ exec                          驱动执行 → 结果
```

## 3. 17 个包现状

| 包 | 文件 | LOC | 状态 | 关键内容 |
|---|---|---|---|---|
| `internal/frontend/token` | 3 | 697 | ✅ 完成 | token 枚举(literal/operator/keyword)、Pos/File/Position 三层、大小写不敏感 Lookup、round-trip 审计 |
| `internal/frontend/lexer` | 4 | 771 | ✅ 完成 | 手写 rune scanner、Reset/File、金标准对齐(typekeyword 分组、in~ INCI)、`\` 宽容、benchmark ~120MB/s |
| `internal/frontend/ast` | 9 | 1306 | ✅ 完成 | 全部 P0+P1/P2 节点、Visitor/BaseVisitor、lambda/datatable/parse/mv-expand/make-series/render/consume/serialize/externaldata |
| `internal/frontend/parser` | 12 | 1793 | ✅ 完成 | g4 优先级阶梯、save/restore、所有算子(project-away 等)、union-as-function、lambda、数组字面量、benchmark |
| `internal/frontend/binder` | 1 | 329 | ✅ 完成 | ColID 物理绑定、大小写不敏感 Lookup、Schema 流(project/extend/aggregate/join union)、$left/$right 放行 |
| `internal/frontend/builtin` | 5 | 700+ | ✅ 完成 | **433 catalog names（> kqlparser 425 = 全覆盖）**：聚合/标量/网络/数组/series/geo/窗口/集合/pack/parse/数学/转换 全族注册。5 个 builtin 文件（builtin.go + round4/5/geo + signature.go）|
| `internal/frontend/diagnostic` | 2 | 199 | ✅ 完成 | Diagnostic/Code(KQL000-008)、List(Add/dedup/HasErrors/Render)、codes.go |
| `internal/ir` | 11 | 1411 | ✅ 完成 | Pipeline/Stage(P0)/Source/Expr/Type/ColID/Caps/Visitor、translate(全算子+特殊字面量+unquote+List)、NOTES |
| `internal/backend` | 1 | ~30 | ✅ 接口 | Backend 接口(Dialect/Emit/Exec/Close)、Query、Result、ResultColumn |
| `internal/backend/sqlite` | 5 | 882 | ✅ 完成 | emit(编号占位符?N、has_any/in~/ILIKE近似)、backend(modernc)、SchemaProvider(PRAGMA)、NOTES |
| `internal/backend/pg` | 3 | 755 | ✅ 完成 | emit($N占位符、ILIKE、pg函数重写)、backend(pgx)、SchemaProvider(information_schema)、iff类型修复(inline int/bool) |
| `internal/optimizer/stats` | 3 | 306 | ✅ 完成 | Catalog/Table/ColumnStats/MCV/Hist/CorrVs/IndexDef/CostModel、置信度(ceiling×完整度)、YAML加载器、示例 |
| `internal/optimizer/cost` | 4 | 582 | ✅ 完成 | Estimator(= / < / in / between / AND / OR / join 1/max / corr修正)、Cost{IO,CPU,Net,Mem}、Weights(pg/duckdb/sqlite)、降级 |
| `internal/optimizer/rules` | 4 | 709 | ✅ 完成 | RewriteRule接口、Engine(不动点+maxIter)、PredicatePushdown、ConstantFold(where 1=1删/where 1=0→Limit0)、ColumnPrune |
| `internal/optimizer/decision` | 6 | 750+ | ✅ 完成 | DecisionPolicy(Conservative/Aggressive/ConfidenceGated)、PredicateOrder、**JoinPlan(O4: Hash/NestLoop/Merge/IndexLookup cost+hint)**、AltPlan、Explain(决策日志) |
| `pkg/kql` | 1 | 290 | ✅ 完成 | Exec/ExecOn/Explain(--policy/--stats)、Policy类型、OpenBackend(dsn路由)、Error |
| `cmd/kql` | 3+1 | 583 | ✅ 完成 | CLI: run/validate/explain、--policy/--stats/-o csv/json/table、IR pretty-print、README |

**总计**：~13k 行非测试 + ~5.5k 行测试，21 包，433 builtin catalog names（kqlparser 全覆盖），90 真实语料 + 85 Sigma 真实狩猎规则(100% parse)。

## 4. 能力清单

### 解析（100% 语料覆盖）
- **P0 算子**：where/filter, project/project-away/keep/rename/reorder/smart, extend, take/limit, sort/order, summarize, join(kind=innerunique|inner|left|right|full), union, distinct, count, top, let(scalar/tabular)
- **P1 算子**：mv-expand(+to typeof), make-series, parse/parse-where/parse-kv, render, consume, getschema, serialize, externaldata, evaluate
- **P2**：top-nested, partition, fork, lookup, facet, sample, sample-distinct, reduce（passthrough）
- **表达式**：全部二元/一元/比较/逻辑/字符串操作符(has/contains/startswith/matches regex/...)、in/!in/in~/!in~、between/!between、函数调用(含命名参数)、member/index、iff/case、cast、dynamic、array literal
- **特殊**：函数式 lambda `let f=(x:int){body}`、datatable(schema)[data]、union-as-function、materialize(管道参数)、$left/$right 限定

### 执行（sqlite + pg 双后端）
- **能执行**：source/where/take/project/extend/sort/summarize(count/sum/avg/min/max/dcount/countif)/distinct/top/join($left/$right)/union/in/in~/has/iff/tostring/strcat/coalesce/make_set/array_length/abs/bin...
- **passthrough**（解析通过，emit 为 SELECT * 或近似）：mv-expand/make-series/parse/externaldata/evaluate（NeedsPostProc 标记）
- **CLI**：`kql -d <dsn> 'query'`（csv/json/table）、`explain`（IR+SQL+决策日志）、`validate`、`--policy`/`--stats`

### 优化器（O0-O5 完整链）
- **O0 stats catalog**：YAML 加载、置信度评分、多后端隔离
- **O1 selectivity**：MCV/范围/IN/AND/OR/join/corr 完整公式表(DESIGN §6.3)
- **O2 rules**：谓词下推、常量折叠、列裁剪（到不动点）
- **O3 decision**：三策略(Conservative/Aggressive/ConfidenceGated)、谓词排序、Explain 日志
- **O4 Join AltPlan**：cost-based join-method selection(Hash/NestLoop/Merge/IndexLookup)、ir.JoinHint、pg_hint_plan 注释 emit、策略分歧已验证
- **O5 benchmark**：optimizer ~3.9µs < parse ~4.7µs
- **用户可见**：`--stats <yaml> --policy gated explain` 展示统计→选择率→重排→决策

## 5. 测试现状

| 维度 | 数量 | 状态 |
|---|---|---|
| 测试包 | 14（含 cmd/kql） | 全绿 |
| 测试用例 | 233 | 0 FAIL, 0 SKIP(有 pg DSN), 0 panic |
| 语料覆盖 | 89/89 = 100% | parse→translate→emit 全绿 |
| P0 子集 | 67/67 = 100% | 回归基线 1.0 |
| Golden 快照 | 48 文件(24 case × 2 backend) | 锁定 emit SQL |
| e2e (sqlite) | 20+ 用例 | 内存 sqlite 建表→KQL→验证 |
| e2e (pg) | 13 用例 | Docker PostgreSQL, KQL_PG_DSN 门控 |
| Race detector | 13 包 | 全部 race-clean |

## 6. 认知持久化文件

| 文件 | 内容 |
|---|---|
| `claude.md` | 项目导航、金标准优先原则、git约定、进度表、常用命令 |
| `docs/PROGRESS.md` | 活路线图：已完成阶段、被推迟方向(依赖/优先级/理由)、依赖速查 |
| `internal/frontend/NOTES.md` | 语法对齐细节(11节)：kqlparser 偏差修正、g4 优先级、lambda/datatable/union/project-away 修复史 |
| `internal/ir/NOTES.md` | IR 设计要点、字段命名陷阱(Position/T)、typed-nil Walker、I1/I2 实现记录 |
| `internal/backend/NOTES.md` | sqlite/pg emit 细节、unquote、编号占位符、pg case-folding、iff类型修复、join $left/$right |
| `internal/optimizer/NOTES.md` | O0 置信度公式坑、O1 公式表+corr、O2 规则安全条件、O3 策略设计、O5 基准 |

## 7. 依赖

| 依赖 | 版本 | 用途 |
|---|---|---|
| `modernc.org/sqlite` | v1.52.0 | 纯Go SQLite 驱动（原型/测试） |
| `github.com/jackc/pgx/v5` | v5.10.0 | 纯Go PostgreSQL 驱动（生产） |
| `gopkg.in/yaml.v3` | v3.0.1 | O0 stats catalog YAML 加载 |

## 8. Docker

- `kql-pg` 容器：postgres:16, :5433, user/pass/db=kql（`docker-compose.pg.yml`）
- `testdata/pg-seed.sql`：events(6行) + meta(6行) 标准测试数据
- pg e2e 测试用 `KQL_PG_DSN` 环境变量门控

## 9. 已知限制（待做）

| 项 | 说明 | 优先级 |
|---|---|---|
| O3 PhysicalPlanner | 系统化物理方案枚举(HashJoin/NestedLoop/IndexedLookup) | 中 |
| duckdb 后端 | 第三个后端（分析加速） | 中 |
| O6 高级规则 | **CTE 断点决策已完成**（cost-based MATERIALIZED/NOT MATERIALIZED，footprint × work_mem 阈值）；**join 顺序重排已完成**（System-R DP） | 已完成 |
| 更多 builtin 函数 | ✅ **已对齐**：433 catalog names 覆盖 kqlparser 全部 386 标量+39 聚合（commit 026332a/2c70efe/76482f2/206e9a1） | 已完成 |
| lambda 调用语义 | `let f=(x){...}` 目前只解析不调用 | 低 |
| datatable 数据物化 | `datatable(...)[data]` 目前占位 SourceTable | 低 |
| PostProc 框架 | **✅ mv-expand/parse 已客户端执行**（MvExpand/Parse IR stage + applyPostProc + mvExpandRows/parseRows + 客户端 Aggregate/Limit/Project）；SQL failback 层已完成；make-series 待 | 已完成（make-series 待） |
| 类型推断 | Col.T 仍 Unknown（binder 只绑 ID+物理名） | 中 |
| --stats 进 run 路径 | ✅ **已完成**（commit 8859ec6 后）：run 路径现支持 --policy/--stats，经 ExecOnOpt 应用 O3/O4 代价优化 | 已完成 |
| 统计采集脚本 | O0.S6 pg 采集（cmd/kql-collect-pg-stats） | 低 |

## 10. 下一批候选方向

按价值排序：
1. **O3 PhysicalPlanner** — 把 O3 的单决策点（PredicateOrder）系统化为多物理方案枚举
2. **duckdb 后端** — 第三个后端，列式分析加速
3. **PostProc 框架** — 让 mv-expand/parse/make-series 真正执行（而非 passthrough）
4. **类型推断** — binder 填 Col.T，让更多函数翻译正确
5. ✅ **更多 builtin** — 已抽全 kqlparser 386 标量+39 聚合（433 catalog names）
6. **饱和语义分析查询改写**（未来方向）：引入基于饱和语义（saturation semantics）的查询改写机制——分析列取值饱和性（cardinality cap）消除冗余 distinct/union、折叠恒真/恒假谓词、识别幂等投影。与 O2 规则引擎正交，可作 O6.S4 探索方向。
6. **T-series 扩展** — 更多语料（Azure-Sentinel 仓库全量）、跨后端等价性测试
