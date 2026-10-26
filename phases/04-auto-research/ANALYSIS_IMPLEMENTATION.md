# 04-B3：逐篇分析实施记录

目标：用户确认候选后，由同一个 Agent 顺序阅读、保存带引用的分析；任务页实时显示进度，支持安全暂停、继续及失败单篇重试。

仅修改 AutoResearch，不修改框架、不调用付费模型、不操作用户运行中的服务。新图 single/multi-v6，保留 v1–v5 的 schema、完成条件和恢复路径。

## 边界与状态

| 对象 | 所有者 | 规则 |
| --- | --- | --- |
| WorkItem | 领域与 SQLite | 纳入审核事务创建；pending → reading → completed/failed；失败重试回 pending，成功项不覆盖 |
| 工作身份 | SQLite | RunID/PaperID/冻结 DocumentID/SpecHash；首次成功阅读绑定文档，后续读取复用该版本 |
| 执行身份 | SQLite / 现有调用账本 | OperationID 重放稳定；AttemptID 表示一次真实执行，预算累计 |
| 分析内容 | 领域与 SQLite | 独立 Revision/Hash；finding 需同论文同冻结文档的有效 Evidence；摘要分析标注级别，unknown 不编造 |
| 暂停 | 应用控制与 Graph | 持久请求；当前模型/工具请求返回后，在下一个调用前中断；继续消费原检查点 |
| 工具 | 应用适配 | 扩展 submit_research_result(kind=paper_analysis)，读取复用 read_file/read_paper，无额外读取工具 |
| 前端 | React | 任务页轮询持久快照，会话沿用进度 SSE；每篇结果可展开、引用可点击 |

矩阵、Claim、正式报告仍属 04-C。编辑冻结问题需新 Run；失败重试复用 Run 和累计预算，已完成研究再次执行需新 Run。

## 实施清单

- [x] 回归先行：排除论文拒绝、成功重放/身份冲突、引用版本与摘要限制、单篇重试累计预算。
- [x] 011 迁移，工作项/分析版本持久化、批准事务建项、快照与进度事件同事务。
- [x] v6 成果 schema、顺序分析约束、冻结读取、完成校验、离线演示。
- [x] 暂停/继续/单篇重试 API；暂停在安全边界生效，恢复原上下文。
- [x] 任务页进度、逐篇卡片与控制；会话进度事件可见。
- [x] 重启、暂停、失败继续和重试回归；Go test/vet、前端单测/typecheck/build、Playwright 验证。

验证使用临时 DB、独立端口和本地来源/模型；实际结果记录到 ACCEPTANCE.md。

2026-10-25：Go 全量 test/vet、前端 25 项单测/typecheck/build、28 项 Playwright 全部通过。正式任务默认 64 次模型、128 次工具调用；15 分钟执行段到安全边界暂停，硬期限额外宽限 125 秒。成功分析重放不再占预算；同执行身份换内容仍冲突。修复 Windows 时钟精度导致的 ID 碰撞，以及问题发布后二次写状态造成的即时审核冲突。
