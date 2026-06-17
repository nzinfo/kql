# S2 — internal/exec Backend 接口 + Arrow 转换

> 范围：`internal/exec/`
> 依赖：S1（用 Engine 结构）
> 阶段目标：统一三后端的执行与结果转换

## 顺序化子目标

### S2.S1 — Backend 接口
- 产出：`exec/backend.go`（Backend 接口：Query(ctx, sql, args, schema) → arrow.Record）。
- 验收：pg/duckdb/sqlite 实现同一接口。
- 测试来源：手写 mock backend。

### S2.S2 — schema 描述
- 产出：`exec/schema.go`（列名/类型，从 IR 推导）。
- 验收：schema 能描述 P0 算子输出列。
- 测试来源：手写 + I3 投影。

### S2.S3 — Arrow 记录构造器
- 产出：`exec/arrow.go`（driver.Rows → arrow.Record）：
  - pg：pgx 批量拉取 → Arrow。
  - duckdb：duckdb-go 原生 Arrow 零拷贝。
  - sqlite：行式拉取 → Arrow。
- 验收：mock backend 返回固定行集 → Arrow；列类型正确。
- 测试来源：手写。

### S2.S4 — 资源管理
- 产出：Record 生命周期 + 释放器（`arrow.Release`）+ context 超时。
- 验收：Record 用完释放；连接池配置合理。
- 测试来源：手写 + leak 检测。

### S2.S5 — 后端注册表
- 产出：`exec/registry.go`（按 scheme 注册 Backend；DSN 解析时查找）。
- 验收：postgres://→pg、duckdb://→duckdb、sqlite://→sqlite。
- 测试来源：手写。

## 阶段产出物
- `internal/exec/`（backend/schema/arrow/registry）
- Arrow 转换测试

## 风险与对策
| 风险 | 对策 |
|---|---|
| Arrow 类型与 SQL 类型不匹配 | S2 类型映射表 + 缺失时报错 |
| 资源泄漏 | S4 释放器 + context 超时 + leak 检测 |
| duckdb 零拷贝与行式接口不一致 | S3 统一为 arrow.Record，内部适配 |
