# CLAUDE.md

> 本文件给 Claude（及任何 AI 协作者）提供本仓库的快速导航与约定。
> 权威设计见 `DESIGN.md`，分阶段实施细节见 `docs/phases/`。

## 1. 这是什么

自研一套 **Kusto Query Language (KQL)** 的解析器与执行器（Go 实现）。

- **CLI**：`kql -d <dsn> -f csv/parquet 'KQL...'`
- **嵌入式库**：`kql.Exec(ctx, dsn, query, params) → arrow.Record`

Go module：`nzinfo/kql`（见 `go.mod`）。当前尚无 `.go` 源码，处于脚手架/文档阶段。

## 2. 目录布局（当前实际状态）

```
kql/
├── DESIGN.md                 # 权威设计文档（必读）
├── claude.md                 # 本文件
├── go.mod                    # module nzinfo/kql
│
├── cmd/kql/                  # CLI 入口（待实现）
├── internal/                 # 核心实现，对外不暴露
│   ├── frontend/{token,lexer,parser,ast,binder,diagnostic,builtin}/
│   ├── ir/
│   ├── optimizer/{stats,rules,cost,decision}/
│   ├── backend/{pg,duckdb,sqlite}/
│   └── exec/
├── pkg/                      # 公开 API（kql.Exec / Explain / Validate）
├── stats/                    # 预定义统计描述 YAML（见 stats/README.md）
├── testdata/corpus/          # 测试语料
├── docs/phases/              # 分阶段实施文档（frontend/ir/optimizer/backend/shell/test-corpus）
│
└── .source-projects/         # ⚠ 第三方参考项目，见下节
```

### 注意：顶层空目录

顶层另有 `backend/ frontend/ ir/ optimizer/ shell/ test-corpus/` 等**空目录**，
与 `internal/` 下同名子目录重复（疑似早期脚手架残留）。
**新代码请放入 `internal/` 下的对应子目录**，不要往顶层空目录里写。
顶层空目录是否清理待定，先保留以免误删。

## 3. `.source-projects/` —— 第三方参考项目（只读）

这些是**独立的 git 仓库**（各自带 `.git`），仅作设计与实现参考，
**不是** `nzinfo/kql` 的一部分，**不被本模块 import**，互不干扰。

| 目录 | 语言 | 角色 | 用途 |
|---|---|---|---|
| `.source-projects/Kusto-Query-Language/` | C#（官方） | **语法金标准（最高优先级）** | `grammar/Kql.g4` + `grammar/KqlTokens.g4` 是一切语法/词法争议的唯一权威 |
| `.source-projects/kqlparser/` | Go（手写） | 工程分层范本 | lexer/parser/ast/binder 分包结构、`Reset(offset)`、`File/Pos/Position` 三层抽象；`builtin/functions.go` 380+ 函数清单 |
| `.source-projects/kql-parser/` | Go（ANTLR） | 语料来源 | `fuzz_corpus_test.go` 真实 Sentinel 语料回归集；`extractor.go` 提取参考 |
| `.source-projects/rust-kql/` | Rust | 翻译思路 | `datafusion-kql/planner.rs`：AST→执行 plan（我们走 SQL，仅借鉴） |

### ⚠ 语法对齐原则（写前端代码前必读）

1. **金标准永远优先**：实现任何词法/语法规则前，先查 `Kusto-Query-Language/grammar/KqlTokens.g4`（词法）或 `Kql.g4`（语法）。
2. **kqlparser 只是模板，不是语法权威**：它的分层结构和接口可以借鉴，但**语法细节必须回 g4 校验**。模板与 g4 冲突时，**改模板的做法，不改 g4**。
3. 已发现的模板偏差与修正，记录在 `internal/frontend/NOTES.md` —— 实现前端前先读这个文件，避免重蹈覆辙。

**约定**：
- 这些目录**只读**，不要修改其中的代码或 git 状态。
- 不要把它们加入 `nzinfo/kql` 的 import 路径或 go.sum。
- 需要复用其中内容（如函数清单、测试语料）时，**复制**到本仓库的 `internal/frontend/builtin/` 或 `testdata/corpus/` 下，并注明出处。
- 各自分支：`kqlparser`=main、`kql-parser`=main、`rust-kql`=main、`Kusto-Query-Language`=master。

> 已知状态：`kql-parser` 的 git 索引在迁入前即为空（`git ls-files` 返回 0，
> 工作区文件物理存在但相对 HEAD 显示为 staged deletion）。这是该仓库**原有**状态，
> 非本次移动造成；文件未丢失，作为只读参考不受影响。

## 4. 开发约定

- **实现语言**：Go（`go.mod` 声明 `go 1.22`，本机 `go 1.26.4` 可用）。
- **分层**：严格按 `internal/` 锁死，对外只暴露 `pkg/`。
- **前端**：手写递归下降（**不**用 ANTLR），语法以 `Kusto-Query-Language/grammar/Kql.g4` 为**唯一权威基准**。⚠ kqlparser 只是分层模板，语法细节要回 g4 校验，见 §3 与 `internal/frontend/NOTES.md`。
- **后端**：PostgreSQL 为 pg 主；sqlite/duckdb 为辅。运行时产物是 **SQL 文本**，不是 DataFusion plan。
- **优化器**：两段式（规则重写 + 代价选择），统计描述走外部 YAML（`stats/`），不依赖运行时查系统表。
- **测试**：语料放 `testdata/corpus/`，golden 快照随实现补。
- **认知持久化**：实现过程中发现的"来之不易的认知"（语法对齐、模板偏差、设计决策）必须写进对应模块的 `NOTES.md`，避免上下文摘要后丢失。前端笔记在 `internal/frontend/NOTES.md`。

## 5. 实现进度

> 按 `docs/phases/README.md` 的 Wave A→G 推进。详见各模块 `NOTES.md`。

### 前端线（F1–F7）
| 阶段 | 状态 | 产出 |
|---|---|---|
| F1 词法层 | ✅ 完成 | `token/`（枚举+Position+大小写不敏感 Lookup）、`lexer/`（金标准对齐、~120 MB/s）、benchmark |
| F2 AST 骨架 | ✅ 完成 | `ast/`（Node/Expr/Stmt/Operator 接口 + P0 节点 + Visitor）、测试 |
| F3 parser 表达式 | ✅ 完成 | `parser/`（g4 优先级阶梯、save/restore 回溯）、`diagnostic/`（F6 提前）、测试 |
| F4 parser tabular P0 | ⏳ 待做 | `parser/`（where/project/extend/take/sort/summarize/join/let + Pipeline） |
| F5–F7 | ⏳ 待做 | binder/builtin（diagnostic 已在 F3 完成） |

**下一批**：F4（P0 tabular 算子 + Pipeline 顶层）。语法对齐笔记见 `internal/frontend/NOTES.md`。

## 6. 常用命令

```bash
go build ./...          # 编译全部（前端铺开后有包）
go vet ./...            # 静态检查
go test ./...           # 全量测试
go test ./internal/frontend/...   # 仅前端线
```

## 7. Git

- 远程：`git@github.com:nzinfo/kql.git`（分支 `main`）。
- **提交节奏**：完成一个阶段（F1/F2/…）或一组逻辑改动后及时提交，避免改动堆积。
- `.source-projects/` 已加入 `.gitignore`（本地保留的只读上游仓库，**不提交**）。
- 提交信息约定：`chore:` / `feat(<scope>):` / `fix(<scope>):` / `docs:`，scope 用线名（frontend/ir/optimizer/backend/shell）。
- 语法对齐的认知持久化在 `internal/frontend/NOTES.md`（以及后续各模块的 NOTES.md），提交时一并带上。
