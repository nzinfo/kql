# T1 — 语料调研与统一格式确定

> 范围：调研笔记 + 格式决策 + schema
> 依赖：无
> 阶段目标：摸清 kql-parser 三类语料 + 确定本项目统一格式
>
> **校验状态**：已完成三类语料实地抽样校验，详见 `T1-verification.md`。**关键发现**：fuzz_corpus 是 Go 字面量（1214 行 ~150+ 条），抽取需 go/ast 解析（不能用纯正则），工作量 0.5-1 天；large_corpus 是 JSON 外部文件，抽取最简单；kqlparser testdata 是纯 .kql 文本，按行读即可。YAML 决策保留但 schema 需补 `sanitized` 字段，脱敏规则要表化。

## 顺序化子目标

### T1.S1 — 调研 fuzz_corpus_test.go
- 产出：调研笔记。
- **校验结论**：**Go 字面量**（`var realWorldKQLQueries = []struct{name, query string}{...}`），1214 行，~150+ 条真实 Sentinel/Defender/community 查询，**仅 name+query 两字段，无期望结果**。**抽取需 go/ast 解析**（不能用纯正则，Go raw string 模式可被正则匹配但脆弱）。
- 覆盖算子：含 P0 全部 + P2（parse/parsekv/mv-expand/evaluate/graph 等）。
- 含真实表名（`DeviceProcessEvents`/`SecurityEvent`/`AlertInfo`/`DeviceLogonEvents`/`SigninLogs`/`AzureActivity`）+ 命令行路径（`vssadmin.exe delete shadows`）+ 域名/IP，**脱敏必要**。
- 验收：明确语料结构、规模、覆盖算子清单、抽取难度（go/ast）。
- 测试来源：直接读 `kql-parser/fuzz_corpus_test.go`。

### T1.S2 — 调研 large_corpus_test.go
- 产出：调研笔记。
- **校验结论**：**JSON 格式**（运行时加载外部 `testdata/corpus.json`），343 行骨架代码 + JSON 数据。结构 `{source, name, query}`。**抽取最简单**（直接 encoding/json 转 YAML）。**抽取工作量 0.5 小时**。
- 验收：明确格式、规模、抽取难度（最低）。
- 测试来源：读 `kql-parser/large_corpus_test.go`。

### T1.S3 — 调研 kqlparser/testdata/grammar/
- 产出：调研笔记。
- **校验结论**：**纯 .kql 文本**，4 文件按算子分类（literals/statements/operators/expressions.kql），**一行一查询，`//` 注释**。从官方 ANTLR grammar 抽取，已用 `T | ...` 通用占位（**脱敏压力最低**）。**抽取按行读即可，工作量 0.5 小时**。
- 验收：明确组织风格（按算子分文件）、抽取难度（最低）。
- 测试来源：读 `kqlparser/testdata/grammar/`（4 个 .kql 文件 + README.md）。

### T1.S4 — 统一格式决策
- 产出：决策文档。
- **决策：YAML**（保留原决策，校验确认仍合理）。
- **否决方案的具体理由（校验补全）**：
  - JSON：不可注释，无法标注 tags/sanitized/source 等元数据
  - Go 字面量：与运行时测试代码耦合，不利于跨工具（fuzz/lint/IDE）消费
  - 直接保留 .kql：无元数据（无法标注覆盖算子、来源、脱敏状态）
- 三类源都能转 YAML：fuzz 需 go/ast（一次性 0.5-1 天），large 用 JSON 转换（0.5 小时），grammar 按行读（0.5 小时）。
- 验收：格式定稿；否决理由明确。
- 测试来源：调研结论。

### T1.S5 — 语料 schema 定稿
- 产出：schema 文档。
- **字段（校验补 `sanitized` 字段）**：
  - `name`（必填，唯一标识）
  - `source`（来源+许可，如 `kql-parser/fuzz_corpus (MIT)`）
  - `kql`（查询文本）
  - `tags`（覆盖的算子列表，如 `[where, summarize, join]`，支持多算子索引）
  - `sanitized`（**校验新增**，bool，标注是否已脱敏）
  - `expected_ast_snapshot` / `expected_ir_snapshot`（可选，golden 生成）
  - `notes`（备注，如脱敏映射、已知差异）
- 验收：schema 能表达三类语料来源；tags 支持多算子索引；sanitized 字段就位。
- 测试来源：手写示例。

## 阶段产出物
- `testdata/corpus/README.md`（格式说明 + schema）
- 调研笔记

## 风险与对策
| 风险 | 对策 |
|---|---|
| 格式选错导致后续返工 | T1.S4 评审 + 与 stats catalog 一致降低学习成本 |
| schema 漏字段 | T1.S5 参考三类语料实际内容补全（已补 sanitized） |
| **fuzz_corpus 抽取难度被低估**（校验新增） | T2.S1 分三个子工具；fuzz 用 go/ast，工作量预算 0.5-1 天 |
| **脱敏不彻底**（校验新增） | T2.S3 脱敏规则表化（表名/字段名/字面量三类）+ 映射表 + 人工抽查 |
