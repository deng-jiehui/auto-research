# 阶段 04 设计：自动调研与研究方向整理

日期：2026-10-26。状态：04-A、04-B 和 04-C1 已交付，支持独立调研、审核、逐篇分析、证据矩阵与版本化报告。04-C2 已补齐共用 UI、Web 接入及[报告后续对话](REPORT_CONVERSATION.md)，综合验收已完成；CLI 命令延期，阶段五远程实验暂缓。当前边界见 ACCEPTANCE.md，旧版本章节保留历史语义。

## 1. 目标与进入条件

让学生从“我想了解一个研究方向”推进到“我确认了一个可执行研究问题，并且知道论文依据、已有局限和下一步实验是什么”。

演示场景：用户研究多变量长序列预测，希望比较 Transformer 与线性基线，在有限 GPU 条件下选出适合开展学生实验的问题。Agent 帮助缩小范围、检索论文、审核候选、阅读证据并形成报告；用户可以继续追问某项结论的来源。

阶段 03 的全文阅读、项目检索与引用定位已按[补齐计划](../03-paper-library/READING_AND_EVIDENCE.md)交付；方向管理可以并行于这些基础能力做设计，但正式调研验收依赖可定位证据。

本阶段分三次交付：04-A 方向管理；04-B 可恢复的调研与候选审核；04-C 证据矩阵、报告及后续追问。远程 SSH 实验仍在阶段 05。

首版来源维持已接入的 arXiv，并可使用项目已归档论文。总体需求 FR-R03 的“多个公开来源并行查询”仍需后续增加来源适配器，本计划不将单来源流程验收算作该需求全部完成。

## 2. 用户流程

1. 在现有会话讨论主题。普通解释直接回答；缺少会改变研究路线的信息时，模型调用 `ask_user`，展示可编辑选项和自定义文本。
2. 用户希望保存方向时，模型调用 `submit_research_result(kind="direction")`，提交有代表论文、关键问题、研究空白和可行性说明的提案。用户编辑、确认后才进入正式方向。
3. 用户点击方向上的“开始调研”，或直接在会话要求开展调研，创建 ResearchRun，冻结问题、年份、来源、论文上限和选中方向版本。
4. Agent 通过 `submit_research_result(kind="plan")` 提交检索计划，再调用 `ask_user(kind="research_plan")`。用户查看并修改检索式、纳入/排除条件及范围，确认后执行；取消则保留草案并结束本次任务。
5. 搜索结果去重形成候选集。模型给出推荐与理由，再调用 `ask_user(kind="research_candidates")`，展示复选框、摘要、纳入/排除理由和分析上限。用户提交后才归档及分析选中论文。
6. Agent 按需阅读选中论文，保存结构化字段和原文证据。界面逐篇显示进度、失败与证据，不等整份报告完成才出现内容。
7. Agent 综合结果，提交报告草稿；应用校验引用与覆盖情况后保存不可变报告版本。报告包括结论、差异、冲突、未验证项和可执行下一步。
8. 用户在会话继续问“这个局限由哪篇论文支持”，Agent 读取报告和证据回答。项目笔记仍通过明确保存操作维护，不自动塞入整份报告。

## 3. 架构与所有权

继续沿用现有四层和默认 single 模式；所有研究推理使用同一个模型节点和连续工具上下文。模型决定查什么、读什么、保存哪些证据、何时追问及如何综合。应用校验项目归属、审核版本、预算、引用和任务完成条件。详细图见 [GRAPH_DESIGN.md](GRAPH_DESIGN.md)。

| 对象/模块 | 所有者 | 责任与存储 |
| --- | --- | --- |
| Direction / DirectionRevision | 领域、DirectionService | 正式方向与历史提案；SQLite 保存版本及关联论文 |
| ResearchRun / ResearchSpec | 领域、ResearchService | 研究输入快照、阶段、预算、计划和候选版本；SQLite 是业务事实来源 |
| Turn / Graph State | 现有会话运行时 | 一个执行目标与模型/工具历史、中断、检查点；不复制全文及完整研究数据库 |
| ResearchWorkItem | ResearchService | 逐论文领取、失败原因、文档/证据结果和幂等身份；SQLite 保存 |
| Evidence / Claim | EvidenceService / ReportService | Evidence 校验准确原文；Claim 区分 finding、inference、unverified |
| ReportVersion / Artifact | ReportService / Artifact Store | 固定本次引用集合与正文版本；SQLite 元数据和外置 Markdown |
| 用户审核 | HTTP 与现有恢复机制 | 以真实界面操作确认固定版本；模型不能填写“已获批准”跳过审核 |

ResearchRun 是业务对象，Turn 是执行与对话对象，Checkpoint 是执行恢复记录。它们互相引用，但 ID、状态和版本不可混用。保留可选 multi 模式；本阶段主线不依赖子 Agent，首版不新增并行论文分析或第二套模型服务。

## 4. 数据约定

以下均为拟新增的应用类型，以现有 tasks/CONTRACTS.md 为起点细化，属于本项目内部接口。

- **Direction**：ID、ProjectID、Name、Description、State、PaperIDs、Questions、Revision；补充来源 SessionID、研究空白和可行性说明。State 为 proposed / accepted / rejected / archived。编辑产生历史版本，接受操作必须绑定当前 Revision。
- **ResearchSpec**：问题、年份、来源、候选/分析上限、SelectedPaperIDs；补充方向 ID/Revision、项目 Revision、分析字段、内容要求（允许摘要/要求全文）。快照在创建任务时冻结。
- **ResearchRun**：ID、ProjectID、SessionID、TurnID、State、Spec、ReviewRevision、SelectedPaperIDs、EvidenceIDs、ArtifactIDs；补充 Phase、计划 Revision/Hash、图定义版本、累计预算和来源任务 ID。
- **ResearchCandidate**：RunID、CandidateID、来源身份、元数据快照、纳入建议/理由、用户决定及审核版本。CandidateID 是任务候选身份，不冒充已入库 PaperID。
- **ResearchWorkItem**：RunID、PaperID、文档版本、分析规格哈希、状态、EvidenceIDs、失败原因。唯一工作身份为 RunID + PaperID + DocumentID + 分析规格哈希。
- **Claim**：沿用 ID、ProjectID、Text、EvidenceIDs、Kind，补充 RunID；finding 必须有本任务纳入论文的可定位证据。推断也记录支撑证据，未证实内容明确标为 unverified。
- **ReportVersion**：RunID、Version、ArtifactID、纳入集版本、EvidenceIDs、ClaimIDs、生成时间和质量标记。生成新版本不覆盖旧版本。

论文按现有项目规则去重；候选、研究任务、审核和逐篇状态均按项目校验。方向不足以证明研究空白，界面明确显示其为待验证提案；没有代表论文时标记证据不足。

## 5. 工具设计

### 5.1 按能力划分工具

现有文件、Shell、修改、论文和笔记工具保留。阶段 04 原计划新增 9 个独立工具，现优先复用读取和交互能力，研究成果暂设计一个 `submit_research_result` 入口；不把“只能新增一个工具”作为硬约束。服务方法、HTTP 端点和模型工具不要求一一对应；暂停、恢复、取消、审批入账等仍由应用内部处理。

| 能力 | 给模型的工具 | 应用内部承担 |
| --- | --- | --- |
| 浏览、读取、检索已有任务和报告 | 现有 `list_directory`、`read_file`、`search_files` | 当前项目资料视图、业务查询和 Artifact 读取 |
| 撰写草稿与代码 | 现有 `write_file`、`apply_patch`、`run_shell` | 沿用工作区范围与确认规则；草稿文件不等于正式业务结果 |
| 外部论文检索、归档与获取 | 现有 `search_papers`、`save_paper`、`list_papers`、`read_paper` | 来源访问、去重、获取/解析；ResearchRun 中登记真实候选 |
| 原文证据检索和保存 | 阶段 03 计划的 `search_project_evidence`、`save_evidence` | 返回带文档/块身份的检索命中；逐字引用与来源校验 |
| 澄清、计划审核、候选勾选 | 现有 `ask_user` 扩展为有类型表单 | 绑定业务版本、Graph 中断与可信回答；不再新增两个 review 工具 |
| 提交研究成果 | 新增 `submit_research_result` | 分派到方向、计划、逐篇分析或报告用例，完成版本与引用校验 |

两个证据工具仍有独立价值：文件搜索能找到文本，但不会自动建立稳定 DocumentID/BlockID 引用，也不执行 Quote 和项目归属校验。它们属于阶段 03 的证据基础，阶段 04 不重复新增同类工具。模型只需找普通报告段落时使用 `search_files`；需要形成可追溯证据时使用证据工具。

### 工具扩展与拆分规则

增加能力时，优先更新同一职责的现有工具，并同步完成以下变更，不能只修改提示词或工具描述就宣称支持新能力：

| 部分 | 必须同步更新的内容 |
| --- | --- |
| 参数与 schema | 输入类型、字段说明、枚举、条件必填、默认值和校验；复用原字段能准确表达时不重复增加参数 |
| 工具描述 | 能处理的对象、使用场景、边界、副作用和失败条件；让模型能正确选择及填参 |
| 执行方法 | 参数解码、来源分派、业务用例调用、作用域/版本校验和错误返回 |
| 返回结果 | 新能力的结构化结果、来源/版本、截断或继续读取提示，以及相应前端消费 |
| 验证与兼容 | 旧调用仍可用；新参数经真实工具入口执行有效；新定义和旧检查点分别恢复 |

具体落实：read_file/list_directory/search_files 的 path 参数说明覆盖工作区路径与 research://；执行方法接入项目资料解析器，保留原工作区路径校验。ask_user 更新 Kind、审核所需 expected_revision、描述、表单执行及有类型结果；旧参数仍默认 clarification。read_paper 扩展阅读范围时，同步更新 ReadPaperArgs、Section 说明、定位参数与实际文档读取方法。

如果多个分支需要大量互斥字段、参数 schema 难以清楚表达、描述需要反复解释分支，或权限/确认/副作用规则差异很大，则按职责拆分。拆分后的每个工具应有简单明确的输入和结果，复用内部服务与校验。字段数量和工具数量均不作为唯一判断依据，不用任意 action/method 参数把所有能力塞入一个入口。

当前研究成果入口在实施参数 schema 时专门复核这条规则：方向提案、计划、逐篇分析和正式报告可按需要拆出明确工具；读取资料仍复用通用工具，不因成果提交拆分而重新新增重复的 get/read 工具。

同一图定义的工具及参数保持稳定，避免随业务阶段更换定义影响提示前缀；能力扩展或拆分使用新的定义版本，旧执行保留原 schema 与方法。能否提交某类结果仍由业务阶段校验。

### 5.2 用 read_file 读取项目资料

现有 `read_file` 通过本机文件系统读取工作区普通文件，尚不能直接查询 SQLite。实施时在应用层加入轻量的项目资料解析器：普通路径沿用原逻辑，`research://` 路径查询当前 Scope 下的业务记录或已保存 Artifact，再通过相同工具返回文本；不增加新的读取工具，也不让模型读取数据库文件。

```text
research://index.json                         当前项目资料索引
research://directions/index.json              方向列表与详情引用
research://runs/index.json                    任务列表与状态摘要
research://runs/<runID>/state.json             范围、阶段、进度和版本
research://runs/<runID>/plan.json              当前检索计划
research://runs/<runID>/candidates.json        候选、推荐理由和用户决定
research://runs/<runID>/matrix.json            已完成分析与证据引用
research://reports/<reportID>/report.md        具体不可变报告正文
```

模型调用示例：

```json
{"path":"research://reports/report-1/report.md","start_line":1,"end_line":60}
```

- `research://` 是应用的只读逻辑资料地址，已支持索引、方向、论文及已知文档/证据；任务/报告地址随后续批次开放，不可交给 Shell。`list_directory` 和 `search_files` 使用同一解析器、同一项目范围和读取上限。
- 任务资料按请求从数据库一致性快照生成，不另存一份可被文件编辑改写的任务事实；报告正文由 Artifact Store 读取。任务摘要保持短小，候选/矩阵按有界页返回，后续页引用绑定该业务 Revision，不能拼接不同版本的数据。
- 当前任务在上下文里只注入小型索引和资料地址；已知地址直接读取，无需先扫描全项目。普通文件路径继续按既有工作区规则处理，不为读取资料放开整个数据根目录。
- 资料地址不接受 user_id/project_id；当前 Scope 由应用注入。跨项目任务或 Artifact 返回不可访问；`write_file` 和 `apply_patch` 拒绝修改逻辑资料地址，不能借修改 JSON 伪造批准或完成状态。
- 报告 ID 指向固定版本；读取返回来源 Revision/哈希及截断提示。可变任务资料变化后，旧审核仍通过版本校验拒绝。

### 5.3 研究成果提交入口

当前候选方案中，`submit_research_result` 负责提交结构化研究成果，使用 `kind` 区分固定的四种 schema；是否保留合并入口，按上述复杂度规则在实施前核对。无论合并还是拆分，均不接收任意方法名或任意 CRUD action：

| kind | 结果内容 | 内部用例 |
| --- | --- | --- |
| direction | 方向提案、代表论文、问题和可行性 | DirectionService 保存 proposed；正式接受仍是用户操作 |
| plan | 检索式、纳入/排除条件、范围与期望版本 | ResearchService 保存计划；聊天内首次提交时幂等关联当前 Turn 的 Run，已有 Run 则更新计划 |
| paper_analysis | 论文字段、unknown 项和已保存 EvidenceID | ResearchService 校验纳入集、文档和证据，提交一个 WorkItem 结果 |
| report | 报告草稿、Claim/Evidence 引用和覆盖说明 | ReportService 校验、渲染并保存不可变报告版本 |

Scope、当前 Turn/Run 从可信上下文注入；更新已有对象须带期望版本，首次提交由应用登记身份，不让模型管理检查点和执行状态。工具返回正式对象 ID、Revision、可读资料地址或具体校验错误。普通 `write_file` 可以保存研究草稿，但不代替正式提交；正式结果需要的引用校验、幂等和事件不能省略。

报告仍由同一个 Agent 综合，ReportService 不再启动隐藏模型。矩阵字段按研究问题制定：默认 problem / method / dataset / metrics / result / limitation，tools 等字段可扩展。

## 6. 审核、恢复与长任务

- ResearchRun State：queued / running / awaiting_input / paused / failed / completed / cancelled；Phase：planning / searching / reviewing / analyzing / reporting / done。状态与业务阶段分别展示。
- 从页面启动时，`ResearchService.Start` 在同一个 admission 事务创建 Run、关联 Turn 和初始事件；会话已有活动 Turn 返回 409。聊天首次提交 plan 时，应用内部幂等登记并关联当前 Turn，不另提供 create 工具，也不调用 Send 导致会话自锁。
- `ask_user` 的计划/候选表单分别绑定 RunID、Revision、Hash、待回答 InterruptID。审核动作与待恢复动作先在同一事务落库，再由 worker 使用现有 PendingInterrupts / Resume 机制消费。崩溃后从已落盘动作继续，不能只写“已审核”却遗失恢复答案。
- 用户修改检索式或纳入规则使旧候选审核失效；已确认的输入快照不原地修改。修改问题或年份等输入范围时创建关联的新 Run。已保存旧结果仍可查看。
- 选中集合必须是当前候选版本的子集；全部排除结束为 cancelled 并保留原因，不生成空的成功报告。部分论文失败逐篇显示，用户可重试、排除或接受带限制的报告。
- 暂停在已完成工具调用的安全边界生效；不声称能暂停外部 HTTP 的半次操作。取消停止新增工作，保存已有证据和报告，不做业务回滚。
- 累计候选、分析、模型与工具预算落库，恢复与重试不清零。沿用默认 30 候选/12 分析，允许范围仍为项目现有 100/30 上限。初版研究任务拟默认 64 次模型、128 次工具调用，可在启动前调小；普通问答保留原上限。
- 模型调用前登记预算预留与调用身份，成功后写 Usage；崩溃导致用量未知时保留 unknown 并计入保守预算，不声称 token/费用恰好一次。公开请求记录来源、时间、状态及重试建议。
- 单次活动执行段拟上限 15 分钟；用户等待不计入活动时间。到界限在安全边界转 paused；重启恢复仍使用累计预算。实现需检查现有 5 分钟 worker、64 步主图和 125 秒工具超时，不能只新增业务表而沿用不适合长调研的执行上限。
- 新图/工具/Codec 使用独立定义版本，保留已有 single-v1 / multi-v1 的恢复入口；不能把旧检查点直接交给新图。遇到框架限制记录最小复现，单独说明，不在本任务修改框架。

编辑或重试研究起始消息时：运行中沿用现有限制；编辑创建新 Run 并关联旧 Run，不改旧快照；失败重试从同 Run 台账继续；已完成任务再次重试创建新 Run。会话内容替换不删除已经保存的论文、证据与报告。

## 7. 前端与接口

在现有项目导航增加“研究方向”“调研任务”。会话仍是主要入口，计划和候选使用业务卡片，点击可打开完整任务页。任务页包含范围、检索记录、候选、逐篇进度、证据矩阵和报告。

聊天显示简短有意义进度，例如“找到 8 篇候选”“已完成 3/5 篇分析，1 篇只有摘要”。正文沿用模型真实增量，结果表和报告按稳定对象 ID 更新；刷新读取持久快照并从 SSE 游标续读。内部 JSON、隐藏推理、整篇全文不进入聊天展示。

当前项目路由：`/p/:projectId/directions`、`/p/:projectId/research`、`/p/:projectId/research/:runId`。报告引用通过 EvidenceID 打开冻结原文，不依赖模型拼接外部 URL。

HTTP 前缀均为 `/api/projects/{projectID}`：

| 接口 | 用途 |
| --- | --- |
| `GET/POST /directions`、`PATCH /directions/{id}` | 列出、手工创建/编辑、生成版本 |
| `POST /directions/{id}/accept`、`reject`、`archive` | 真实用户操作，绑定 revision |
| `GET/POST /research`、`GET /research/{runID}` | 列表、启动及研究状态 |
| `GET /research/{runID}/snapshot`、`events` | 共用 UI 快照、事件游标与 SSE |
| `POST /research/{runID}/turns/{turnID}/resume` | 回答澄清、计划/候选审核，登记可靠恢复动作 |
| `POST /research/{runID}/pause`、`continue`、`retry`、`cancel` | 任务动作、revision 与独立幂等键；retry 可携带单篇身份 |
| `GET /research/{runID}/matrix`、`reports` | 查看逐篇分析及报告版本 |
| `GET /reports/{reportID}` | 读取具体不可变版本 |

这些接口已接入现有应用服务；会话 API 继续兼容。`internal/ui` 在 HTTP 与应用服务之间统一展示数据和交互入口，React 消费 `ui` 协议；具体见 [UI_IMPLEMENTATION.md](UI_IMPLEMENTATION.md)。CLI 命令延期，未来只增加输入/渲染适配器，复用相同 UI 服务或 HTTP 协议，审核仍须明确用户输入。

## 8. 报告与验收标准

报告固定包含：研究问题与范围、来源/检索式/检索时间、纳入与排除、证据矩阵、方法比较、冲突和未知项、研究空白假设、下一步实验建议及引用。每篇标记全文/仅摘要/不可用。事实必须引用有效证据；程序只保证引用身份和原文准确，仍需抽样检查证据是否支持语义。

报告保存以 RunID、草稿哈希和动作幂等键识别同次提交。检查点重放返回原版本；用户明确重新生成才追加新版本，避免恢复时无意产生多份报告。

核心自动化场景：5 篇 fixture 候选 → 用户排除 1 篇 → 4 篇进入分析，其中 1 篇只有摘要 → 矩阵逐篇显示 → 报告不使用排除论文 → 点击引用回到正确版本 → 刷新/重启后继续追问。另测旧审核、跨项目引用、429、单篇失败、暂停恢复、预算耗尽和消息编辑重跑。

完成实际验证后才创建 ACCEPTANCE.md；离线流程、真实来源获取和真实模型质量分别记录。实施不读取真实 API Key，不自动调用收费模型，不自动启动或重启用户服务。

## 首批实现：计划审核与执行身份（历史 v4）

首批工具定义为 single/multi-v4，旧 v1/v2/v3 保留原工具定义与审核形状。默认单 Agent；一个 Run 绑定一个 Turn，HTTP 通过消息接口的 research 字段原子登记，聊天中首次 plan 提交可绑定当前 Turn，不递归发送消息。

- 执行身份：RunID 表示研究任务，OperationID 来自框架持久运行/节点/批次调用键，或用户提交审核时的幂等键；AttemptID 表示一次实际模型/工具执行。三者都不从内容哈希生成。
- 内容版本：PlanRevision 只随计划内容提交或用户编辑递增，确认记录 ApprovedPlanRevision；SpecHash/PlanHash 是内容指纹。执行次数、Usage 和 HTTP 重发不增加内容版本。
- 幂等顺序：先查询 OperationID 与请求指纹，同请求返回已保存结果；同身份换内容冲突；首次执行才检查 expected_revision。计划保存与幂等结果在同一事务中落盘。
- 输入快照：ResearchSpec、项目/已确认方向版本在任务创建时冻结。正式任务不支持原消息编辑后复用旧 Run，当前返回明确 409 并引导新建任务；失败重试保留原 Run 与累计预算。完成任务重新运行、新 Run 自动克隆留给后续批次。
- 审核：ask_user(kind=research_plan) 绑定后台计划版本和哈希，前端可修改说明、检索式、纳入/排除条件。可信用户 Resume 在同一 SQLite 事务保存用户修改、确认版本和 TurnAction；恢复时校验原中断，并把数据库认可的结构化结果交回原模型。
- 预算：实际模型/工具调用前写执行账本，模型默认 24 次、工具 64 次；新工具批次、恢复及子 Agent 使用同一账本。首次计划前的调用也计入 Turn，创建 Run 后全部纳入预算。Usage 缺失、调用失败或未收到结尾均保留 unknown，不当成零。Token 当前用于统计，不作为精确金额或 Token 硬上限。
- 门槛：正式任务未确认计划时，研究工具拒绝检索/获取并给出模型可处理的错误；普通问答仍可直接检索。最终文本只表示回复结束，计划未确认时返回缺项并继续原模型循环；有调用/步数和累计预算上限。Run 不因最终文本标记整个研究完成。
- v4 首批审核后的阶段为 searching，仅交付首轮检索；v5 增加候选持久化和用户选择；当前 v6 增加逐篇分析，矩阵与报告仍未实现。

已开放地址为 research://runs/index.json 和 research://runs/<id>.json，后者包含范围、计划和执行摘要。上文细分 state/plan/candidates/matrix/report 地址是后续目标，尚未全部开放。任务索引最多 100 项并沿用每页 20 条的版本绑定分页。
## 04-B2 已实现：候选审核

本节记录 single/multi-v5 的历史交付。v4 保留原计划审核工具定义与首轮结束条件；v1/v2/v3 继续按旧构造规则恢复。本批不改框架，也不新增模型工具。当前新任务为 v6，见下节。

- `search_papers` 在正式 Run 中使用冻结年份/数量范围，按规范 SourceKey 去重，返回稳定 CandidateID、候选版本和哈希；候选总量受任务上限限制。OperationID 记录成功检索结果，重放先返回原结果，不再访问来源或消耗新的工具预算。响应保存前崩溃时，外部只读检索仍可能重发。
- `ask_user(kind=research_candidates)` 接收当前版本和覆盖全部候选的推荐/理由，只引用已经持久化的元数据。应用发布包含推荐的新快照，再以 RunID、Revision、Hash、CandidateIDs 进入中断。前端通过现有任务详情 API 加载元数据，核对版本和哈希；模型通过 `research://runs/<id>/candidates.json` 分页读取，后续页绑定审核内容版本。
- 用户决定为 approve / search_again / cancel。approve 要求 1 至分析上限篇，分析上限不超过冻结上限；每个候选有可编辑理由。search_again 要求全部排除，保留历史并开启新候选轮次；补充要求以结构化反馈返回原 Agent。没有结果也必须展示重检/取消入口。
- 复用 Resume API，在同一事务验证当前 pending、Turn Revision、Run、候选 Revision/Hash、完整候选集合及数量，登记审核历史、自动归档选中论文并保存 CandidateID→PaperID 映射，同时写入 TurnAction 和排队。已有论文沿用身份，不覆盖阅读笔记。相同审核操作重发先查原动作，再检查版本；同身份换内容冲突。
- 原 ask_user 调用恢复后从仓储读取已认可的结构化决定及 PaperID，继续原模型上下文。read_paper/save_paper/save_evidence 拒绝未纳入论文；原文检索在数据库查询时限定到纳入集。list_papers 和文件工具的论文/文档/证据逻辑地址遵循同样范围，防止绕过业务门槛。普通聊天保持项目级工具行为。计划修改使候选审核失效，需重新确认。

v5 确认候选后的 Run 阶段为 `candidates_selected`，表示候选准备完成；旧图仍保留该结束条件。运行统计继续走既有 callback，调用预算跨恢复/重试累计。

## 04-B3 本批实现（v6）

候选审核批准时同事务创建逐篇 WorkItem，phase 进入 analyzing。沿用同一模型上下文顺序阅读并提交五字段分析，先成功读取冻结文档，再用真实 Evidence 支持 finding；未知项明确标注。每篇结果立即落盘并通过 research.progress 可见；所有篇均 completed/failed 后进入 analyses_completed，矩阵与正式报告仍未开放。

工作身份绑定 RunID/PaperID/DocumentID/SpecHash，内容修订与执行 AttemptID/OperationID 分离；成功操作重放不占新预算或新增版本。失败单篇可以重试，保留其他成功项、冻结文档和累计预算；编辑问题或完成后再执行派生新 Run，PreviousRunID 指向旧成果。

暂停/继续在安全边界通过 graph.Interrupt 恢复原上下文；15 分钟执行段到期自动请求暂停，取消停止新工作。API 复用 `/api/projects/{projectID}/research/{runID}` 的 pause/continue/retry/cancel 动作。前端提供进度、可展开逐篇结果与引用跳转。实施细节及实际验证见 ANALYSIS_IMPLEMENTATION.md、GRAPH_DESIGN.md 和 ACCEPTANCE.md。


## 独立研究任务（v7）

研究与会话的上下文、队列、预算及交互页面分开；采用同一套 Graph 工具循环实现两个执行容器。会话只委派和接收简短成果投影，研究过程不自动回灌会话。具体输入、身份、持久化与前端职责见 [独立研究实施](INDEPENDENT_AGENT_IMPLEMENTATION.md)；逐篇分析规则继续沿用 v6，04-C 尚未开放。

## 04-C1 当前实现（single-v8）

新独立研究在全部 WorkItem 终态后进入 reporting，由原 Research Agent 提交结构化报告；真实引用、分析版本、完整矩阵哈希、固定章节及不可变 Artifact 校验通过后进入 reported。普通会话升级 single/multi-v9，自动接收研究摘要和位置，可通过消息附件绑定指定报告版本并按需读取；旧 v7 研究仍在 analyses_completed 结束。详细类型、参数、事务与验证见 [报告实施](REPORT_IMPLEMENTATION.md)和[报告对话](REPORT_CONVERSATION.md)。
