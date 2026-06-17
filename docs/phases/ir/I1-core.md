# I1 — IR 核心数据结构

> 范围：`internal/ir/`
> 依赖：F2（AST 节点结构参考）
> 阶段目标：定义 Pipeline / Stage / Expr 接口与基础类型
>
> **校验状态**：已完成对 `rust-kql/kqlparser/src/ast.rs` 校验，详见 `I1-verification.md`。`TabularExpression{source, operators}` 结构验证正确；rust-kql 用字符串列名（单后端可接受），我们多后端必须用 ColID（本项目相对 rust-kql 的关键改进）；能力位是本项目新增，rust-kql/kqlparser 均无。

## 顺序化子目标

### I1.S1 — Pipeline 与 Stage 接口
- 产出：`ir/pipeline.go`（Pipeline{Source, Stages []Stage}）、`ir/stage.go`（Stage 接口 + P0 实现）。
- **Pipeline 结构对齐 rust-kql `TabularExpression{source, operators}`**（ast.rs:11-15）。
- **Source 升级为接口/枚举（校验改，原设计是"表名"）**，参考 rust-kql `enum Source`（ast.rs:17-26）7 变体预留：
  - **MVP 实现**：`SourceTable`（表名/Reference）
  - **预留**：`SourceDatatable`（datatable 字面量表）/ `SourcePrint`（print 单行）/ `SourceRange`（range 序列）/ `SourceUnion`（union 多源）/ `SourceExternalData`（externaldata）/ `SourceFind`（find）
- Stage P0 实现：Filter/Project/Extend/Aggregate/Join/Sort/Limit/Union/Let（与 rust-kql `enum Operator` 31 变体交集）。
- 验收：能构造 `orders | where ... | take 10` 的 IR；每个 Stage 实现 `stage()` 标记 + Pos()；Source 接口允许未来扩展。
- 测试来源：手写构造测试 + rust-kql Operator 对照。

### I1.S2 — Expr 接口与基础实现
- 产出：`ir/expr.go`（Expr 接口 + Lit/Col/BinOp/UnaryOp/FuncCall/Agg/Case/Member）。
- **类型系统补 Decimal（校验补，对齐 rust-kql `enum Type`:9-97）**：bool/int/long/real/**decimal**/string/datetime/timespan/dynamic。
- **Literal 用指针/nullable 表达 KQL null（校验补，对齐 rust-kql `enum Literal`:99-110 的 Option 包裹）**：Lit 结构的字段用指针或额外 HasValue bool；KQL 的 null 字面量（如 `iff(x, null, 1)`）必须有显式表达。
- 验收：表达式节点携带类型信息（I1 阶段先用占位，类型由 binder 传入）；Col 携带 ColID；null 字面量可表达。
- 测试来源：手写 + rust-kql Literal 对照。

### I1.S3 — 列引用与物理列 ID
- 产出：`ir/column.go`（ColExpr{TableID, ColID, Name}；ColID 由 binder 在 F5.S3 绑定）。
- **明确（校验补）**：物理列 ID 是**本项目相对 rust-kql 的关键改进**——rust-kql 用 `Ident(String)` + `Vec<String>` 纯字符串名（ast.rs:67），因为它单后端（DataFusion），无需跨方言。我们多后端（pg/duckdb/sqlite），字符串名会撞上大小写折叠（pg unquoted 折叠为小写）、保留字、引号差异——必须用 ColID。
- 验收：相同列名跨表 ColID 不同；ColID 为整数稳定标识；ColID 在 IR 全链路传递（I2 翻译、O2 重写、B1 emit 都用 ColID 而非字符串）。
- 测试来源：手写。

### I1.S4 — FuncCall 能力位
- 产出：`ir/caps.go`（Caps{SQLExpr, Aggregate, Window, NeedsUDF, NeedsPostProc}），FuncCall 携带 Caps。
- **明确（校验补）**：能力位是**本项目新增**，rust-kql 和 kqlparser 均无对应物。rust-kql 在 planner.rs 直接把所有函数翻译为 DataFusion UDF（单后端，不需要分流），我们多后端必须靠能力位决定"该函数在该后端走 SQL 表达式/UDF/post-process 哪条路径"。
- 能力位填写由 F7.S3（builtin 表）+ I3（规则文档化）共同完成。
- 验收：能力位可在构造时填入；默认值合理（标量函数 SQLExpr=true，聚合 Aggregate=true）；F7 builtin 表的 Caps 能被翻译器读取填入 FuncCall。
- 测试来源：手写 + F7 builtin 表对照。

### I1.S5 — 节点访问器与等价辅助
- 产出：`ir/visitor.go`（Visitor + Walk）+ 类型化访问器（AsBinOp/AsFuncCall/AsAggregate...）。
- 验收：遍历测试覆盖所有节点；访问器避免类型断言散落。
- 测试来源：手写。

## 阶段产出物
- `internal/ir/`（pipeline/stage/expr/column/caps/visitor）
- 构造器单元测试

## 风险与对策
| 风险 | 对策 |
|---|---|
| Stage 接口过宽 | 仅暴露 stage() 标记 + Pos；具体字段在各实现 |
| ColID 在子查询/view 边界失效 | S3 在 view 边界重新绑定（I3 详化） |
| 能力位散落难维护 | S4 集中在 caps.go + F7 builtin 表 |
