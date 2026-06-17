# S1 认知校验报告（对照 rust-kql kq CLI）

> 校验对象：S1-api-skeleton.md
> 参考：`/home/nzinfo/src.erp-ext/kql/rust-kql/kq/src/main.rs`（56 行，Apache 2.0）

## 1. 校验结论

**我们 S1 设计有过度设计倾向**。rust-kql 的 CLI 极简（56 行，clap + 直接调 datafusion SessionContext），无 Engine 抽象、无 Explain/Validate 子命令、无 dependency injection。我们的 Engine+Options+依赖注入对 MVP 偏重。**建议 S1 简化**：保留公开 API 但内部用简单结构（非 DI 模式），Engine 推迟到 S3 真正多组件协作时再引入。

## 2. 逐条校验表

| 校验点 | 我们认知 | rust-kql 实际（main.rs） | 偏差 |
|---|---|---|---|
| CLI 参数 | `-d <dsn> -f <fmt> <query>` | `-f <file>` 可多个 + 可选 query 参数（main.rs:13-17） | **借鉴**：rust-kql 的 `-f` 是数据源文件（注册到 ctx），无 DSN 概念（因为它内嵌 DataFusion，不连外部 DB）。我们因多后端必须有 DSN |
| CLI 库 | （未指定） | clap（main.rs:1, 12） | 借鉴：Go 用 cobra/spf13 或 urfave/cli |
| 数据源 | DSN | 文件扩展名分发（main.rs:35-46）：arrow/avro/csv/json/parquet/kql | **借鉴**：rust-kql 用文件扩展名决定加载方式；我们用 DSN scheme 决定后端，但**输入数据文件**（csv/parquet）也可类似处理 |
| 公开 API | Exec/Explain/Validate/Optimize | **无抽象**，直接 `state.create_logical_plan_kql(query)` + `ctx.execute_logical_plan(plan)`（main.rs:21-22） | **rust-kql 极简**，两行完成查询。我们设计 4 个 API + Engine |
| Engine | 持有各线组件 + DI | **无 Engine**，DataFusion 的 `SessionContext` 就是事实 Engine（main.rs:31） | **借鉴**：rust-kql 用 DataFusion 现成 ctx 作为 Engine，自己不重造。我们因多后端 + 自研 frontend/ir/optimizer 必须自己有 Engine |
| 子命令 | Explain/Validate | **无**，只有执行 | 我们 Explain/Validate 是新增价值（rust-kql 没有） |
| 错误处理 | KqlError 透出 | `Box<dyn Error>` 直接抛（main.rs:8, 19） | rust-kql 极简，不分类错误。我们要透出 diagnostic code 是必要增量 |
| 输出格式 | arrow/csv/parquet/json | `pretty::print_batches`（main.rs:23）默认表格打印 | rust-kql 输出单一；我们多格式是新增价值 |
| 库 vs CLI | 共享入口 | 库就是 datafusion-kql crate，CLI 是 kq crate（薄壳） | **一致**：thin wrapper 模式正确 |

## 3. rust-kql 可借鉴的做法

- **文件扩展名决定加载方式**（main.rs:35-46）：我们的"输入数据文件"（csv/parquet）加载可借鉴
- **thin CLI wrapper**（kq crate 只有 main.rs 56 行）：CLI 不应承载业务逻辑
- **复用现有 ctx 作为 Engine**（DataFusion SessionContext）：精神上我们也应尽量复用 pgx/duckdb/sqlite 的现成连接池，而非自造

## 4. 我们 S1 是否过度设计

**部分过度**。具体：
- **Exec/Explain/Validate/Optimize 4 个 API**：MVP 只需 Exec；Explain 可后置到 S5；Validate 可用 Exec+特殊模式实现；Optimize 是内部能力不应作公开 API。**建议 S1.S1 简化为 Exec + Explain 两个**。
- **Engine + Options + 依赖注入**：MVP 阶段用简单 struct + 函数式选项即可，DI 推迟到 S3。**建议 S1.S3 简化为 `type Engine struct{ db *sql.DB; opts Options }`**。
- **公开错误类型 KqlError**：保留（rust-kql 的 `Box<dyn Error>` 不够）。
- **公开类型导出**：保留（不暴露 IR/optimizer 内部）。

## 5. 修订 S1 文档的具体建议

1. **S1.S1**：API 从 4 个简化为 2 个（Exec + Explain）；Validate 用 `Exec(ctx, "", query)` 或独立轻量函数；Optimize 改为内部函数不导出。
2. **S1.S2**：Options 简化为核心字段（backend/dsn/statsPath/policy/strict），函数式选项保留。
3. **S1.S3**：Engine 简化为 `struct{ db *sql.DB; stats StatsReader; policy DecisionPolicy; opts Options }`，构造器直接赋值（非 DI 容器）。DI 推迟到真正需要（如多 catalog 切换）。
4. **S1.S5**：公开类型精简——ExplainOutput、KqlError、Options；不导出 StatsCatalog（用接口）。
5. **新增 S1.S6（可选借鉴）**：输入数据文件加载（rust-kql 风格，按扩展名分发 csv/parquet/json 注册到 backend）。
6. **风险补充**：MVP 过度设计风险——rust-kql 56 行能跑，我们 S1 不应超过 ~300 行。
