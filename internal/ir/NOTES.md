# IR 实现笔记（Translator Alignment Notes）

> 持久化 IR 线（I1/I2）的实现决策与坑。新发现随时追加。
> 语法对齐仍以 `internal/frontend/NOTES.md` 为准；本文件聚焦 IR/翻译层。

## 1. 设计要点（对齐 DESIGN.md §5 + docs/phases/ir/）

- **IR 是内部中间表示，不是运行时产物**：运行时输出永远是后端生成的 SQL。
  IR 的 pretty-print/YAML 仅用于 `kql explain` 与 golden 快照。
- **ColID 而非字符串列名**（I1.S3）：本项目相对 rust-kql（用 `Ident(String)`，
  单后端可接受）的关键改进。多后端（pg/duckdb/sqlite）字符串名会撞大小写折叠、
  保留字、引号差异，必须用稳定整数 ColID。
- **能力位 Caps**（I1.S4）：rust-kql/kqlparser 均无，本项目新增。决定函数在每个后端
  走 SQL 表达式 / UDF / 客户端 post-process 哪条路径。

## 2. 已实现（I1 + I2 P0）

### I1 核心数据结构 ✅
- `Pipeline{Source, Stages, Position}` —— 对齐 rust-kql `TabularExpression`。
- Source 接口：`SourceTable`（MVP 实现）+ `SourceDatatable/Print/Range`（预留 stub）。
- Stage P0：Filter/Project/Extend/Aggregate/Join/Sort/Limit/Union/Distinct。
- Expr：Lit（HasValue=false 表 null，对齐 rust-kql Option 包裹）/Col/Star/BinOp/UnaryOp/
  FuncCall/Member/Index/Case。
- Type 含 Decimal（对齐 rust-kql enum Type，原 I1 设计漏了 Decimal）。
- Caps{SQLExpr,Aggregate,Window,NeedsUDF,NeedsPostProc} + DefaultCaps。
- Visitor + BaseVisitor（Walk 处理 typed-nil 指针，见 §3）。

### I2 AST→IR 翻译器（P0）✅
- `Translate(ast.Node, *diagnostic.List) → *Pipeline`，接 Script/QueryStmt/Pipeline。
- 表达式翻译全覆盖（translateExpr），含 between→AND(>=,<=)、cast→to_<type> FuncCall。
- P0 算子全覆盖；**top 特殊**：一个 AST op 展开为两个 IR stage（Sort + Limit）。
- 列引用 **ColID=Invalid 占位**（Name 保留），等 F5 binder 回填（见 PROGRESS.md §2）。
- FuncCall Caps 用 DefaultCaps，等 F7 builtin 表回填。
- count() 翻译为 Aggregate（summarize count() 的等价）；顶层 `| count` 同。

## 3. 关键坑（防再犯）

### 3.1 IR 节点字段不能叫 `Pos` / `Type` ⚠️
Node 接口要 `Pos()` 方法、Expr 接口要 `Type()` 方法，所以**结构体字段不能用同名**，
否则 "field and method with the same name" 编译错。
**约定**：位置字段一律叫 `Position`，类型字段一律叫 `T`。方法 `Pos()`/`Type()` 返回它们。
（前端 ast 包没这问题，因为它的节点字段叫 `Pipe`/`ValuePos` 等带前缀名。）

### 3.2 Walk 必须处理 typed-nil 指针 ⚠️
`*Pipeline` 字段为 nil 时，作为 Node 接口传给 Walk 是**非 nil 接口**（带类型信息），
`node == nil` 判断为 false → `v.Visit(node)` 解引用 panic。
**修**：`isNilPointer` 用 reflect 检查 `Kind()==Ptr && IsNil()`。
（Go 接口的经典坑：nil *T ≠ nil interface。）

### 3.3 top 一个 AST op → 两个 IR stage
`| top N by k desc` = `| sort by k desc | take N`。translatePipeline 里对 `*ast.TopOp`
特殊处理，调 `translateTopOp` 返回 `[]Stage{Sort, Limit}`。不要试图塞进一个 stage。

## 4. 待办（依赖下游）

- **F5 binder 接入后**：翻译器把 ColID 占位换成真实绑定（ColID 有效）。
  目前 Col.ColID.IsValid() 全是 false，Name 是字符串。
- **F7 builtin 接入后**：FuncCall.Caps 从 DefaultCaps 换成查表结果。
- **I3 能力位文档化**：哪些函数 NeedsUDF/NeedsPostProc（percentile/series_* 等）。
- **I4 pretty-print**：给 `kql explain` 和 golden 用。
- **I5 等价测试**：重写前后 IR 跑同 SQL 同结果。
