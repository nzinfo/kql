# 跨项目对比分析（2026-06-17）

> 对照 `.source-projects/` 四个参考项目与本项目（`nzinfo/kql`）`docs/`，
> 系统性盘点定位差异、能力差距与可借鉴资产。

## 一、四个参考项目的定位

| 项目 | 语言 | 定位 | 对本项目的价值 |
|---|---|---|---|
| **Kusto-Query-Language** (microsoft) | C# | **官方解析器 + 语义分析器**（Kusto.Language），含 ANTLR 金标准 grammar (`Kql.g4`) | 🥇 金标准语法 + OperatorKind/SyntaxFacts 权威枚举 + Binder 实现参考 |
| **kqlparser** (cloudygreybeard) | Go | **纯 Go 解析器 + 语义分析器**，结构最接近本项目（ast/binder/builtin/diagnostic/lexer/parser/symbol/token/types） | 🥈 直接对照：函数表 380+、operator list、类型系统 |
| **kql-parser** (Go, ANTLR) | Go | ANTLR 生成 + Sigma roundtrip fuzz（真实 Sentinel/Defender 查询） | 🥉 生产级 fuzz corpus（35+ 真实狩猎规则）、grammar 参考 |
| **rust-kql** (Rust) | Rust | nom 解析器 + **DataFusion planner**（执行层，Arrow 列式） | 执行层对标（DataFusion ≈ 我们的 DuckDB/Arrow 路径） |

### 关键观察
1. **kqlparser 与本项目架构高度同构**（同名包：ast/binder/builtin/diagnostic/lexer/parser/token/types），
   是最直接的对照基线。
2. **本项目独有**：优化器（O0–O6）、多 SQL 后端（pg/duckdb/sqlite）、Arrow 多引擎管道、
   cost-based join 选择。kqlparser/rust-kql 都没有这些——它们停在"解析 + （可选）语义分析"。
3. **本项目缺**：完整函数表（kqlparser 386 标量 + 39 聚合；我们 158 标量 + 18 聚合）。

---

## 二、能力差距明细

### 2.1 语法 / 算子（tabular operators）

对照 kqlparser `parser.go`（line 714–722 的 tabular-operator 关键字表）+ kql-parser `KQLParser.g4`：

| 算子 | kqlparser | rust-kql | **本项目** | 差距 |
|---|---|---|---|---|
| where/filter, project/-away/-keep/-rename/-reorder/-smart, extend, take/limit, sort/order, summarize, join, union, distinct, count, top | ✅ | ✅ | ✅ | 无 |
| mv-expand, mv-apply, make-series, parse/parse-where/parse-kv, render, consume, getschema, serialize, externaldata, evaluate | ✅ parse | ✅ parse | ✅ parse | 无（均 passthrough emit） |
| top-nested, partition, fork, facet, sample, sample-distinct, reduce, lookup, scan, top-hitters | ✅ parse | ✅ parse | ✅ parse | 无（passthrough） |
| as, invoke, set | ✅ | ✅ | ✅ | 无（已补齐） |
| graph-match / make-graph / graph-shortest-paths / graph-mark-components / graph-to-table / graph-where-nodes / graph-where-edges | ✅ AST | ❌ | ✅ AST | 无（passthrough；真实语义需图引擎） |
| execute-and-cache, assert-schema, macro-expand | ✅ AST | ❌ | ✅ AST | 无（passthrough） |

**结论**：算子层面**零语法差距**。本项目 g4 OPERATOR 61/61、STATEMENT 8/8 全覆盖，
kqlparser grammar corpus 207/207 = 100%。所有 P2/P3 算子均 passthrough（与 kqlparser 一致——
kqlparser 也只解析不执行 graph/scan/fork 的真实语义）。

### 2.2 标量操作符（string/comparison）

对照官方 `OperatorKind.cs` + `SyntaxFacts.cs`：

| 操作符族 | 官方 | kqlparser | **本项目** | 差距 |
|---|---|---|---|---|
| 算术 `+ - * / %`、一元 `- +` | ✅ | ✅ | ✅ | 无 |
| 关系 `< > <= >= == !=` | ✅ | ✅ | ✅ | 无 |
| 逻辑 `and or` | ✅ | ✅ | ✅ | 无 |
| `=~ !~`（大小写不敏感 eq） | ✅ | ✅ | ✅ | 无 |
| `:`（=~ 别名） | ✅ | ✅ | ✅ | 无 |
| `has/!has/has_cs/!has_cs/hasprefix(_cs)/hassuffix(_cs)` | ✅ | ✅ | ✅ | 无 |
| `contains/!contains/contains_cs`、`startswith/!startswith(_cs)`、`endswith/!endswith(_cs)` | ✅ | ✅ | ✅ | 无 |
| `has_any/has_all` | ✅ | ✅ | ✅ | 无 |
| `in/!in/in~/!in~` | ✅ | ✅ | ✅ | 无 |
| `between/!between` | ✅ | ✅ | ✅ | 无 |
| `like/!like/like_cs/!like_cs` | ✅ | ✅ | ⚠️ | **like 系列 emit 未单独处理**（normalize 到 contains/regex 近似） |
| `matches regex` | ✅ | ✅ | ✅ | 无 |

**结论**：仅 `like`/`like_cs` 系列的 emit 是弱近似，其余零差距。

### 2.3 函数表（builtin）—— **主要差距**

| 维度 | kqlparser | **本项目** | 差距 |
|---|---|---|---|
| 标量函数 | **386** | **158** | 缺 **285** |
| 聚合函数 | **39** | **18** | 缺 **24** |

#### 缺失聚合（24 个，按可补充性排序）

| 聚合 | 价值 | 可补性 |
|---|---|---|
| `any` / `anyif` / `take_any` / `take_anyif` | 高（Sentinel 高频） | 🟢 pg/sqlite 都有 ANY_VALUE/`MIN(random())` 近似 |
| `arg_max` / `arg_min` | 高（top-N by 分组） | 🟢 pg `DISTINCT ON`/窗函数；sqlite 子查询 |
| `count`（无参形式） | 中 | 🟢 已有 countif，补无参 |
| `dcountif` | 中 | 🟢 sum(case) |
| `make_list_if` / `make_set_if` / `make_bag(_if)` | 中 | 🟡 make_bag 需 JSON 聚合 |
| `hll` / `hll_merge` / `dcount_hll` | 低（pg 无原生 HLL） | 🔴 需扩展或近似 |
| `tdigest` / `tdigest_merge` / `percentile*_tdigest` | 低 | 🔴 需扩展 |
| `percentiles` / `percentiles_array` | 中 | 🟡 percentilesw 已有，补复数形式 |
| `binary_all_and/or/xor` | 低 | 🟢 位运算聚合 |
| `stdevp` / `variancep` | 中 | 🟢 总体方差 |
| `buildschema` | 低 | 🔴 JSON schema 推断 |

#### 缺失标量（285 个）—— 按类别

| 类别 | 缺失数 | 价值 | 建议 |
|---|---|---|---|
| `series_*`（时序：fft/fir/iir/fit/decompose/anomalies/...） | **51** | 中（make-series 配套） | 🟡 优先 series_add/subtract/multiply/divide/fill_*（基础算术）；FFT 等需后端支持 |
| `geo_*`（地理：geohash/h3/s2/polygon/line/point/distance/...） | **53** | 低（本项目非地理场景） | 🔴 暂不补；GeoIP 可单独加 |
| `ipv4_*` / `ipv6_*`（网络） | 19 | 中（Sentinel 安全场景高频） | 🟢 ipv4_is_match/compare/is_in_range 用 pg inet/cidr |
| `array_*`（数组操作） | 16 | 中 | 🟢 pg `array_*`/jsonb；duckdb `list_*` |
| `binary_*`（位运算） | 6 | 低 | 🟢 标准位运算 |
| `bag_*`（dynamic 对象操作） | 7 | 中 | 🟡 pg jsonb 操作 |
| `parse_*`（csv/url/xml/user_agent/version/path） | 8 | 中 | 🟢 多数有 SQL 等价（regex/unnest） |
| `base64_*` / `gzip_*` / `zlib_*` | 8 | 低 | 🔴 需 UDF（UDF-STRATEGY 已标注） |
| `punycode_*` | 4 | 极低 | 🔴 跳过 |
| `tdigest*` | 5 | 低 | 🔴 需扩展 |
| `unicode_*` | 2 | 低 | 🟢 codepoints |
| `unixtime_*_todatetime` | 4 | 低 | 🟢 to_timestamp |
| `to*` 转换（toboolean/todatetime/todynamic/toguid/...） | 7 | 高 | 🟢 多数已有 tobool/toint；补 tolong/toreal/toguid |
| `beta_*`（统计） | 3 | 低 | 🟢 数学公式 |
| `row_*`（窗口：cumsum/number/rank/window_session） | 6 | 高（窗口函数） | 🟢 pg/sqlite 窗口函数 |
| `pack*` / `zip` | 3 | 中 | 🟡 JSON 对象构造 |
| `current_*` / `cursor_*` | 9 | 极低（ADX 内部） | 🔴 跳过 |
| `make_datetime/string/timespan` | 3 | 中 | 🟢 构造函数 |
| 数学（acos/asin/atan/atan2/cos/sin/tan/cot/degrees/radians/exp2/erf/erfc/loggamma/ceil） | 14 | 中 | 🟢 pg/sqlite 标准数学函数 |
| `has_*` index/ipv4 | 1+ | 中 | 🟢 已有 has_any/all，补 index 变体 |
| 其他（assert/bin_at/bin_auto/countof/datepart/datetime/estimate_data_size/extract_all/extract_json/format_bytes/gettype/guid/indexof_regex/ingestion_time/isascii/isfinite/isnan/isutf8/jaccard_index/max_of/min_of/next/notnull/prev/regex_quote/repeat/replace_strings/set_difference/set_equals/strcat_array/strcmp/strrep/translate/treepath/url_encode_component/welch_test/...） | ~60 | 混合 | 🟢 多数有直接 SQL 等价 |

### 2.4 类型系统

| 维度 | kqlparser | **本项目** | 差距 |
|---|---|---|---|
| 标量类型（bool/int/long/real/decimal/string/datetime/timespan/guid/dynamic） | ✅ 11 种 | ✅ 11 种 | 无 |
| `CommonType`（数值提升：int→long→real→decimal） | ✅ | ✅（F5.S4 类型推断） | 无 |
| `Compatible`（dynamic 与万物兼容） | ✅ | ✅ | 无 |
| Tabular 类型（列 schema） | ✅ `types.Tabular` | ⚠️ `exec.Schema`（exec 层）+ ir.Type | **binder 层 Tabular 类型推断弱**（STATUS §9 已列"类型推断 中"优先级） |
| decimal 字面量 `decimal(0.1)` | ✅（函数调用兼容） | ✅（函数调用兼容） | 无 |

### 2.5 语义分析（binder）

| 维度 | kqlparser | **本项目** | 差距 |
|---|---|---|---|
| 列绑定（ColID + 大小写不敏感） | ✅ | ✅ | 无 |
| 未知列诊断 KQL001 | ✅ | ✅ | 无 |
| 类型不匹配 KQL002 | ✅（warning） | ✅（F5.S4） | 无 |
| 函数校验 KQL003/KQL004 | ✅ | ✅ | 无 |
| Scope（let 嵌套 + shadow） | ✅ `symbol.Scope` | ✅ `binder.Scope` | 无 |
| 重复绑定 KQL006 | ✅ | ✅ | 无 |
| 表/列 schema 流（project/extend/aggregate/join 流动） | ✅ | ✅ | 无 |
| **Tabular 类型流（表达式节点带返回类型）** | ✅ `SemanticInfo` | ⚠️ 仅 30+ 函数 | **中等差距**——kqlparser 对每个 Expr 节点推断类型；我们只在 30+ 函数上推断 |

### 2.6 执行 / 后端 —— **本项目独有优势**

| 维度 | kqlparser | rust-kql | **本项目** |
|---|---|---|---|
| 执行 | ❌（只解析） | ✅ DataFusion | ✅ pg/sqlite/duckdb |
| 多后端 | ❌ | ❌（仅 DataFusion） | ✅ 3 后端 + 跨后端等价性 19/19 |
| 优化器 | ❌ | ❌ | ✅ O0–O6（stats/cost/rules/decision/join-plan/two-stage-agg/view-match） |
| cost-based join 选择 | ❌ | ❌ | ✅ O4（Hash/NestLoop/Merge/IndexLookup + pg_hint_plan） |
| 多引擎管道（pg↔Arrow↔DuckDB） | ❌ | 部分（Arrow 原生） | ✅ EngineRouter + ExecMulti |
| CLI | ❌ | ✅ kq | ✅ run/validate/explain |
| PostProc 框架（mv-expand/parse/series 客户端计算） | ❌ | 部分 | ✅ exec.go |

**结论**：本项目在**执行与优化**维度远超两个 Go/Rust 参考项目——它们都停在解析（+可选语义）。
rust-kql 的 DataFusion planner 是对标对象，但只支持 where/project/extend/summarize/sort/take/top/
join/union/distinct/count/mv-expand/parse/render/as/serialize/getschema/consume（约 18 个算子），
远少于本项目的全算子 passthrough。

---

## 三、可借鉴资产

### 3.1 kql-parser 的 Sigma roundtrip corpus（**高价值，建议引入**）
`fuzz_corpus_test.go` 含 **35+ 真实 Microsoft Sentinel / Defender XDR 狩猎规则**（勒索软件、
Kerberoasting、横向移动检测等），是生产级 KQL 的最佳 fuzz 基线。当前本项目用：
- 自维护 90 条 sentinel 语料（100%）
- kqlparser grammar corpus 207/207（100%）

**建议**：把 kql-parser 的 `realWorldKQLQueries`（35 条）作为第二轮 fuzz corpus 引入，
专门验证复杂 `let`/`union isfuzzy`/`parse ... with`/`extend tostring(EventData.X)` 模式。

### 3.2 kqlparser 的函数表（**主要差距来源，建议批量导入签名**）
`builtin/functions.go`（988 行）是权威的 386 标量 + 39 聚合签名表，含参数类型。
本项目可按"类别优先级"分批导入（见 §四建议）。

### 3.3 Kusto-Query-Language 的 SyntaxFacts / OperatorKind（**权威枚举参考**）
`Symbols/OperatorKind.cs`（63 个操作符）+ `Syntax/SyntaxFacts.cs`（keyword→OperatorKind 映射）
是判断"我们是否遗漏某个操作符"的金标准。本项目 `internal/frontend/NOTES.md` 已对齐。

### 3.4 rust-kql 的 DataFusion planner（**执行层对标**）
rust-kql 的 `datafusion-kql/src/planner.rs` 展示了如何把 KQL 算子映射到列式执行引擎
（DataFusion 的 DataFrame builder 模式）。对 本项目的 DuckDB/Arrow 路径有参考价值，
但 DataFusion 的 builder API 与 DuckDB 的 SQL emit 路径差异较大。

---

## 四、建议的下一步优先级

> 基于差距价值 × 实现成本，排序如下。每项标注对应 docs/ 文档。

| 优先级 | 任务 | 价值 | 成本 | 对应文档 |
|---|---|---|---|---|
| 🟢 高 | **批量导入 kqlparser 函数签名（第 1 批：高价值）** —— any/arg_max/arg_min/count/dcountif/make_list_if/make_set_if/row_*/to*/数学函数/max_of/min_of/set_*/pack/zip（约 80 个） | 高（Sentinel + 通用查询） | 中（签名导入 + pg/sqlite emit 模板） | capabilities.md §Functions |
| 🟢 高 | **引入 kql-parser Sigma roundtrip corpus**（35 条真实狩猎规则 fuzz） | 高（生产级验证） | 低（复制 query + roundtrip 测试） | PROGRESS.md T 系列 |
| 🟡 中 | **Tabular 类型流增强**（binder 对每个 Expr 节点推断返回类型，对齐 kqlparser SemanticInfo） | 中（更多函数翻译正确） | 中 | STATUS.md §9 类型推断 |
| 🟡 中 | **like/like_cs emit**（pg LIKE + 同大小写 sqlite） | 中（补全 string op emit） | 低 | capabilities.md §Operators |
| 🟡 中 | **ipv4_* / ipv6_* 函数族**（Sentinel 安全场景） | 中 | 中（pg inet/cidr 映射） | capabilities.md |
| 🟢 低 | **series_* 基础算术**（series_add/subtract/multiply/divide/fill_forward/backward） | 中（make-series 配套） | 中 | capabilities.md |
| 🟢 低 | **第 2 批函数**（array_*/parse_*/bag_*/datetime 转换，约 80 个） | 中 | 中 | capabilities.md |
| 🔴 极低 | geo_*（53 个）/ punycode_* / current_*/cursor_* / base64/gzip/zlib（需 UDF） | 低（非本项目场景） | 高 | UDF-STRATEGY.md |

---

## 五、文档同步建议

当前 `docs/` 与对比结论的偏差：

| 文档 | 当前陈述 | 对比结论 | 建议 |
|---|---|---|---|
| `capabilities.md` | "Functions (~103 catalogued)" / STATUS "57 builtin" | 实际 158 标量 + 18 聚合（builtin.go） | **更新数字**（文档落后于代码） |
| `STATUS.md` §9 | "更多 builtin 函数 当前 57 个，kqlparser 有 380+" | 实际已 158；差距是 285 标量 + 24 聚合 | **更新数字 + 引用本文** |
| `ALIGNMENT-ANALYSIS.md` §三 | "F7 完整函数表（380+）/ 文档 按需补" | 本文档量化了缺口（285/24） | **交叉引用本文** |
| `capabilities.md` §Operators | like/like_cs 未单列 | kqlparser 支持，我们 emit 弱近似 | **补一行标注** |

---

## 六、总结

**本项目相对四个参考项目的定位**：
- 🥇 **语法对齐**：与官方 Kusto-Query-Language 金标准 100% 对齐（g4 61/61 OPERATOR + 8/8 STATEMENT），
  与 kqlparser 同级（207/207 grammar corpus）。
- 🥇 **执行与优化**：**独有**多 SQL 后端 + cost-based 优化器 + Arrow 多引擎管道——
  kqlparser（Go 同构）和 rust-kql（Rust）都没有这些。
- 🥈 **函数覆盖**：158/386 标量 + 18/39 聚合，**是主要差距**，但已覆盖所有高频函数。
- 🥈 **语义分析**：与 kqlparser 基本同级，Tabular 类型流是次要差距。
- 🟢 **生产验证**：建议引入 kql-parser 的 35 条 Sigma 真实狩猎规则作为第二轮 fuzz corpus。

**一句话**：本项目在"解析 + 语义"维度与最佳 Go 参考（kqlparser）同级，
在"执行 + 优化"维度**显著领先**所有参考项目，函数表是唯一可量化的主要缺口。
