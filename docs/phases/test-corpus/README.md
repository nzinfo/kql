# 测试与语料线阶段拆解

> 范围：`testdata/corpus/` + 各线 golden/snapshot 机制
> 总目标：为所有线提供回归基线与验收数据；独立于其他线但服务全部

## 阶段

### Phase T1 — 语料调研与统一格式确定
**目标**：摸清 kql-parser 三类语料 + 确定本项目统一格式。

子目标：
- 调研 `kql-parser/fuzz_corpus_test.go`（格式：Go 字面量？条目数？覆盖算子？）。
- 调研 `kql-parser/large_corpus_test.go`（JSON？规模？）。
- 调研 `kqlparser/testdata/grammar/`（按算子分文件的 testdata 组织）。
- 确定本项目统一格式：**YAML**（理由：可读、可注释、易标注元数据、与 stats catalog 一致）。
- 语料 schema 设计（每条：name / source（来源+许可）/ kql / tags（算子列表）/ expected_ast_snapshot / expected_ir_snapshot）。

验收：调研笔记 + 格式决策文档；schema 定稿。
产出物：`testdata/corpus/README.md` + schema。
依赖：无。

### Phase T2 — 语料抽取与分类
**目标**：从 kql-parser 三类语料抽取到本项目格式，按算子分类。

子目标：
- 抽取脚本（`testdata/corpus/extract.go`：从 kql-parser Go 字面量/JSON 读出 → 转 YAML）。
- 按 MVP 算子分类目录（`testdata/corpus/{where,project,extend,take,sort,summarize,join,let,union}/`）。
- 脱敏：Sentinel 真实查询可能含内部表名/字段 → 替换为通用占位（`T1`/`T2`/`col_a`）。
- 许可证合规：kql-parser 是 MIT，可在 NOTICE 注明来源。

验收：每类目录有 ≥10 条语料；脱敏后无内部命名残留；NOTICE 文件就位。
产出物：分类语料 + 抽取脚本 + NOTICE。
依赖：T1。

### Phase T3 — P0 算子最小回归集
**目标**：手写 + 抽取的 P0 算子最小回归集（~50 条），每条标注覆盖算子。

子目标：
- 手写覆盖每个 P0 算子的最小用例（每算子 5–8 条，含正常/边界/错误）。
- 从 T2 抽取补充真实复杂度的 P0 查询。
- 元数据 tags 完整（便于按算子过滤跑测试）。
- 期望结果标注（P0 子集可手工标注 AST/IR 快照）。

验收：50 条语料覆盖全部 P0 算子；每条 tags 完整。
产出物：`testdata/corpus/p0/*.yaml` + 索引。
依赖：T2。

### Phase T4 — Golden file 机制
**目标**：查询 → AST/IR/SQL 快照对比，防漂移。

子目标：
- Golden 框架（`internal/testutil/golden.go`：`Update` flag + diff 输出）。
- AST golden：F4 输出对照。
- IR golden：I2 输出对照。
- SQL golden：B7 三后端对照。
- CI 集成：`go test -update` 更新，否则对比。

验收：F4/I2/B7 改动后 golden 不应有非预期漂移；有漂移时 diff 可读。
产出物：golden 框架 + 各线 golden 文件。
依赖：T3、F4、I2、B7。

### Phase T5 — 大语料 fuzz/解析压力测试
**目标**：验证不 panic + 不漏算子，不验证结果。

子目标：
- 把 kql-parser 完整 fuzz/large corpus 作为压力输入。
- 只断言：解析不 panic；能识别的算子全部识别；不能识别的优雅报错（带 code）。
- 覆盖率统计：P0 算子在语料中的覆盖率。

验收：1000+ 条语料无 panic；P0 算子覆盖率报告生成。
产出物：fuzz/stress 测试 + 覆盖率报告。
依赖：F4、T2。

### Phase T6（后置）— 端到端执行结果对比
**目标**：真实数据库（或 mock）执行结果跨后端等价性验证。

子目标：
- mock dataset（小规模固定数据集，三后端加载）。
- 端到端：语料 KQL → 各后端执行 → 结果对比。
- 已知差异文档化（NULL 排序、类型转换）。

验收：T3 P0 子集三后端结果一致；差异文档化。
产出物：e2e 对比测试 + 差异文档。
依赖：S3、S4、S6、B7、T3。

## 关键决策记录

1. **统一 YAML 格式**：可读、可注释、易标注元数据、与 stats catalog 一致；优于 Go 字面量（编译耦合）和 JSON（不可注释）。
2. **按算子分类 + tags 双索引**：目录按主算子分便于人查；tags 支持一条语料覆盖多算子（如 `where+summarize+join`）。测试可按 tag 过滤跑。
3. **引入 golden file 快照**：AST/IR/SQL 文本对照是检测漂移最便宜的手段，比写"期望 AST 树"结构断言维护成本低。`-update` flag 让重构时可批量刷新。
4. **执行结果对比后置**：T1–T5 不依赖真实数据库，能在 CI 快速跑；T6 才需要数据库/mock，作为可选集成阶段。
5. **不验证"查询语义对错"**：本项目不是 KQL 参考实现，无法判定 Sentinel 语料"正确结果"。只验证：解析不崩、识别算子、AST/IR/SQL 形状稳定、跨后端等价。
6. **许可证合规**：kql-parser MIT，抽取语料在 NOTICE 注明来源；Sentinel 真实查询脱敏避免泄露内部命名。

## 风险与对策

| 风险 | 对策 |
|---|---|
| 语料漂移（重构后大面积 golden 失败） | 区分"预期漂移"（重构）与"意外漂移"（bug）；review diff |
| 敏感数据脱敏不彻底 | 抽取脚本做正则替换 + 人工抽查 |
| 语料覆盖算子不全 | T3 最小回归集 + T5 覆盖率报告双保险 |
| 版权纠纷 | NOTICE + 仅抽取 MIT/Apache 项目语料 |
| golden 文件爆炸（数量大） | 按算子分目录 + 压缩无关字段 |
| 真实数据库依赖拖慢 CI | T6 后置 + mock dataset 替代 |
