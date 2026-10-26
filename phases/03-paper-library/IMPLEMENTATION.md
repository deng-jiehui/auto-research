# 阶段 03 实施：论文库与学术工具

> 2026-10-23：已补齐真实 MCP 配置和 Graph 论文工具。下列原始任务中未实现的全文/Artifact/检索增强不计入本轮完成范围，实际结果见 ACCEPTANCE.md。2026-10-24 已另写[全文阅读与证据补齐计划](READING_AND_EVIDENCE.md)，待实施部分以该计划为准。

## 全局约束

- 所有修改留在 `extensions/examples/auto-research/`，不修改 goHarness 核心框架。
- 领域包不导入 HTTP、SQL、MCP 或模型 SDK。
- 论文与证据查询必须带可信 `domain.Scope`；不相信模型传入的项目身份。
- MCP 工具使用现有 `extensions/mcp`，并设置 Strict Policy 和工具白名单；上游工具仅供适配器内部使用。
- 外部来源结果先归一化；来源失败不能静默转为空结果。
- 运行测试使用独立临时数据库；真实 arXiv 联调单独记录，不把 fixture 作为真实兼容性证明。

## Task 1：来源契约和 arXiv MCP 归一化

**文件：**

- Create: `internal/domain/paper_source.go`
- Create: `internal/infra/mcp/catalog.go`, `source.go`, `normalize.go`
- Create: `internal/infra/mcp/source_test.go`
- Create: `testdata/mcp/arxiv-search.json`, `arxiv-metadata.json`, `arxiv-section.json`, `tools.json`
- Modify: `go.mod` only if the existing local MCP extension is not already imported

**行为：**

- `ParseCandidates("arxiv", data)` 读取固定的 `id/title/published/summary/authors/url` 字段，生成规范 Candidate。
- 统一 arXiv 版本键，保留具体版本供文档 Locator 使用。
- MCP Catalog 根据配置建立连接、初始化、发现工具并按白名单映射调用；保存状态和 schema 版本。
- 对工具缺失、无效 JSON、超时和连接失败返回可分类错误。

**验证：** `go test ./internal/domain/... ./internal/infra/mcp/...`；使用 in-process MCP server 验证 tools/list 和只读调用。

## Task 2：项目论文库和 HTTP/工具

**文件：**

- Create: `internal/domain/paper.go`, `paper_identity.go`, tests
- Create: `internal/app/papers.go`, tests
- Create: `internal/infra/sqlite/migrations/005_papers.sql`, `papers.go`
- Create: `internal/transport/httpapi/papers.go`
- Modify: `internal/domain/repositories.go`, `internal/transport/httpapi/router.go`, `main.go`
- Create: `web/src/pages/PapersPage.tsx`, `PaperDetailPage.tsx`, `web/e2e/papers.spec.ts`
- Modify: `web/src/types.ts`, `web/src/api/client.ts`, `web/src/App.tsx`

**行为：**

- Import 按项目 Scope 和 SourceKey 幂等；重复导入不覆盖用户 Note/Tags。
- List/Get/Patch 和 SaveNote 做归属检查及 revision 校验。
- 搜索结果只有明确保存后才进入项目库。
- 页面展示来源、年份、摘要级别、阅读状态、标签和用户笔记；详情页支持打开文档。

**验证：** 两项目隔离、重复导入、revision 冲突、刷新持久化；Go 单测、TypeScript 检查、浏览器论文库用例。

## Task 3：冻结文档、证据和引用面板

**文件：**

- Create: `internal/domain/evidence.go`, tests
- Create: `internal/app/evidence.go`, `internal/app/analysis.go`, tests
- Create: `internal/infra/artifacts/store.go`, tests
- Create: `internal/infra/sqlite/evidence.go`
- Create: `web/src/components/EvidencePanel.tsx`, `CitationLink.tsx`
- Modify: `internal/transport/httpapi/papers.go`, `web/src/types.ts`

**行为：**

- 文本归一化后计算 UTF-8 SHA-256 并冻结；拒绝绝对路径、路径逃逸和符号链接逃逸。
- Evidence.Save 校验项目、论文、文档、rune 半开区间和逐字 Quote。
- 分析输出缺失字段使用 unknown；不具备原文支持的结论标记 unverified。
- 检索仅使用当前项目文档，引用可从前端打开冻结片段。

**验证：** 伪造 Quote、越界、跨项目和旧版本文档用例；人工标注 fixture 的定位回归；前端引用跳转。

## 阶段验收命令

```powershell
go test -count=1 ./...
npm --prefix web run typecheck
npm --prefix web run build
npm --prefix web run test:e2e -- papers.spec.ts
```

真实 arXiv MCP 联调需另行记录命令、服务版本、tools/list 结果和只读查询结果；没有外网或服务时，验收报告明确标记未联调。

## 本轮已实施的接入项

- `config.AcademicConfig`、`config.arxiv.example.json`：独立的真实学术来源配置，固定服务版本。
- `infra/mcp/connect.go`、`arxiv.go`：真实 MCP 启动、发现、schema 映射、超时、串行访问、限流和摘要缓存；结构化内容优先，拒绝 MCP 错误结果。
- `app/academic_tools.go`、`academic_graph.go`：四个项目范围论文工具、模型工具循环、ask_user 中断恢复和进度投影。
- `ResearchState`/`TurnService`：第三版图与状态恢复、可信 Scope 注入；不修改框架。
- `httpapi/academic.go`、`PapersPage`：项目归属、搜索边界、来源模式和空结果提示。
- `sqlite/runtime.go`：快照保留当前轮的业务工具结果；避免诊断事件覆盖检索过程。
- `web/e2e/papers.spec.ts`：会话搜索/归档/读取/刷新和论文库搜索/显式归档/空结果验收。
- `app/academic_live_test.go`：真实 MCP 按 ID 归档和摘要读取；关键词全链路单独启用，指定论文使用精确标题短语查询，真实搜索/归档/摘要读取复测已通过。
