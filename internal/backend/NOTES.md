# Backend 实现笔记

> 持久化后端线（B1/B2/B5）实现决策与坑。新发现随时追加。

## 1. 设计要点

- **后端直接消费 IR Pipeline**（跳过 B1 的 PhysicalPlan/Optimizer 耦合）—— e2e 最小闭环
  的刻意简化（见 docs/PROGRESS.md §2 / 用户方向"先打通最小闭环"）。当 optimizer 落地后，
  后端会改为接 PhysicalPlan；emit 逻辑能自然组合进去。
- **sqlite 驱动选 modernc.org/sqlite（纯 Go）**，不是 DESIGN.md §7 写的 mattn/go-sqlite3 (cgo)。
  理由：e2e 验证环要免 cgo、可交叉编译、零工具链门槛。production 可后续用 build tag 切到 mattn。
  参考搜索结论：modernc 略慢但纯 Go，mattn 快但要 cgo。

## 2. 关键坑（防再犯）

### 2.1 字面量必须 unquote 再绑定 ⚠️
`ast.BasicLit.Value` 是**原始源文本**（含引号）。`"TEXAS"` 的 Value 是 `"TEXAS"`（带双引号）。
若直接绑定，SQL 比较 `"state" = ?` 会拿 `TEXAS` 去比 `"TEXAS"`（带引号的串）→ 永远 0 行。
**修**：`ir/translate_expr.go` 的 `unquoteString` 剥引号 + 解转义（普通/verbatim/h 前缀都处理）。
INT/REAL 用 strconv 解析成 int64/float64 再绑（让驱动拿到正确类型）。

### 2.2 嵌套子查询的参数顺序 ⚠️⚠️（最深的坑）
每个 stage 包裹前一个，导致**后处理的 stage 的占位符在最终 SQL 文本里更靠左**。
- 例：`where > 500 | extend * 2 | take 1`
- 处理顺序：where(500), extend(2), limit(1) → 朴素 append 得 `[500, 2, 1]`
- 文本占位符顺序：`extend * ?`, `where > ?`, `LIMIT ?` = 需要 `[2, 500, 1]`
- 简单反转（`[1,2,500]`）也不对，因为 LIMIT 是后缀（`SELECT * FROM (...) LIMIT ?`），
  其占位符在最右，而 extend 是前缀包裹。

**正确解法：用 SQLite 编号占位符 `?1 ?2 ?3`**，而非顺序 `?`。每个字面量在 emit 时拿到一个
递增的稳定索引，arg 存进 map[idx]→value，最终按 idx 排序输出。这样**与 SQL 文本顺序无关**，
不管 stage 怎么嵌套都对。见 `emit.go` 的 `emitter.bind/orderedArgs`。

教训：顺序 `?` 占位符只适合"线性拼接"的 SQL；一旦有子查询嵌套/包裹，必须用编号占位符
（pg 的 `$1/$2`、sqlite 的 `?1/?2`）或重新排序 arg。编号占位符最稳。

### 2.3 字符串操作符的 LIKE 映射与 `%` 格式化陷阱
`has`/`contains` → SQLite `LIKE ('%' || ? || '%')`（默认 ASCII 大小写不敏感，与 KQL 接近）。
**坑**：`fmt.Sprintf("%s LIKE ('%' || ...")` 会把字面 `%` 当格式化动词 → vet 报 `%' 未知动词`。
**修**：含字面 `%` 的 SQL 片段用字符串拼接，不用 Sprintf。

### 2.4 count() → COUNT(*)；count(x) → COUNT(x)
sum/avg/min/max 等聚合名直接 UPPER 后透传，SQLite 原生支持。bin(col, span) 用数值近似
`(col / span) * span`（datetime binning 待 T 线真数据 refine）。iff → CASE WHEN。

## 3. 待办（依赖下游）

- **F5 binder 接入**：ColID 绑定后，列引用从字符串名改走 ColID（避免大小写/保留字撞名）。
- **F7 builtin 接入**：FuncCall.Caps 决定每个函数走 SQL expr / UDF / 客户端 post-process。
- **Optimizer 接入**：stage 合并（相邻 SELECT 合一）、谓词下推、CTE 断点。
- **pg/duckdb 后端**：复用 emit 结构，换占位符风格（`$1`）、类型映射、方言。

## 4. PostgreSQL 后端（B2）

**结构**：`internal/backend/pg/`（emit/emit_expr/backend/schema），镜像 sqlite 的
嵌套子查询骨架，两处方言差异：
- 占位符 `$N`（pg 风格，sqlite 是 `?N`）；同样用编号占位符避免嵌套参数错序。
- 字符串操作符用 **ILIKE**（真大小写不敏感，比 sqlite 的 ASCII-only LIKE 更接近 KQL）。
- 函数重写：`ago`→`now() - (x)::interval`、`make_set`→`array_agg(DISTINCT)`、
  `array_length`→`array_length(x,1)`、member 访问→`->>`（JSONB）、index→`->`。

**SchemaProvider**：从 `information_schema.columns` 读列（pg 原生，比 PRAGMA 跨方言）。

**接线**：`pkg/kql.Exec` 按 dsn scheme 路由（`postgres://`→pg，其余→sqlite）。
CLI 的 run/explain 都走 `kql.OpenBackend` 统一路由。

**Docker 复现**：`docker-compose.pg.yml`（postgres:16，:5433，kql/kql）+
`testdata/pg-seed.sql`。`KQL_PG_DSN=... go test -run TestPg_` 跑 pg e2e（无 DSN 则 skip）。

## 5. pg 关键坑（防再犯）

### 5.1 Emit 必须 newEmitter()，不能 &emitter{} ⚠️
`Emit` 第一版写 `e := &emitter{}`，`args` map 是 nil → `bind` 写 nil map → panic。
必须 `newEmitter()` 初始化 `args: map[int]interface{}{}` 和 `postProc`。
（sqlite 包用 `newEmitter()` 没这问题，照抄时漏了。）

### 5.2 pg 大小写折叠（case folding）—— ColID 绑定的核心理由 ⚠️⚠️
pg 把**未加引号的**标识符折叠成小写：`CREATE TABLE events (EventType TEXT)` 实际存成
`eventtype`。KQL 标识符大小写敏感，所以 `where EventType has "x"` 在 pg 上：
- binder（从 information_schema 读到 `eventtype`）报 `column "EventType" not found`
- 或运行时报 `column "eventtype" does not exist`
这正是 DESIGN §5 说要用 **ColID 物理绑定**而非字符串列名的核心理由——多方言后端的
大小写/保留字/引号差异只能靠整数 ID 抽象掉。当前最小版 emit 仍用字符串列名，pg 的
case-folding 是**已知限制**（测试用存储后的小写名绕过）。真解：binder 给每个列引用
解析出 ColID，emit 按 ColID 输出方言正确的引用名。

### 5.3 iff/CASE 的 mixed-type 分支绑定 ⚠️（待修）
`iff(damage > 2000, 1, 0)` → `CASE WHEN ... THEN $1 ELSE $2 END`，pgx 报
`unable to encode 1 into text format (OID 25)`。pg 从 CASE 上下文推断结果列为 text，
而绑定值是 int。需要给 THEN/ELSE 加显式类型转换或让 binder 给字面量类型信息。
暂记为 follow-up（COUNT/SUM/where 等主路径都正常）。
