# 实现进度与待办路线图

> 活文档：记录已完成的阶段、**被推迟的方向**（含依赖/优先级/理由）、以及下一步决策。
> 静态规划见 `docs/phases/README.md` 的依赖图；本文件追踪实际推进状态与认知。
> 更新原则：完成一个阶段或做一个推迟决策时，更新本文件。

## 1. 已完成

| 阶段 | 提交 | 产出 | 验收 |
|---|---|---|---|
| 项目脚手架 | `36a3da0` | DESIGN/phases 文档、go.mod、claude.md、.gitignore | — |
| **F1** 词法层 | `ad4cce9` | `token/`（枚举+Position+大小写不敏感 Lookup）、`lexer/`（金标准对齐）、benchmark | 9-token 验收 ✅，~120 MB/s |
| **F2** AST 骨架 | `ad4cce9` | `ast/`（Node/Expr/Stmt/Operator + P0 节点 + Visitor） | 编译期接口断言 ✅ |
| **F3** parser 表达式 | `88b6b98` | `parser/`（g4 优先级阶梯）、`diagnostic/`（F6 提前） | 表达式优先级/字面量/函数 ✅ |
| **F4** parser tabular P0 | `e1aaf45` | `parser/`（pipeline + 全部 P0 算子）+ keyword round-trip 审计 | 完整 P0 查询解析到 AST ✅ |
| **I1** IR 核心 | `_next_` | `internal/ir/`（Pipeline/Stage/Source/Expr/Type/ColID/Caps/Visitor） | 接口断言 + visitor 覆盖 ✅ |
| **I2** AST→IR 翻译器 | `_next_` | `internal/ir/translate*.go`（P0 算子，字符串列名占位） | 端到端翻译 + 类型/聚合 ✅ |
| **B1/B5-min** sqlite 后端 + **pkg/kql** | `4fe4fde` | `internal/backend/` + `sqlite/`（emit IR→SQL）+ `pkg/kql`（Exec API） | **e2e 最小闭环 ✅**：内存 sqlite 建表→KQL→取结果 |
| **S5-min** CLI | `e77416e` | `cmd/kql/`（main/output/ir：run/validate/explain + csv/json/table） | **命令行可跑 ✅** |
| **T1–T3-min** 语料回归 | `_next_` | `pkg/kql/testdata/corpus/sentinel/`（89 真实查询）+ `corpus_test.go` | **parse→translate→emit 覆盖 93%**（39%→72%→93%；P1/P2 算子 + 函数管道参数） |
| **F7-min** builtin 函数表 | `_next_` | `internal/frontend/builtin/`（Spec 表 + SQLite 模板）+ emit 接线 + 执行测试 | **常见函数能执行**：ago/tostring/iff/dcount/countif/make_set/strcat/coalesce/sum/avg/...（9 个执行用例全绿） |
| **F5** binder + **ColID 绑定** | `_next_` | `internal/frontend/binder/`（Schema→ColBinding + 大小写不敏感 Lookup + ColID 分配 + 物理名回写）| **pg 大小写折叠根治**：`EventType` 在 pg 存为 `eventtype` 也能正确解析执行（DESIGN §5 落地） |
| **O0** stats catalog | `_next_` | `internal/optimizer/stats/`（Catalog/Table/ColumnStats/Index/CorrVs/CostModel + 置信度 + YAML 加载器）+ 示例 + 10 单测 | **统计描述可加载**：pg_analyze/manual/sampling 源、缺字段降级、CorrVs 可选、未知字段告警 |
| **O2** 核心规则 | `_next_` | `internal/optimizer/rules/`（RewriteRule + Engine 不动点 + PredicatePushdown + ConstantFold + ColumnPrune）+ CatalogStatsReader | **三条核心规则生效**：谓词下推、常量折叠（where 1=1 删/where 1=0→空）、列裁剪（sqlite+pg e2e 语义等价验证） |
| **B2-min** pg 后端 | `_next_` | `internal/backend/pg/`（emit $N 占位符 + ILIKE + pg 函数）+ docker-compose.pg.yml + 10 pg e2e | **首次连真实生产库**：KQL → Docker PostgreSQL（pgx 纯 Go），10 用例全绿（sourceOnly/where/take/summarize/sort/distinct/in/ILIKE/binder） |

**🎉 当前能力（e2e 最小闭环 + CLI 已打通）**：
```go
res, _ := kql.Exec(ctx, ":memory:", `events | where state == "TEXAS" | summarize total = sum(damage) by state | sort by total desc | take 1`)
```
```bash
kql -d events.db 'events | where state == "TEXAS" | take 10'           # csv 默认
kql -d events.db -o json 'events | summarize c = count() by state'    # json
kql -d events.db explain 'events | where x > 0 | take 5'              # IR + SQL，不执行
kql validate 'events | where'                                         # 只解析，报诊断
```
KQL → 解析(F1-F4) → 翻译(I2) → SQLite SQL(sqlite emit) → 执行(modernc.org/sqlite) → 结构化结果 / CLI 输出。
覆盖 where/project/extend/take/sort/summarize(含 sum/count/bin)/join/union/distinct/count/top/in/string-op。
`go test ./...` 全绿（含 cmd/kql 的 explain/validate/run + 格式化 + 共享内存 DB e2e）。

**认知持久化**：`claude.md`（导航）+ 各模块 `NOTES.md`（frontend/ir/backend 的对齐细节与坑）。

## 2. 下一步

### e2e 最小闭环已完成 ✅（2026-06-17）

KQL 能从内存 sqlite 查询数据。接下来的优先级（待用户确认，以下为建议）：

1. **B2 pg 后端**：复用 sqlite emit 结构，换 `$1` 占位符 + pg 类型映射 + 真实连接。
   这是"首次能连真实库"的里程碑。
2. **F5 binder**：ColID 绑定 + 列/函数存在性校验。让查询在执行前校验列名，
   错误更友好；也为 optimizer 谓词下推铺路。
3. **S1/S2/S3 shell**：`kql -d <dsn> '查询'` CLI，首次命令行可跑。
4. **F7 builtin**：380+ 函数签名 + 能力位，让 FuncCall.Caps 精确化。
5. **扩 parser P1/P2**：mv-expand/parse/make-series/scan 等。

> 原 I2 章节的"F5/F7 占位"说明已落地：翻译器用字符串列名 + DefaultCaps 占位跑通了 e2e。

## 3. 被推迟的方向（backlog）

> 每条记：依赖、优先级、推迟理由、接入时机。完成时移到 §1。

### F5 — Binder（名称解析/类型推断/列绑定）
- **依赖**：F4 ✅、F7（builtin 函数签名，用于函数类型推断）
- **优先级**：高（IR 完成后，binder 是把"AST 上的字符串列名"换成"物理 ColID"的必要环节，后端 emit 才能跨方言）
- **推迟理由**：用户选了 IR 线优先。binder 不阻塞 I1+I2（翻译器先用字符串列名占位）。
- **接入时机**：I2 完成后立即做，或与 I2 并行（I2 用 placeholder，F5 回填 ColID）。
- **产出位置**：`internal/frontend/binder/`（scope/类型/schema 流）。

### F7 — Builtin 函数清单（380+ 签名 + 能力位）
- **依赖**：无（独立，纯数据工作）
- **优先级**：中（binder 的函数类型推断 + I2 的 FuncCall 能力位填充都需要它，但两者都能先用默认值占位）
- **推迟理由**：纯数据抽取工作，可随时插入；优先打通 IR 主干。
- **推迟时的占位**：FuncCall 能力位用"标量=SQLExpr、聚合=Aggregate"默认值；binder 遇未知函数先按 unknown 类型放行（宽松模式）。
- **产出位置**：`internal/frontend/builtin/functions.go`，从 `.source-projects/kqlparser/builtin/functions.go` 抽取（注明 Apache-2.0 来源）。

### 扩展 parser 到 P1/P2 算子
- **依赖**：F4 ✅
- **优先级**：中低（P0 覆盖了 MVP 核心查询；P1/P2 是增量）
- **推迟理由**：先把前端→IR→后端主干打通，再回头扩 parser 覆盖面。避免"前端很全但下游断"。
- **覆盖清单**（DESIGN §10）：
  - **P1**：`let`+管道引用（部分已做）✅、`union` ✅、`distinct` ✅、`parse`/`parse-where`/`parse-kv`、`mv-expand`/`mv-apply`、`make-series`、`partition`
  - **P2**：`evaluate`/插件（客户端 post-process）、窗口函数、graph-* 算子、`scan`、`fork`、`facet`
- **产出位置**：`internal/frontend/parser/op_*.go`，每算子一文件。

### IR 线 I3/I4/I5（I1+I2 之后）
- **I3** 能力位规则文档化（哪些函数 NeedsUDF/NeedsPostProc）
- **I4** pretty-print（用于 `kql explain` + golden 快照）
- **I5** 等价性测试（重写前后语义不变）
- **时机**：I1+I2 完成后，与 optimizer/backend 线并行打磨。

### Optimizer 线 O0–O6
- **依赖**：I1（IR 结构）、I5（等价性测试）、T3（P0 回归集）
- **O0** StatsCatalog + YAML 加载器可独立先做（依赖无）
- **时机**：IR + backend 主干打通后。

### Backend 线 B1–B7
- **依赖**：I1、O3（决策策略）、S2（exec Backend）
- **B1** Backend 接口 + **B2** pg P0 是首个能跑端到端 SQL 的里程碑（依赖 I2）
- **时机**：I2 完成后即可启动 B1+B2，是整个项目的"第一次能跑 SQL"节点。

### Shell 线 S1–S6
- **S1** pkg/kql API 骨架可独立先做（接口先行）
- **S2** exec Backend（依赖 S1）
- **S3** pg 接线 = **首次端到端可跑**（依赖 S1/S2/B2/F4/I2/O3）
- **时机**：B2 之后。

### Test-corpus 线 T1–T6
- **T1** 语料格式调研 + **T2** 抽取分类可独立先做
- **T3** P0 回归集（依赖 T2）是所有线功能验收的基础
- **时机**：I2 之后大量需要 golden，T3/T4 应尽早。

## 4. 跨阶段依赖速查（从 docs/phases/README.md 精简，标注当前可启动项）

```
已完成: F1 F2 F3 F4
当前:   I1 I2 (进行中)
可立即启动(无阻塞): F5(pending F7占位) F7 O0 S1 T1 T2
I2 完成后可启动: B1 B2 I3 I4 I5 T3 T4
O3 完成后可启动: B3 S5
B2+S2 完成后: S3 (首次端到端)
```
