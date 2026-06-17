# T2 — 语料抽取与分类

> 范围：`testdata/corpus/` + 抽取脚本
> 依赖：T1
> 阶段目标：从 kql-parser 三类语料抽取到本项目格式，按算子分类

## 顺序化子目标

### T2.S1 — 抽取脚本（分三个子工具）
- 产出：`testdata/corpus/extract/` 下三个子工具（校验改，原是单一脚本）：
  - `extract_fuzz.go`：用 `go/ast` 解析 `kql-parser/fuzz_corpus_test.go` 的 `realWorldKQLQueries` 变量，提取每条 {name, query}。**工作量 0.5-1 天**（最难点）。
  - `extract_large.go`：直接 `encoding/json` 读 `kql-parser/testdata/corpus.json`，转 YAML。**工作量 0.5 小时**。
  - `extract_grammar.go`：按行读 `kqlparser/testdata/grammar/*.kql`（跳过 `//` 注释和空行），每行一条。**工作量 0.5 小时**。
- 三个工具输出统一 YAML（符合 T1.S5 schema），含 source 字段标注来源。
- 验收：三个子工具都能跑通；输出 YAML 符合 schema；fuzz 提取 ~150+ 条、large 提取条数与原 JSON 一致、grammar 提取 ~100+ 条（4 文件汇总）。
- 测试来源：kql-parser（MIT）+ kqlparser（Apache 2.0）语料。

### T2.S2 — 按算子分类目录
- 产出：`testdata/corpus/{where,project,extend,take,sort,summarize,join,let,union,distinct,mv_expand,evaluate}/`。
- 验收：每类目录有 ≥10 条语料（从抽取 + 手写补充）。
- 测试来源：抽取脚本输出。

### T2.S3 — 脱敏（规则表化）
- 产出：脱敏脚本 + `testdata/corpus/sanitization-rules.yaml`（**校验改：规则表化，原是散乱正则**）。
- **脱敏规则三类**（基于实际抽样）：
  - **表名**：`DeviceProcessEvents`→`T1`、`SecurityEvent`→`T2`、按出现顺序编号；保留语义但语料内只放 `T{n}` 占位。映射表存 `sanitization-rules.yaml` 的 `tables:` 段。
  - **字段名**：`TimeGenerated`→`ts`、`ProcessCommandLine`→`col_a`、按表内顺序。映射表存 `fields:` 段。
  - **字面量敏感内容**：保留结构（带通配符的命令行模板），但替换具体路径/域名为占位（`<PATH>`/`<DOMAIN>`/`<IP>`）。规则存 `literals:` 段。
- **保留**：查询结构、算子组合、复杂度（这是回归测试价值所在）。
- 验收：脱敏后无内部命名残留（自动扫描 `Device*`/`Security*`/`Alert*` 等模式）；语义保持；映射表完整可审计。
- 测试来源：人工抽查 + 自动扫描敏感模式。

### T2.S4 — 许可证合规
- 产出：`NOTICE` 文件（注明 kql-parser MIT 来源；抽取语料合规）。
- 验收：NOTICE 就位；许可证清晰。
- 测试来源：kql-parser LICENSE。

### T2.S5 — 索引生成 + 脱敏报告
- 产出：`testdata/corpus/index.yaml`（全语料索引：name→file、tag→names）+ `testdata/corpus/sanitization-report.md`（**校验新增**，记录脱敏映射表 + 抽查结果，便于审计）。
- 验收：索引完整；支持按 tag 查询；脱敏报告可追溯每条语料的脱敏操作。
- 测试来源：抽取脚本生成。

## 阶段产出物
- `testdata/corpus/extract.go`
- 分类语料目录
- `NOTICE` + `index.yaml`

## 风险与对策
| 风险 | 对策 |
|---|---|
| 抽取脚本漏解析 kql-parser 内部结构 | T1.S1 调研充分 |
| 脱敏不彻底 | T2.S3 双重检查 |
| 语料重复 | T2.S1 去重逻辑 |
