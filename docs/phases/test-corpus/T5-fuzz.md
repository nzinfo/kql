# T5 — 大语料 fuzz/解析压力测试

> 范围：fuzz/stress 测试
> 依赖：F4、T2
> 阶段目标：验证不 panic + 不漏算子，不验证结果

## 顺序化子目标

### T5.S1 — 完整语料接入
- 产出：把 kql-parser 完整 fuzz/large corpus 作为压力输入。
- 验收：1000+ 条语料可加载。
- 测试来源：kql-parser 语料（脱敏后）。

### T5.S2 — 解析压力断言
- 产出：只断言：解析不 panic；能识别的算子全部识别；不能识别的优雅报错（带 code）。
- 验收：无 panic；未知算子有 KQL 错误码。
- 测试来源：T5.S1 语料。

### T5.S3 — 覆盖率统计
- 产出：P0 算子在语料中的覆盖率报告。
- 验收：覆盖率报告生成；P0 算子覆盖率 ≥90%。
- 测试来源：T5.S1 + 算子识别日志。

### T5.S4 — go fuzz 集成
- 产出：`internal/frontend/parser/fuzz_test.go`（go test -fuzz 随机变异）。
- 验收：fuzz 跑 N 次无 panic。
- 测试来源：go fuzz 引擎。

### T5.S5 — 性能压测
- 产出：lexer/parser 吞吐 benchmark（与 F1.S6 / O5 关联）。
- 验收：吞吐达基线。
- 测试来源：T5.S1 语料。

## 阶段产出物
- fuzz/stress 测试
- 覆盖率报告
- 性能 benchmark

## 风险与对策
| 风险 | 对策 |
|---|---|
| 语料含未实现 P2 算子导致大量"未知" | T5.S3 区分 P0/P1/P2 覆盖率 |
| fuzz 耗时 | T5.S4 限时 + corpus 种子 |
| 覆盖率统计口径 | 按"识别成功"而非"语义正确" |
