# F6 — Diagnostic 系统

> 范围：`internal/frontend/diagnostic/`
> 依赖：F1（Position）
> 阶段目标：结构化错误/警告，带 code 与位置，可被 CLI 与库用户渲染

## 顺序化子目标

### F6.S1 — Diagnostic 结构与严重性
- 产出：`diagnostic/diagnostic.go`（Diagnostic{Code, Severity, Pos, Message, Suggestions []string}、Severity: Error/Warning/Info）。
- 验收：结构可序列化；Code 用稳定字符串（KQL000+）。
- 测试来源：手写。

### F6.S2 — 错误码命名空间
- 产出：`diagnostic/codes.go`（错误码常量表：KQL001 未知列、KQL002 类型不匹配、KQL003 未知函数、KQL004 参数数量、KQL005 语法错误、KQL006 算子参数错误、KQL007 作用域、KQL008 资源不存在 等）。
- 验收：每个 code 有 godoc 描述；新增 code 走集中登记。
- 测试来源：手写。

### F6.S3 — 诊断聚合与去重
- 产出：`diagnostic/list.go`（List 排序、按 Pos 去重、按 Severity 过滤）。
- 验收：同位置多条诊断只保留最高严重性；Error 优先于 Warning。
- 测试来源：手写。

### F6.S4 — 渲染器
- 产出：`diagnostic/render.go`（CLI 文本渲染 `file:line:col: KQL001: message`；结构化 JSON 渲染供库用户）。
- 验收：CLI 输出格式与 `go vet`/`rustc` 风格一致；JSON 字段稳定。
- 测试来源：手写快照。

### F6.S5 — 各线接线
- 产出：前端各子包（lexer/parser/binder）使用 diagnostic 而非 error/panic（除不可恢复错误）。
- 验收：T3 P0 全集诊断输出稳定（golden）。
- 测试来源：T3 P0 + golden。

## 阶段产出物
- `internal/frontend/diagnostic/`（diagnostic/codes/list/render）
- 各线诊断输出 golden

## 风险与对策
| 风险 | 对策 |
|---|---|
| 错误码语义漂移 | S2 集中登记 + godoc |
| 诊断爆炸（一处错触发雪崩） | S3 去重 + 同位置合并 |
| 跨线诊断口径不一 | S5 接线时统一用 diagnostic.Diagnostic |
