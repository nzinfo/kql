# F7 认知校验报告（对照 kqlparser builtin）

> 校验对象：F7-builtin.md
> 参考：`/home/nzinfo/src.erp-ext/kql/kqlparser/builtin/functions.go`（988 行，386 个函数）

## 1. 校验结论

**认知高度成立**。386 个函数与"380+"吻合，签名质量好（参数名/类型/可选/变长都齐全），可直接复用。**关键偏差：kqlparser 无能力位（Caps）字段**，需要我们新建——这点 F7.S3 已预留，但要明确工作量。

## 2. 逐条校验表

| 校验点 | 我们认知 | kqlparser 实际（位置） | 偏差 |
|---|---|---|---|
| F7.S2 函数数量 | "380+" | **实测 386**（`grep -c "NewScalarFunction\|NewVariadicFunction\|NewAggregateFunction" functions.go`） | 数字准确 |
| F7.S1 Signature 结构 | {Name, Params, ReturnType, Kind, Caps} | `symbol.FunctionSymbol`（含 Name/Params []*Parameter/ReturnType/IsVariadic 等，构造器：NewScalarFunction/NewVariadicFunction/NewAggregateFunction） | **差**：kqlparser 无 Kind（标量/聚合/窗口）显式字段，靠"用哪个构造器"区分；无 Caps 字段 |
| F7.S2 分类 | 聚合/字符串/数学/日期/数组/类型转换/逻辑/IP/地理 | 实测 **23 类**（functions.go:21-954 注释分隔）：String/Base64/Parse/DateTime/Bin/TimeSpan/Math/Binary/TypeConversion/NullEmpty/Format/Column/Row/Table/ArrayDynamic/IP/Hash/GUID/Geo/Special/StatisticalAggregates/tdigest_hll | **补**：我们的分类太粗，应对齐 23 类 |
| F7.S3 能力位 | CanFoldToSQL/NeedsUDF/NeedsPostProc | **kqlparser 完全没有** | **重大**：能力位需从零设计，kqlparser 无法复用。F7.S3 是新增工作量 |
| F7.S4 查询接口 | Lookup(name) | 通过 `symbol` 包的全局变量（ScalarFunctions/Aggregates）+ 名称匹配 | **借鉴**：可改 map[name]Signature 加速 O(1) |
| 签名数据质量 | 参数/返回类型完整 | functions.go:22-每个函数都有 returnType + 具名参数 + 类型 + 可选标记（param/optParam） | **优秀**，可直接抽 |

## 3. 抽取工作量评估

- **抽取脚本难度：低**。functions.go 是纯声明式 Go，386 行 `symbol.NewXxxFunction(name, retType, params...)` 模式，可用 go/ast 解析或正则抽取。
- **签名补全工作量：低**。kqlparser 已有完整参数类型，只需复制。
- **能力位标注工作量：中**。386 个函数每个都要判断 pg/duckdb/sqlite 三后端能否纯 SQL 表达——这是 F7.S3 + I3 的核心工作，**估算 1-2 天集中标注**（高频函数 ~50 个优先，其余按需补）。
- **聚合函数数量**：functions.go:829-953 "Statistical aggregates" 段约 30+ 个（count/sum/avg/min/max/percentile*/stdev/variance/arg_max/arg_min/make_set/make_list/...），与 DESIGN.md 第 10 节 MVP P0 所需（count/sum/avg/min/max）够用且富余。

## 4. 修订 F7 文档的具体建议

1. **F7.S2**：把分类从 9 类改为对齐 kqlparser 的 23 类（直接搬注释分隔）。
2. **F7.S1**：Signature 结构对齐 `symbol.FunctionSymbol` 字段集（Name/Params/ReturnType/IsVariadic/IsOptional 等），加 Kind 字段（Scalar/Aggregate/Window）—— 用 Kind 而不是"构造器名"区分。
3. **F7.S3**：明确"能力位是本项目新增，kqlparser 无对应物"，标注工作量；优先标注 P0 用到的函数（count/sum/avg/min/max/bin/now/datetime/isnull/iff/strcat/substring/todouble/tobool ~15 个）。
4. **F7.S4**：把全局变量改为 `map[string]*Signature` 以支持 O(1) Lookup；保留原始 slice 用于按类别迭代。
5. **F7.S5**：函数清单文档可直接用 kqlparser 的分类注释结构生成。
