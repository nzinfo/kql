# Arrow 多引擎执行管道 — 架构设计

> 2026-06-17 创建。Step 0（原型验证）已完成。

## 目标

每个 IR stage 路由到最擅长的引擎，Arrow 做零拷贝中间格式：

```
events (pg)
  → WHERE timestamp > ago(1h)        [pg 执行: 索引扫描, 返回少量行]
  → Arrow batch                       [行→列转换 (有成本但行少)]
  → DuckDB RegisterView               [零拷贝注册]
  → summarize avg() by region         [DuckDB 执行: 向量化聚合]
  → Arrow batch                       [结果]
```

## 技术验证结论

### pg × Arrow

| 路径 | 零拷贝 | Go 可用 | 评估 |
|---|---|---|---|
| pgx → 手动构建 Arrow | ❌ 行→列重排 | ✅ 纯 Go | **首选**（已有 pgx） |
| Arrow ADBC Driver | ⚠️ 至少 1 次转换 | ⚠️ 需 CGO/libpq | ❌ 引入 CGO |
| Arrow Flight SQL | ⚠️ 早期 | ❌ 无 Go 客户端 | ❌ 需额外部署 |

**关键限制**：pg 是行式存储，wire protocol 传输行格式。pg→Arrow 转换成本 = f(行数)。
对策：pg 段先 filter（减少行数），DuckDB 段做重计算（向量化优势）。

### DuckDB × Arrow

| 能力 | 状态 | API |
|---|---|---|
| DuckDB→Arrow 零拷贝查询 | ✅ 验证通过 | `duckdb.NewArrowFromConn` → `QueryContext` → `RecordReader` |
| Arrow→DuckDB RegisterView | ✅ 验证通过 | `ar.RegisterView(reader, name)` → SQL 可查询 |
| columnar.Record→Arrow | ✅ 验证通过 | `Record.ToArrow(allocator)` → `RecordBatch` |

**已知限制**：DuckDB C Data Interface 的 RecordReader 在 `Next()` 调用间重用内部 buffer。
数值类型稳定；字符串类型需要在 RecordBatch 存活时立即提取值。
RegisterView 路径不受影响（DuckDB 内部处理 buffer 生命周期）。

## 实现步骤

### Step 0 — 原型验证 ✅ (已完成)

- `backend/arrowbackend.go`: `ArrowBackend` 可选接口
- `backend/duckdb/arrow.go` (`-tags duckdb_arrow`): `ExecArrow` + `RegisterArrowView`
- `columnar/arrow_bridge.go` (`-tags duckdb_arrow`): `Record.ToArrow` + `RecordBatchToRows`
- 4/4 测试通过：Arrow 查询、round-trip、列名提取、RegisterView

### Step 1 — Arrow 作为 exec 中间格式 (下一步)

ExecPipeline 检测 ArrowBackend → Arrow RecordReader 在 stage 间传递。
- `exec/exec_arrow.go`: ArrowResult + Arrow PostProc
- DuckDB + Arrow path vs row path 结果等价性测试

### Step 2 — Engine Router

`exec/router.go`: stage 级多引擎路由。
- Filter on indexed column → pg
- Aggregate on large data → DuckDB
- pg 结果 → columnar.Record → Arrow → DuckDB RegisterView → DuckDB 下一段

### Step 3 — 优化器集成

扩展 O4 AltPlan：每 stage 枚举引擎（pg/DuckDB/client），cost-based 选择。
cost model 加入行→列转换成本。

## 构建标签

```sh
# 无 Arrow（默认，零回归）
go build ./...

# 有 Arrow（DuckDB 零拷贝）
go build -tags duckdb_arrow ./...
go test -tags duckdb_arrow ./internal/backend/duckdb/ -run TestArrow_
```

## 文件清单

| 文件 | 构建标签 | 用途 |
|---|---|---|
| `internal/backend/arrowbackend.go` | 无 | ArrowBackend 接口（类型定义，无运行时依赖） |
| `internal/backend/duckdb/arrow.go` | duckdb_arrow | ExecArrow + RegisterArrowView |
| `internal/backend/duckdb/arrow_test.go` | duckdb_arrow | 原型测试 |
| `internal/columnar/arrow_bridge.go` | duckdb_arrow | Record↔RecordBatch 桥接 |
