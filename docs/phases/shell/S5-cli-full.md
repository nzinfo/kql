# S5 — CLI 输出格式 + Explain 子命令

> 范围：`cmd/kql/`
> 依赖：S3、S4、O3（Explain）、O5（代价对比）
> 阶段目标：完善 CLI 用户体验

## 顺序化子目标

### S5.S1 — 输出格式
- 产出：`cmd/kql/output.go`（arrow/csv/parquet/json；默认 csv）。
- 验收：各格式输出正确；大结果集流式不 OOM。
- 测试来源：手写 + 大结果集压测。

### S5.S2 — Explain 子命令
- 产出：`cmd/kql/explain.go`（输出 IR 树 + 优化前后代价 + 决策 reason + 最终 SQL）。
- 验收：`kql explain -d ... '<query>'` 输出可读；含全部决策信息。
- 测试来源：手写快照。

### S5.S3 — Validate 子命令
- 产出：`cmd/kql/validate.go`（只解析不执行，输出诊断）。
- 验收：`kql validate '<query>'` 输出 diagnostic 列表。
- 测试来源：手写。

### S5.S4 — 统计 catalog 加载
- 产出：`--stats <path>` 选项（加载 O0 catalog；缺省走空 catalog）。
- 验收：指定 catalog 后优化器代价感知生效。
- 测试来源：手写 + O0 示例。

### S5.S5 — decision policy 切换
- 产出：`--policy conservative|aggressive|gated` 选项。
- 验收：切换 policy 后 Explain 显示不同决策。
- 测试来源：手写 + O4 join 案例。

### S5.S6 — CLI 帮助与文档
- 产出：`--help` 输出 + `cmd/kql/README.md`。
- 验收：帮助完整；含示例。
- 测试来源：手写。

## 阶段产出物
- `cmd/kql/{output,explain,validate}.go`
- CLI README

## 风险与对策
| 风险 | 对策 |
|---|---|
| 大结果集 OOM | S1 流式输出 |
| Explain 输出冗长 | S2 提供 verbose/compact 模式 |
| 选项爆炸 | S6 分组（输入/输出/优化/调试） |
