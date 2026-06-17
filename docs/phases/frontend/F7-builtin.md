# F7 — 内建函数清单

> 范围：`internal/frontend/builtin/`
> 依赖：无（可并行 F1-F4）
> 阶段目标：维护标量/聚合/窗口函数签名清单，供 binder 类型推断与 IR 能力位使用
>
> **校验状态**：已完成对 `kqlparser/builtin/functions.go` 校验，详见 `F7-verification.md`。实测 386 个函数（与"380+"吻合），签名质量好，分类应从 9 类改为对齐 kqlparser 的 **23 类**，**能力位是本项目新增**（kqlparser 无对应物）。

## 顺序化子目标

### F7.S1 — 函数签名数据结构
- 产出：`builtin/signature.go`。
- **Signature 字段（校验调整，对齐 `kqlparser/builtin/functions.go:10-17` 的 FunctionSymbol + 新增 Kind/Caps）**：
  - Name string
  - Params []*Param（具名参数 + 类型 + IsOptional）
  - ReturnType types.Type
  - IsVariadic bool（变长参数，如 strcat）
  - **Kind FuncKind**（校验新增，kqlparser 用"构造器名"区分，我们用显式字段）：Scalar / Aggregate / Window
  - **Caps Capabilities**（校验明确：本项目新增，kqlparser 无对应物，见 F7.S3）
- 构造器：NewScalar/NewAggregate/NewWindow/NewVariadic，对齐 kqlparser 风格。
- 验收：能表达 count()/sum()/bin()/now()/iff()/percentile() 等的签名差异。
- 测试来源：手写。

### F7.S2 — 从 kqlparser 抽取函数清单
- 产出：`builtin/functions.go`（386 个函数）+ 按类别分文件 `builtin/<category>.go`。
- **类别（校验改：对齐 `kqlparser/builtin/functions.go` 的 23 类注释分隔，原 F7 列的 9 类太粗）**：
  1. String（functions.go:21）/ 2. Base64 编码（:123）/ 3. Parse（:145）/ 4. DateTime（:159）/ 5. Bin（:232）/ 6. TimeSpan（:242）/ 7. Math（:254）/ 8. Binary 位运算（:312）/ 9. TypeConversion（:328）/ 10. NullEmpty 处理（:341）/ 11. Format（:368）/ 12. Column（:375）/ 13. Row（:384）/ 14. Table/Database（:406）/ 15. Array/Dynamic（:415）/ 16. IP（:510）/ 17. Hash（:572）/ 18. GUID（:577）/ 19. Geo（:581）/ 20. Special（:721）/ 21. StatisticalAggregates（:829）/ 22. tdigest/hll（:954）
- **抽取脚本工作量**：声明式 Go，可用 `go/ast` 解析或正则，**0.5-1 天**。
- 验收：386 个函数全部抽到；高频函数（count/sum/avg/min/max/now/datetime/isnull/iff/bin/strcat/substring/todouble/tobool）签名正确；按类别文件组织。
- 测试来源：与 kqlparser 清单交叉核对（grep -c 验证 386）。

### F7.S3 — 能力位标注（本项目新增，分阶段）
- 产出：每个函数 Caps 字段标注（CanFoldToSQL / NeedsUDF / NeedsPostProc）。
- **重要（校验明确）**：kqlparser **完全没有能力位概念**，这是本项目新增工作量。估算 **1-2 天集中标注**。
- **分阶段策略**：
  - **P0 优先（MVP 必需）**：count/sum/avg/min/max/bin/now/datetime/isnull/iff/strcat/substring/todouble/tobool/isempty ~15 个高频函数三后端能力位优先标完
  - 其余按需补：在某个函数被实际查询用到时再补
- **三后端能力矩阵参考**（pg 主）：
  - pg：`percentile_cont`/`percentile_disc` 有 → CanFoldToSQL；`series_*` 无 → NeedsUDF
  - duckdb：`quantile_cont`/`quantile_disc`/`median` 有；聚合比 pg 全
  - sqlite：window/series 缺 → NeedsPostProc（客户端补算）
- 验收：P0 ~15 个高频函数能力位正确；能力位矩阵文档 `docs/capabilities.md` 落地。
- 测试来源：手写 + 三后端实际验证（B6 阶段联调）。
- 详化在 I3 阶段（能力位规则文档化）。

### F7.S4 — 查询接口
- 产出：`builtin/registry.go`：
  - **`map[string]*Signature`**（校验改：用 map 实现 O(1) Lookup，kqlparser 用 slice 全局变量 + 线性匹配）
  - `Lookup(name) (*Signature, bool)`
  - `Aggregates() []*Signature` / `ByCategory(cat) []*Signature`（保留 slice 用于按类别迭代）
- 验收：binder（F5.S6）通过 Lookup 解析函数；未知函数返回 false；O(1) 查找。
- 测试来源：手写 + 集成。

### F7.S5 — 函数表测试与文档
- 产出：`builtin/functions_test.go`（每类抽 3-5 个代表函数验证签名）+ Markdown 函数清单（按类别）。
- 验收：函数清单文档可作用户参考。
- 测试来源：手写。

## 阶段产出物
- `internal/frontend/builtin/`（signature.go + 23 个按类别文件 + registry.go）
- `docs/capabilities.md`（能力位矩阵，与 I3 共建）
- 函数清单 Markdown 文档（按 23 类）

## 风险与对策
| 风险 | 对策 |
|---|---|
| 函数签名漂移（KQL 演进） | S2 标注来源版本；定期对照官方文档 |
| **能力位工作量被低估**（校验新增） | S3 分阶段：P0 ~15 个优先，其余按需补；预算 1-2 天 |
| 清单过大难维护 | S2 按 23 类分文件组织 |
| 与 binder 耦合 | S4 只暴露查询接口，不暴露内部结构 |
| **能力位规则主观**（校验新增） | I3 阶段决策表 + 三后端评审固化到 docs/capabilities.md |
