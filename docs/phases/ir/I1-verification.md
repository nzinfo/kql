# I1 认知校验报告（对照 rust-kql AST）

> 校验对象：I1-core.md
> 参考：`/home/nzinfo/src.erp-ext/kql/rust-kql/kqlparser/src/ast.rs`（163 行）

## 1. 校验结论

**认知基本成立**。rust-kql 的 enum 体系验证了"Pipeline = Source + Operators"的正确性。**关键差异：rust-kql 是 AST 而非 IR**——它没有"物理列 ID"和"能力位"概念（这是我们的创新，rust-kql 不提供参考）。rust-kql 在 `datafusion-kql/planner.rs` 直接从 AST 翻 DataFusion plan，跳过了我们 IR 层。

## 2. 逐条校验表

| 校验点 | 我们认知 | rust-kql 实际（位置） | 偏差/借鉴 |
|---|---|---|---|
| Pipeline 顶层 | Pipeline{Source, Stages} | `TabularExpression{source: Source, operators: Vec<Operator>}`（ast.rs:11-15） | **一致** |
| Source 类型 | 表名/子管道 | `enum Source`（ast.rs:17-26）：Datatable/Externaldata/Find/Print/Range/Reference/Union | **借鉴**：Source 应是 enum，比我们的"表名"更丰富——MVP 只实现 Reference（表名），其他预留 |
| Operator 表达 | Stage 接口 | `enum Operator`（ast.rs:28-63）共 31 变体 | **借鉴 enum 思路**，但我们 Go 走 interface（I1.S1 已定） |
| 算子覆盖 | P0: where/project/extend/take/sort/summarize/join/union/distinct/let | rust-kql 实现了 As/Consume/Count/Distinct/Evaluate/Extend/Facet/Fork/Getschema/Join/Lookup/MvApply/MvExpand/Parse*/Partition/Project*/Reduce/Render/Sample*/Serialize/Summarize/Sort/Take/Top/Union/Where | **rust-kql 比 kqlparser 简化**（无 graph/scan），更接近 MVP 范围 |
| 列引用 | 物理列 ID（创新） | rust-kql 用 `Ident(String)` + `Vec<String>`（纯字符串名，ast.rs:67） | **反例验证**：rust-kql 用字符串名是因为它单后端（DataFusion），无需跨方言。我们多后端必须用 ColID——I1.S3 设计正确 |
| Expr 类型 | Lit/Col/BinOp/UnaryOp/FuncCall/Agg/Case/Member | `enum Expr`（ast.rs:65-84）：Ident/Index/Literal/Equals/NotEquals/And/Or/Add/Substract/Multiply/Divide/Modulo/Less/Greater/LessOrEqual/GreaterOrEqual/Func | **rust-kql 简化**：每个运算符是独立 enum 变体；我们用 BinOp{Op string} 更紧凑。**补**：rust-kql 漏了字符串操作符（has/contains/startswith）—— 它们走 Func 还是 BinOp？需要查 parser.rs |
| 能力位 | Caps（创新） | **rust-kql 无概念** | 我们的创新，rust-kql 不提供参考。I1.S4 + I3 需从零设计 |
| 类型系统 | bool/int/long/real/string/datetime/timespan/dynamic | `enum Type`（ast.rs:86-97）：Bool/DateTime/Decimal/Dynamic/Int/Long/Real/String/Timespan | **补**：rust-kql 有 Decimal（KQL 真有 decimal 类型），我们 I1 应补 |
| Literal 类型 | 与 Type 对应 | `enum Literal`（ast.rs:99-110）每个都用 Option 包裹（允许 null 字面量） | **借鉴**：Literal 字段用 `Option<T>` 表达 KQL 的 null 字面量 |
| 投影列追踪 | I3 阶段做 | rust-kql 不在 AST 阶段做（planner 阶段才推） | 我们 I3 在 IR 层做更合理（与 O2 列裁剪衔接） |

## 3. rust-kql 哪些可借鉴 / 不适用

**可借鉴**：
- `TabularExpression{source, operators}` 结构（I1.S1 Pipeline 直接对齐）
- `enum Source` 的多种 source 类型（I1.S1 Source 应是接口/ sealed，预留 Datatable/Print/Range 等）
- `Literal` 用 Option 包裹表 null 字面量
- Type 加 Decimal

**不适用**：
- rust-kql 走 DataFusion plan，不走 SQL——我们的 IR 要"接近 SQL 关系代数"，与 rust-kql AST 定位不同
- 字符串列引用（rust-kql 单后端可接受，我们多后端必须 ColID）
- 能力位（rust-kql 无，我们从零设计）

## 4. 修订 I1 文档的具体建议

1. **I1.S1**：Source 从"表名"升级为接口/枚举，预留 Datatable/Print/Range/Union 子源（参考 rust-kql ast.rs:17-26）。MVP 实现表名（Reference）即可。
2. **I1.S2**：Expr 字面量字段用指针 + nullable 表达 KQL null（参考 rust-kql Literal Option 包裹）。
3. **I1.S2**：Type 加 Decimal 类型。
4. **I1.S3**：明确"物理列 ID 是本项目相对 rust-kql 的关键改进"——因多后端。
5. **I1.S4**：明确"能力位是本项目新增，rust-kql/kqlparser 均无对应"。
6. **风险补充**：rust-kql 字符串操作符（has/contains）在 Expr 中怎么表达需要查 parser.rs（未来 F3 阶段补查）。
