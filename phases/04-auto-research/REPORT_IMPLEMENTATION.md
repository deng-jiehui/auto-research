# 04-C：证据矩阵与可核验报告实施计划

> 使用 writing-plans 制定、executing-plans 在当前工作区执行。用户已要求继续下一部分；保留原有改动，不提交推送，不修改框架，不调用付费模型，不启动用户既有服务。

**目标：** 独立研究完成逐篇分析续，继续由同一 Research Agent 综合比较、提出假设和实验建议，保存带真实引用的不可变报告；会话只接收摘要和报告位置。

**架构：** 矩阵直接投影现有 WorkItem，稳定哈希绑定范围、审核版本和分析版本。扩展 `submit_research_result(kind=report)`，报告草稿使用结构化 Claim（结论、确定性、分析版本、证据 ID），代码核验引用续渲染 Markdown。报告正文存入现有 Artifact Store，版本、提交身份与进度同事务保存。新研究使用 v8，v1–v7 保留结束方式。

**技术栈：** 现有 Go、SQLite、Artifact Store、React/TypeScript、MarkdownRenderer、Playwright，无新增依赖。

## 类型与所有权

| 文件 | 职责 |
| --- | --- |
| domain/research_report.go | EvidenceMatrix、AnalysisReference、ReportClaim、ReportDraft、ResearchReport、ReportReference；结构校验与稳定投影 |
| infra/sqlite/013_research_reports.sql、research_reports.go | 不可变报告版本、操作重放、引用/范围/版本校验、原子事件；正文复用 Artifact Store |
| app/research_reports.go | report 工具参数与方法、完成门槛、离线模型报告分支；不新增模型工具名 |
| app/single_agent_graph.go、research_execution.go | v8 装配、成功重放先于预算、完成校验、旧版本兼容 |
| app/project_resources.go | matrix.json、报告目录与 report.md 逻辑只读地址，复用读取、列表、搜索与分页 |
| transport/httpapi/research_reports.go | GET /research/:id/matrix、/reports、GET /reports/:id；只读接口不接受身份参数 |
| web/components/ResearchReportPanel.tsx | 矩阵、报告版本、Markdown、下载和原文引用；集成独立任务页 |

## 接口

`ResearchRepository` 扩展：

```go
SaveResearchReport(context.Context, Scope, string, string, int64, ReportDraft) (ResearchReport, error)
ResearchReportReplay(context.Context, Scope, string, string, int64, ReportDraft) (ResearchReport, error)
GetResearchReport(context.Context, Scope, string) (ResearchReport, error)
ListResearchReports(context.Context, Scope, string) ([]ResearchReport, error)
```

草稿：`title`、`summary`、`matrix_hash`、`limitations`、`claims`。Claim 包含 `section`（comparison/conflicts/gaps/next_steps）、`status`（finding/inference/unverified/unknown）、`text`、`analysis_refs:[{work_item_id,analysis_revision}]`、`evidence_ids`。所有 Claim 绑定本 Run 的有效分析版本；finding 必须有这些分析已登记的真实证据，摘要不提升实验事实。报告自动包含范围、检索、纳入排除、完整矩阵及失败/仅摘要标记。程序验证身份、原文与来源，语义支持程度仍需用户检查。

## 任务清单

- [x] 写矩阵/报告结构测试：哈希不受执行统计影响，分析版本改变必须变化；重复引用、无依据 finding、缺少章节被拒绝。
- [x] 写 SQLite 回归并观察失败：过早报告、跨任务/项目引用、旧矩阵哈希、伪造原文、同身份换内容、重放不新增版本。实现 013 和保存事务。故障注入验证无报告元数据/事件残留。
- [x] 写新图完整执行回归：审核→逐篇→矩阵→报告→最终完成；模型提前结束时返回缺项继续。只重试报告不重读成功论文，预算累计；旧 v7 仍 analyses_completed。
- [x] 扩展 v8 工具参数、描述、方法与 fixture。新独立任务升级 v8，普通会话沿用 v7；工具结果仅返回报告 ID/摘要/位置。报告正文不自动回灌聊天。
- [x] 复用 read_file/list_directory/search_files 开放矩阵分页、不可变报告正文和版本目录；HTTP 校验项目范围。矩阵更新续旧报告仍可读，界面提示属于历史输入版本。
- [x] 写浏览器回归先观察失败：任务页矩阵、报告渲染、引用跳转、Markdown 导出、刷新保留；会话卡片只有简要结果及入口。适配新任务结束状态的既有验收。
- [x] Go 全量 test/vet，前端单测/typecheck/build，全量浏览器回归并检查截图；同步 README、GRAPH_DESIGN、ACCEPTANCE。

## 验收与边界

使用临时数据库、离线来源和本地脚本模型。研究内容由模型决定，代码只校验状态、版本、引用与必要章节；不按关键词生成研究结论。报告失败保留逐篇成果，重试沿用冻结文档和累计预算；重新调研仍派生新 Run。矩阵展示全部已确认 WorkItem，失败和未知项不能消失。报告不冒充真实模型研究质量已验收；远程实验仍属阶段五。

2026-10-25 本批实际通过：Go 全量测试与 vet，前端 25 项单测、typecheck、build，全量 30 项 Playwright。已检查桌面与手机截图，完整交付与手测步骤见 [ACCEPTANCE.md](ACCEPTANCE.md) 的 04-C1。续续计划已调整为先交付用用 UI，CLI 命令延期；续续追问专项与整阶段验收继续在 04-C2 推进。
