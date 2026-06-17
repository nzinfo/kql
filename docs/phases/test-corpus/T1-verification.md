# T1 认知校验报告（对照 kql-parser/kqlparser 三类语料）

> 校验对象：T1-format-survey.md
> 参考：`kql-parser/fuzz_corpus_test.go`（1214 行，MIT）、`kql-parser/large_corpus_test.go`（343 行）、`kqlparser/testdata/grammar/*.kql`（Apache 2.0）

## 1. 校验结论

**YAML 决策需修订**。三类语料格式各异：
- **fuzz_corpus**：Go 字面量（`[]struct{name, query}`），1214 行，含 ~150+ 真实 Sentinel 查询，**抽取需 go/ast 解析**
- **large_corpus**：JSON 文件（外部 `testdata/corpus.json`），运行时加载，**抽取最简单**（直接 JSON→YAML）
- **kqlparser testdata**：纯 .kql 文本（一行一查询，从官方 grammar 抽取），**抽取最简单**（按行读）

YAML 仍合理，但抽取工作量评估要修正：fuzz_corpus 是难点。

## 2. 三类语料对照表

| 维度 | fuzz_corpus_test.go | large_corpus_test.go | kqlparser/testdata/grammar/ |
|---|---|---|---|
| 格式 | Go 字面量 `[]struct{name, query}` | JSON（外部 corpus.json） | 纯文本 .kql |
| 规模 | 1214 行，~150+ 条 | 343 行骨架（运行时加载外部 JSON） | 4 文件（literals/statements/operators/expressions.kql） |
| 含期望结果 | ❌ 仅 name+query | ❌ 仅 source+name+query | ❌ 仅查询文本 |
| 许可 | MIT（kql-parser） | MIT（kql-parser） | Apache 2.0（kqlparser） |
| 来源 | Sentinel/Defender/community 真实 | 同上（去重版？） | 官方 ANTLR grammar 抽取 |
| 抽取难度 | **中**（需 go/ast 解析 Go 字面量） | **低**（JSON 转换） | **低**（按行读） |
| 脱敏必要 | **高**（含 `DeviceProcessEvents`/`SecurityEvent`/`AlertInfo` 等真实表名 + 命令行含路径/域名） | 高 | 低（已是 `T | ...` 通用占位） |
| 是否含 P2 算子 | 是（parse/parsekv/mv-expand/evaluate/graph 等） | 是 | 是（operators.kql 全谱） |

## 3. YAML vs 其他格式的最终建议

**仍推荐 YAML**，理由：
- 可读 + 可注释（标注 tags/脱敏标记/期望）
- 与 stats catalog 一致（DESIGN.md 6.2）
- 三类源都能转 YAML（fuzz 需 go/ast，但一次性工作）

**否决方案**：
- JSON：不可注释，无法标注元数据
- Go 字面量：与运行时测试代码耦合，不利于跨工具消费
- 直接保留 .kql：无元数据（tags/期望/来源）

## 4. 抽取脚本工作量评估

- **fuzz_corpus**（难点）：需用 `go/ast` 解析 `var realWorldKQLQueries = []struct{...}{...}`，提取每个元素的 name/query 字段。工作量 **0.5-1 天**。可选替代：用正则匹配 `{name: "...", query: \`...\`}` 模式（Go raw string 容易匹配），但脆弱。
- **large_corpus**：直接 `encoding/json` 读 corpus.json，转 YAML。工作量 **0.5 小时**。
- **kqlparser testdata**：按行读 .kql（跳过注释/空行），每行一条。工作量 **0.5 小时**。

**总计：抽取脚本 ~1 天**（fuzz_corpus 占大头）。

## 5. 脱敏策略（基于实际抽样）

fuzz_corpus 实测含：
- 真实表名：`DeviceProcessEvents`、`SecurityEvent`、`AlertInfo`、`DeviceLogonEvents`、`SigninLogs`、`AzureActivity` 等（Sentinel/Defender 标准）
- 真实字段：`TimeGenerated`、`ProcessCommandLine`、`AccountName`、`DeviceName` 等
- 命令行/路径：`vssadmin.exe delete shadows`、`c:\*.VHD`、域名等

**脱敏规则**（采集脚本 + 人工抽查）：
- 表名：`DeviceProcessEvents`→`T1`、`SecurityEvent`→`T2`、按出现顺序编号；建立 `原表名→T{n}` 映射表保留语义（但语料内只放占位）
- 字段名：`TimeGenerated`→`ts`、`ProcessCommandLine`→`col_a`、按表内顺序
- 字面量敏感内容：保留结构（带通配符的命令行），但替换具体路径/域名为占位
- **保留**：查询结构、算子组合、复杂度（这是回归测试价值所在）

**人工抽查**：抽取后扫描可疑模式（域名/email/IP/路径），人工确认。

## 6. 修订 T1 文档的具体建议

1. **T1.S1**：明确 fuzz_corpus 是 Go 字面量、含 ~150+ 条、抽取需 go/ast。
2. **T1.S2**：明确 large_corpus 是 JSON 外部文件、抽取简单。
3. **T1.S3**：明确 kqlparser testdata 是纯 .kql 文本、4 文件按算子分类。
4. **T1.S4**：YAML 决策保留，但补充"否决 JSON/Go 字面量/.kql 的具体理由"。
5. **T1.S5**：schema 增加 `sanitized: bool` 字段标注是否已脱敏。
6. **T2.S1**：抽取脚本明确分三个子工具（extract_fuzz.go 用 go/ast、extract_large.go 用 JSON、extract_grammar.go 按行读）。
7. **T2.S3**：脱敏规则表化（表名/字段名/字面量三类），建立映射表。
8. **新增 T2.S6**：抽取后生成 `testdata/corpus/sanitization-report.md` 记录脱敏映射，便于审计。
9. **NOTICE**：注明 kql-parser（MIT）+ kqlparser（Apache 2.0）双来源。
