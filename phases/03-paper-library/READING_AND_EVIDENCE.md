# 阶段 03 补齐：全文阅读、项目检索与引用定位

日期：2026-10-25。状态：03-A / 03-B 已实施并通过本地与公开全文验证；具体证据见 ACCEPTANCE.md。阶段 04 的 research:// 资料视图仍按独立计划实施。

本计划补齐原 T08/T09，不新增产品阶段。现有论文搜索、归档、摘要读取和 Evidence 区间校验继续复用；完成后再执行[阶段 04](../04-auto-research/DESIGN.md)的逐篇分析与报告任务。

## 1. 用户能得到什么

在论文详情点击“获取全文”，查看获取状态、可用章节和原始来源；Agent 可以按章节阅读、搜索当前项目资料，保存有原文依据的证据。点击证据打开冻结文本并高亮引用。获取失败仍能阅读摘要，但明确显示“仅摘要”及失败原因。

不把摘要、全文、项目笔记混成同一类资料。项目笔记用于保存用户认可的项目共识与下一步；论文文档保存来源材料；Evidence 保存从来源中抽取的可定位片段。

## 2. 技术方案与职责

| 模块 | 职责 | 文件计划 |
| --- | --- | --- |
| DocumentService | 检查论文归属，协调获取、解析、冻结、分块；复用已成功文档 | 新增 `internal/app/documents.go` |
| 全文适配器 | 优先获取 arXiv HTML，必要时用 PDF 文本提取；不增加模型调用 | 新增 `internal/infra/documents/arxiv.go`、`html.go`、`pdf.go` |
| Artifact Store | 保存原始文件和冻结长文本，按项目及内容哈希定位 | 新增 `internal/infra/artifacts/store.go` |
| 文档与块索引 | 保存来源版本、解析版本、章节、块和索引；供 Evidence 校验读取 | 扩展 `internal/domain/paper.go`、`internal/infra/sqlite/evidence.go`，新增 `internal/infra/sqlite/document_search.go` |
| EvidenceService | 沿用 rune 半开区间和逐字 Quote 校验，补定位查询 | 扩展 `internal/app/evidence.go` |
| 用户界面 | 获取按钮、内容级别、章节、引用高亮；通过对象 ID 获取正文 | 扩展 `web/src/pages/PapersPage.tsx`；新增 `DocumentReader.tsx`、`CitationLink.tsx` |

继续使用 Go、SQLite 和 React；项目内先用 SQLite FTS5。英文采用分词检索，中文短语补充有界子串检索，并分别验收，不把英文分词效果当作中文检索效果。不引入向量数据库；语义检索待检索质量实测后再决定。

arXiv MCP 继续负责现有搜索和摘要工具。全文获取由应用适配器实现，保持 `PaperSource` 公共形状；不假定当前上游 MCP 已开放全文工具，也不扩大远端工具白名单。新增适配器由入口组合注入。

## 3. 文档、版本和定位约定

- 扩展 SourceDocument 元数据：`content_kind`（abstract/html/pdf）、`source_version`、`parser_version`、`original_artifact_id`。现有 `ArtifactID` 指冻结文本；现有小摘要可继续内联 `Text`。
- 文档正文读取统一经过 DocumentService；Evidence 校验通过仓储按需加载全文，不能因正文外置而把空 Text 传给 `ValidateEvidence`。普通列表和模型工具结果不携带全部正文。
- 获取时保留明确的 arXiv vN；无法确认来源版本时记录 unknown 和内容哈希，不把最新版猜成 v1。项目论文继续按基础身份去重，文档可保留多个版本。
- 冻结后按归一化 UTF-8 文本计算 SHA-256。每个块保存 DocumentID、BlockID、章节、StartRune、EndRune；区间都指向同一份冻结文本。版本变化创建新文档，旧引用仍打开旧文本。
- HTML 使用章节和块定位；PDF 仅在解析器确实提供页边界时显示页码。缺少页码时显示文本片段，不让模型填写页码。
- 全文缺失、扫描件无可提取文本、解析失败分别记录。第一版不做 OCR，不用模型补造缺失正文。
- 模型调用 `save_evidence` 时提供可信工具返回的 BlockID 和原文 Quote；应用在该块内定位并验证。唯一匹配才保存；重复匹配返回候选区间要求消歧，不让模型估算中文字符偏移。

## 4. 工具与 HTTP

拟新增的应用接口均属于 AutoResearch：

```go
func (s *DocumentService) Fetch(ctx context.Context, scope domain.Scope, paperID string) (domain.SourceDocument, error)
func (s *DocumentService) Read(ctx context.Context, scope domain.Scope, documentID, blockID string) (domain.DocumentBlock, error)
func (s *DocumentService) Search(ctx context.Context, scope domain.Scope, query string, limit int) ([]domain.DocumentHit, error)
```

DocumentBlock 包含块身份、章节、正文及 rune 区间；DocumentHit 包含上述定位和有界摘要，不等于已确认 Evidence。

普通任务/报告文本读取复用阶段 04 [项目资料视图](../04-auto-research/DESIGN.md#52-用-read_file-读取项目资料)与现有文件工具，不为每个业务对象新增读取工具。下列论文工具保留外部获取和精确引用语义；已有冻结正文也可通过该视图提供只读地址，但 Evidence 的定位始终按原 DocumentID/BlockID 校验，不把展示行号当作原文 rune 区间。

| 工具 | 输入与输出 |
| --- | --- |
| `read_paper` | 保留现有 abstract 默认；新增全文目录和按章节/BlockID 阅读，返回文档身份与有界文本 |
| `search_project_evidence` | 查询当前项目已归档文本，返回匹配块；名称沿用总体架构，结果区分 document_hit 与已保存 evidence |
| `save_evidence` | DocumentID、BlockID、Field、Value、Quote（PaperID 由文档推导）；应用计算准确区间，返回 EvidenceID |

已实现 HTTP：`POST /api/projects/{projectID}/papers/{paperID}/documents/fetch`；`GET /api/projects/{projectID}/documents/{documentID}`（元数据/目录）；`GET /api/projects/{projectID}/documents/{documentID}/blocks/{blockID}`；`GET /api/projects/{projectID}/evidence/{evidenceID}`。全文获取请求在有界超时内完成，状态记录先落盘；重试复用成功文档。另提供持久化获取状态查询接口。

下载默认上限 30 MiB、90 秒；PDF 解析默认上限 30 秒，超限明确失败。PDF 采用可配置的 Poppler `pdftotext` 可执行文件，以参数数组调用；未安装时 HTML/摘要仍可用并提示缺少组件。实现时记录实际验证的 Poppler 版本及安装说明。只处理已解析论文对应的公开来源地址，检查重定向目标，不接受模型传入任意 URL 或文件路径。

## 5. 实施与验收顺序

### 03-A：获取、冻结与章节阅读

- [x] 先用本地 HTTP fixture 验证：HTML 正常、PDF 可提取、空文本、超限、重定向错误、同一内容重复获取、版本更新。
- [x] 实现 Artifact 写入临时文件后原子提交，登记正文哈希；数据库登记失败不发布成功文档。重复请求复用已提交结果，遗留未登记文件不自动清理。
- [x] 新增迁移文件，保留既有摘要与证据；通过两项目隔离、解析器缺失及旧文档读取回归。
- [x] 在论文页提供获取和重试入口，刷新后仍显示持久状态；完成一个可单独演示的全文阅读闭环。

### 03-B：项目检索、证据与引用

- [x] 测试跨项目查询、中文/英文查询、重复 Quote、Unicode 区间、来源版本变化后旧引用仍可打开。
- [x] 实现检索与证据工具，控制返回片段长度；长全文不写入 Graph State 和会话快照。
- [x] 前端点击 EvidenceID，经服务端校验后打开对应文档块并高亮；刷新定位仍有效。
- [x] 使用脚本模型自动完成“读章节 → 搜索 → 保存证据 → 点击原文”，验证普通问答不会自动新增项目笔记。

在项目根目录执行：

```powershell
go test -count=1 ./...
go vet ./...
npm --prefix web test
npm --prefix web run typecheck
npm --prefix web run build
npm --prefix web run test:e2e -- papers.spec.ts documents.spec.ts
```

`documents.spec.ts` 已覆盖用户界面和脚本模型的证据闭环。公开 HTML / PDF 的实际记录及解析器版本见 ACCEPTANCE.md；不使用收费模型。
