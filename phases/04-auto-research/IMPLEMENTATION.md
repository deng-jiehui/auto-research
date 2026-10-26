# 阶段 04 实施计划：自动调研与研究方向整理

> 执行者：使用 executing-plans 逐项实施并核对结果；本计划不要求子 Agent。用户已确认开始实施，按前置能力、通用资料读取、方向、调研和报告顺序推进。

**目标：** 同一个 Agent 从研究问题出发，经用户审核形成可追溯证据矩阵和报告。

**架构：** 复用单 Agent Graph、工具、中断、持久化 worker、SSE 和 callback。新增 Direction、ResearchRun 与报告用例；业务数据库负责研究事实，框架检查点负责执行恢复。

**技术栈：** Go 1.24、goHarness 现有公共 API、modernc SQLite、React 19、TypeScript、现有 Markdown 渲染器；全文基础见阶段 03 补齐计划。

状态：2026-10-26，前置全文/证据、04-A、04-B1/B2/B3 已交付。当前支持任务、累计预算、计划/候选审核、逐篇分析、暂停继续与单篇重试；04-C1 矩阵、Claim 和不可变报告已交付。04-C2 的共用 UI 层已接入 Web，报告后续追问、综合闭环与故障恢复已验收；CLI 命令按用户要求延期。设计依据为 [DESIGN.md](DESIGN.md) 与 [GRAPH_DESIGN.md](GRAPH_DESIGN.md)，实际验证见 [ACCEPTANCE.md](ACCEPTANCE.md)。

## 全局约束

- 仅修改 AutoResearch 项目，保留既有未提交工作，不修改框架，不提交或推送。
- 中文注释和文档；领域不导入模型/HTTP/SQL SDK。
- 普通会话继续可用，默认 single；不引入 planner/reader/writer 多套上下文。
- 工具的用户、项目、会话与任务身份由应用注入；模型不能扩大作用域。
- 新增迁移，不改历史迁移；新增定义版本，保留已有未完成 Turn 的恢复路径。
- 真实 API Key 不进入测试或输出；自动验证使用临时数据库、本地来源和脚本模型。
- 各任务先运行有意义的失败回归再实施；记录实际结果，不提前勾选通过。
- 工具优先复用，扩展时同步更新参数/schema、描述、执行方法、返回结果和验证；过于复杂时按职责拆分，不固定新增工具数量。当前成果提交采用 submit_research_result 候选入口，读取复用文件工具，审核扩展 ask_user；内部服务不逐个映射成模型工具。

## 文件与接口边界

| 区域 | 拟新增文件 | 修改现有文件 |
| --- | --- | --- |
| 通用资料读取 | `internal/app/project_resources.go`、`project_resources_test.go` | `workspace_tools.go`、对应读取测试、工具装配和 main.go |
| 成果提交入口 | `internal/app/research_result_tool.go`、`research_result_tool_test.go` | 工具装配、状态与定义版本分派 |
| 方向 | `internal/domain/direction.go`、`internal/app/directions.go`、`internal/infra/sqlite/directions.go`、`internal/transport/httpapi/directions.go` | `domain/repositories.go`、路由和 main.go |
| 调研 | `internal/domain/research.go`、`internal/app/research.go`、`research_interactions.go`、`internal/infra/sqlite/research.go`、`internal/transport/httpapi/research.go` | `turns.go`、`single_agent_graph.go`、`research_state.go`、`interaction_tools.go` |
| 报告 | `internal/domain/claim.go`、`report.go`、`internal/app/reports.go`、`internal/infra/sqlite/reports.go`、`internal/transport/httpapi/reports.go` | 阶段 03 引入的 Artifact Store 和证据接口 |
| 界面 | `DirectionsPage.tsx`、`ResearchRunsPage.tsx`、`ResearchRunPage.tsx`、`ReportPage.tsx`、`ResearchPlanReview.tsx`、`CandidateReview.tsx`、`EvidenceMatrix.tsx` | `App.tsx`、`ProjectNav.tsx`、`SessionPage.tsx`、API client、types、styles |
| 共用 UI | `internal/ui/model.go`、`service.go`、`web/src/interaction.ts`、`UserInteraction.tsx` | HTTP/SSE、会话/研究页；CLI 适配器延期 |

页面位于 `web/src/pages/`，组件位于 `web/src/components/`；每个业务包旁放对应 `_test.go`，浏览器测试位于 `web/e2e/`。SQLite 迁移编号以实施时最大编号递增分配。

跨任务接口形状：

```go
func (s *DirectionService) Decide(ctx context.Context, scope domain.Scope, directionID string, revision int64, state string) (domain.Direction, error)
func (s *ResearchService) Start(ctx context.Context, scope domain.Scope, sessionID string, spec domain.ResearchSpec) (domain.ResearchRun, error)
func (s *ResearchService) Review(ctx context.Context, scope domain.Scope, runID string, revision int64, selectedIDs []string) error
func (s *ReportService) Generate(ctx context.Context, scope domain.Scope, runID string) (domain.Artifact, error)
```

保留原契约方法：Review 读取当前已落盘审核上下文验证哈希/候选子集，并原子写入审核及恢复动作；HTTP 额外提供 revision、idempotency_key，交由应用动作层保证幂等。Generate 只渲染已校验草稿，不进行隐式模型调用；草稿由 submit_research_result 的 report 类型提交。内部服务不因工具合并而合成单个服务。

## 前置交付：03-A / 03-B

- [x] 按[阶段 03 补齐计划](../03-paper-library/READING_AND_EVIDENCE.md)交付全文与章节读取。
- [x] 交付项目检索、Quote 定位及点击引用；确认旧摘要与证据仍可读取。
- [x] 记录真实 HTML/PDF 能力和未覆盖内容后，再推进下面涉及逐篇分析的任务。

## 共用能力：资料视图与成果提交入口

**当前进度：** 三种读取工具已支持 research://，方向、任务及候选视图已开放；矩阵和报告资料已在 v8 开放，普通会话 v9 支持报告绑定与追问；历史 single/multi-v6 支持逐篇分析，v1–v8 保留各自工具 schema、结束条件与恢复路径，不向模型广告未实现能力。

**输入：** 可信 Scope、现有工作区策略、项目业务存储和 Artifact Store。
**输出：** 扩展的三种文件读取工具，以及参数与职责清楚的成果提交工具；成果入口是否拆分在 schema 设计时核对。

- [x] 已由 TestReadFileSupportsProjectResourceAndKeepsWorkspacePolicy、TestResourcePaginationBindsRevision 覆盖逻辑资料读取、跨项目拒绝、分页版本绑定和原工作区策略。
- [x] 在 read/list/search 工具应用适配中识别 research://，使用可信 Scope 查询资料；逻辑路径不能进入 os.Open，也不能放开工作区根目录到整个 data 目录。
- [x] 同步更新 ListDirectoryArgs/ReadFileArgs/SearchFilesArgs 的字段说明、工具描述、实际分派方法和来源/版本返回；保留原默认行为，按需要补充定位或分页参数。
- [x] 实现小型索引、任务资料 JSON 和 Artifact Markdown 读取。目录/搜索结果返回同样可读的地址；候选和矩阵分页绑定 Revision，变化后旧页返回明确冲突。
- [x] 逻辑资料地址始终只读，write_file/apply_patch 拒绝修改；保留已有物理文件写入确认、哈希与路径边界。Shell 不解析逻辑资料地址。
- [x] 成果提交定义 direction / plan / paper_analysis / report 四类具体 schema，校验 kind 与内容匹配；未知 kind、夹带批准/状态字段和版本冲突返回具体错误。按分批交付开放的 kind 固定到图定义版本，不向模型广告未实现的类型。
- [x] 复核 schema 条件分支、必填字段和权限/确认差异；若合并入口过于复杂，按成果职责拆出明确工具，并同步本计划、描述和调用示例，保留共用内部服务。不得为减少名称而改成无类型 data/action。
- [x] 各成果类型调用独立应用用例，返回 ID、Revision、可读地址；不暴露任意 action、数据库表名或动态方法分派给模型。
- [x] 新增 schema/定义版本兼容回归；v1/v2 重建图恢复保持旧读取和 ask_user 参数，后续批次继续沿用版本分派规则。

## 04-A / T10：方向提案与正式方向

**输入：** 当前项目、论文库、用户明确的方向整理/保存请求。
**输出：** 版本化 Direction、真实用户确认、方向页与继续研究入口。

- [x] 先增加失败回归 `TestDirectionApprovalBindsRevision`、`TestDirectionRejectsForeignPaper`：旧 Revision 返回 ErrConflict；跨项目论文不进入提案。
- [x] 实现 proposed/accepted/rejected/archived 状态和历史版本；保留来源会话、代表论文、关键问题及待验证假设。
- [x] 实现 DirectionService、仓储、迁移和 HTTP；用户接受/拒绝只走可信 API，模型工具只保存提案。
- [x] 开放 submit_research_result 的 direction 类型；没有论文依据时保存证据不足标记，不伪造论文。不新增 propose_directions 工具。
- [x] 交付方向页及会话提案卡片；编辑后旧确认失效，点击“继续研究”带方向 ID/Revision 返回会话。
- [x] 运行领域/应用/SQLite/HTTP 回归及 `directions.spec.ts`：提案、编辑、确认、刷新后仍存在。

该任务可以独立验收；方向不必等完整自动调研才能供用户管理。

## 04-B1 / T11：ResearchRun 登记与执行预算

**输入：** ResearchSpec、选中方向版本、当前会话。
**输出：** 持久 Run、输入快照、可串行执行的 Turn 关联。

本批已完成：Run/Turn 原子登记，项目/方向快照，OperationID/AttemptID 与计划内容版本分离，调用预算与未知 Usage、任务页、旧图兼容。候选版本和审核表由 04-B2 增补，WorkItem 及分析历史由 04-B3 的 011 迁移增补；原复合待办按其完整范围核对。

- [ ] 先增加 `TestResearchSpecFrozenAfterProjectUpdate`、`TestResearchStartIsAtomicWithTurn`、`TestResearchToolAttachesCurrentTurn`。模拟事务失败验证无孤立 Run 或已入队但缺少关联的 Turn。
- [ ] 实现 Run、计划、候选、WorkItem、预算和动作表；事务中登记输入范围及执行身份，活动会话返回明确 409。
- [ ] HTTP Start 与 submit_research_result 的 plan 类型共用登记用例：前者创建关联 Turn，聊天首次提交计划时只绑定当前 Turn；后续提交保存新计划版本，不能递归 Send。不另新增 create/update 任务工具。
- [x] 实现累计调用预算预留、Usage 更新及 unknown 标记。恢复、工具重试、子 Agent（如使用）都不能绕过 Run 预算。
- [x] 在任务页显示范围快照、阶段、计数、限额和失败原因；不把模型配置或凭据放入快照。
- [x] 增加新旧图定义构造分派；现有 single-v1、multi-v1 检查点重建恢复回归仍通过。

## 04-B2 / T11：计划与候选审核中断

**输入：** 本 Run 计划、检索结果和当前待审核版本。
**输出：** 可靠审核记录、合法纳入集及继续原模型上下文的 tool result。

已完成 research_plan 与 research_candidates 表单、事务审核、原上下文恢复。候选按来源规范键在 Run 内去重，审核快照版本化；回答前和提交后重启均有回归。本批最初使用 single/multi-v5，当前 v6 继续复用审核；保留 v4/v5 原 schema 与结束条件。

- [x] 候选回归覆盖旧版本/哈希、跨 Run/候选、重复 ID、超限、伪造 PaperID 与混合答案；故障注入证明审批、选中论文归档和 Resume 登记同事务。
- [x] plan 成果类型保存计划；ask_user 增加 clarification / research_plan / research_candidates 三类表单，交互独占批次。graph.Interrupt 保存当前业务对象版本和哈希，不新增独立审核工具。
- [x] 同步 AskUserArgs/Result、工具描述、校验方法和返回结果；审核类参数绑定 expected_revision，默认缺省仍按原 clarification 处理。如果后续表单使参数与权限规则过于复杂，按设计规则拆分交互职责，并保持相同中断恢复机制。
- [x] 扩展交互准备、参数校验、完成结果及恢复解码，普通追问返回文本，审核返回结构化决定；不把 CandidateID 列表塞进自由文本猜测解析。
- [x] 增加 `TestCandidateReviewKeepsTypedAnswerAndScopeAcrossRestart`，在审核提交后重建服务，确认选中论文和用户修改仍按原始类型进入配对工具结果。
- [x] 澄清次数只统计 clarification，业务审核不消耗原有三次澄清额度；仍校验审核版本及 Run 预算，旧定义按原答案形状恢复。
- [x] 检索候选按本 Run 去重并保存推荐理由；明确 CandidateID 到入库 PaperID 的映射。
- [x] 提交审核时验证 pending interrupt、revision、哈希、候选子集及分析上限，在同一事务保存动作；worker 再按现有恢复规则消费。
- [x] 交付可编辑计划、候选复选框、纳入/排除理由、全部排除和修改后重新审核界面。
- [x] 使用 `research-plan.spec.ts` 覆盖候选页刷新、重检、空结果、旧浏览器 409、断网重试沿用身份、确认重发只归档一次；服务重启由 Go 集成回归验证。

## 04-B3 / T11：逐篇分析、暂停与重试

**输入：** 已确认纳入集、冻结文档及分析字段。
**输出：** 逐篇状态、结构化五字段分析、EvidenceID 列表和任务进度。矩阵行展示由后续已交付的 04-C1 补齐。

- [x] 先增加 `TestResearchAnalysisSkipsExcludedPaper`、`TestResearchWorkItemReplayIsIdempotent`、`TestResearchRetryKeepsBudget`、`TestResearchPauseWaitsForToolBoundary`。
- [x] 扩展 `read_paper`、项目检索和证据工具的研究作用域，拒绝把未纳入论文写入本 Run 分析；保留普通会话的项目级阅读。
- [x] 开放 submit_research_result 的 paper_analysis 类型，各字段保存 finding/inference/unverified 或 unknown；引用通过已有 EvidenceService 验证，不新增逐篇提交工具。
- [x] WorkItem 用 Run/Paper/Document/SpecHash 幂等登记；已完成结果可复用，失败论文可单独重试。首版顺序分析，控制每次模型阅读量。
- [x] 实现安全边界暂停、恢复、取消及累计预算。正式任务默认模型 64 次/工具 128 次、图 512 步；15 分钟执行段到安全边界暂停，额外 125 秒硬期限宽限；普通聊天保持原限制。
- [x] 核对 research edit/retry 行为：编辑关联新 Run，失败重试继续原 Run，完成重试创建新 Run；旧分析及其版本保留。报告版本保留在 04-C 验收。
- [x] 持久化并推送逐篇事件；E2E 证明报告完成前已能看到部分证据，刷新/重启不重复插入。

具体实施与验证见 [ANALYSIS_IMPLEMENTATION.md](ANALYSIS_IMPLEMENTATION.md) 和 [ACCEPTANCE.md](ACCEPTANCE.md) 的 04-B3。成功操作重放先于预算预留，不增加版本或调用计数；业务状态与待回答问题一次发布，防止立即确认时产生版本冲突。

## 04-C1 / T12：矩阵、Claim 与版本化报告

**输入：** 已确认纳入集、逐篇分析、有效 Evidence 与 Agent 报告草稿。
**输出：** 可点击引用的矩阵、不可变报告版本和 Artifact。

- [x] 引用与版本回归见 `TestReportValidatesOriginalEvidenceAndScope`、`TestReportRequiresBoundClaimsAndAllSections`、`TestReportRemainsImmutableAfterFailedPaperRetry`，完整执行与恢复见 `TestV8ResearchProducesBoundReportAndReadableResources` 和 `TestV8PrematureFinalAndReportFailureReuseAnalysis`。
- [x] 实现 Claim 和报告草稿仓储；submit_research_result 的 report 类型只接收当前任务的结构化草稿与引用，不接受任意服务器路径。普通文件草稿写入不自动登记为正式报告。
- [x] Generate 校验引用、论文覆盖、摘要级别和失败项标记；具体缺项返回原 Agent 修正，预算耗尽返回明确失败/暂停。
- [x] 复用 Artifact Store 原子保存 Markdown，数据库记录新版本及确切证据集合；不覆盖旧版本。
- [x] 交付矩阵与报告页，沿用 Markdown 安全渲染；点击引用打开冻结版本原文并高亮。
- [x] 用固定标注 fixture 验证排除论文未入报告、未知值未被补造、摘要论文没有全文结论；人工另查引用对语义的支持程度。

## 04-C2 / T12：共用 UI、后续追问与整阶段验收

**输入：** 持久交互与事件、已保存报告、会话后续问题。
**输出：** 可复现研究闭环及实际验收记录。

- [x] 实现端无关 UI 服务和版本化展示协议，Web 共用用户交互组件；研究回答及 SSE 使用任务身份。边界和回归见 [UI_IMPLEMENTATION.md](UI_IMPLEMENTATION.md)。
- [x] 用户追问时通过 read_file 读取资料索引、对应报告及证据引用，使用原文读取工具核对；不注册 get_research_run/read_research_report，不把整份资料预先塞进系统指令。报告选择、持久继承与前端入口见 [REPORT_CONVERSATION.md](REPORT_CONVERSATION.md)。
- [x] single/multi 脚本模型仅使用资料索引和原文件工具完成任务查询、定位报告并追问冻结原文，验证不需要独立 get/read/create/update/review 研究工具；HTTP 的任务操作接口继续独立验证。
- [x] 综合场景由 TestPhaseFourComprehensiveFiveCandidatesAndAbstractFallback 覆盖 5 候选和 1 篇仅摘要；research-plan.spec.ts 覆盖排除/重检，research-report.spec.ts 和 report-conversation.spec.ts 覆盖矩阵、引用、刷新和后续追问。
- [x] 注入 429、单篇解析失败、模型格式错误、超时、预算耗尽、审核后进程退出，验证错误可见且恢复不重复保存结果。
- [x] 独立临时数据库演示，不关闭或占用用户现有 8080 服务；不消耗真实模型 Token。
- [x] 实际通过后补充 `ACCEPTANCE.md`，同步 README、路线图和需求映射；真实来源/模型质量单独记录验证边界。

## 验证命令

在 AutoResearch 独立模块执行，父模块全量测试不包含本项目：

```powershell
go test -count=1 ./...
go vet ./...
npm --prefix web test
npm --prefix web run typecheck
npm --prefix web run build
npm --prefix web run test:e2e
```

CLI 命令按用户要求延期，不作为本轮或阶段验收的必需入口。未来终端适配器复用 `internal/ui.Service`，或通过相同 HTTP 接口连接服务端；审核仍需明确用户输入。当前 `main.go` 只支持 `serve`。

每项交付记录输入、观察到的输出、实际命令和限制。完成文档不等于完成开发，离线 fixture 通过不等于真实模型研究结论可靠。

## 当前交付边界与后续顺序

各批验收保留在 ACCEPTANCE.md，最新交付见 04-C2。执行身份与内容版本分开；报告引用绑定矩阵和分析版本，规则以 DESIGN.md 和 REPORT_IMPLEMENTATION.md 为准。

04-A、04-B1/B2/B3、独立研究、04-C1 矩阵/报告、共用 UI 及 04-C2 报告后续追问已交付；阶段四综合场景与故障恢复已完成，CLI 适配器及阶段五远程实验暂缓。正式任务输入冻结：编辑消息或完成后重新执行派生新 Run，并关联 PreviousRunID；失败重试保留原 Run、冻结文档和累计预算，成功论文不会重做。旧 v1–v8 保留原图 schema、结束条件和检查点恢复路径。


## 独立 Research Agent（v7）

- [x] 原子创建隐藏执行会话、初始用户消息、Turn/Run 和幂等来源映射；012 迁移增加队列类型。
- [x] 普通会话 `start_research` 只委派显式输入，研究不复制聊天或项目笔记。
- [x] chat/research 两个 worker，独立上下文、检查点与预算；同类任务排队。
- [x] 独立任务详情、计划/候选/澄清、暂停继续及重试；来源会话只读摘要卡片。
- [x] 成果直接投影，不增加模型复述；既有 read_file 按需读取完整结果。
- [x] 旧 v1–v6 图和兼容 API 保留，来源删除后任务及成果仍存活。

完整文件边界与验证记录见 [独立研究实施](INDEPENDENT_AGENT_IMPLEMENTATION.md) 和 [验收](ACCEPTANCE.md)。
