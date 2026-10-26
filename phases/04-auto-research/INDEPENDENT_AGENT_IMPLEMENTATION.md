# 独立 Research Agent 实施计划

> 用户已确认实施，按 executing-plans 在当前工作区逐项执行；保留已有改动，不修改框架，不提交或推送，不委派子代理。

**目标：** 会话只委派研究并接收结果摘要与资料位置，研究的上下文、执行队列、审核和进度独立。

**架构：** ResearchRun 作为持久任务，内部复用现有 Turn、Graph、检查点和观测实现。新增隐藏的研究会话作为执行容器，不复制来源会话历史或项目笔记。普通聊天和研究分别领取队列；任务页负责所有研究交互，结果卡片及后续会话只读取简要投影。

**技术栈：** 现有 Go/goHarness/SQLite、React/TypeScript/Playwright；不引入新的基础服务。

## 类型与文件边界

| 区域 | 文件与职责 |
| --- | --- |
| 领域 | research_task.go 定义任务创建、来源引用和摘要；Turn.ExecutionKind 区分执行队列 |
| 持久化 | 012 迁移、research_tasks.go：同事务创建内部会话/Turn/Run，按创建身份幂等；sessions.kind 隐藏内部容器 |
| 应用 | research_tasks.go：冻结显式输入、启动、读取摘要；turns.go 分开领取队列及注入简要结果 |
| Graph | v7 普通会话新增 start_research，正式研究沿用既有计划/候选/逐篇图；旧 v1–v6 保留定义 |
| HTTP | 项目级 POST /research、任务 snapshot、来源会话任务摘要；复用可信审核与控制接口 |
| 前端 | ResearchTaskPage 独立详情/审核/进度；ResearchTaskCards 仅显示摘要及查询链接；任务列表直接新建任务 |

## 执行顺序

- [x] 先写隔离回归：来源会话存在历史/笔记和活动 Turn 时，研究依然创建；内部上下文不含它们，内部会话不出现在普通列表，重复创建不产生孤儿。
- [x] 实现领域、012 迁移和原子创建；接口 `CreateResearchTask(ctx, scope, draft)` 返回 Run/Turn，`ResearchTaskSummaries(ctx, scope, sessionID)` 只返回简要投影。
- [x] 先写阻塞回归：研究模型等待期间，普通聊天仍完成；实现 `ClaimQueuedKind(ctx, kind)` 与两个独立 worker，恢复仍走原 runTurn。
- [x] 扩展 v7 Graph：工具 `start_research(question,constraints,paper_ids)` 注入可信来源与调用身份，立即返回任务 ID、URL、resource_path；研究内部不提供再次启动研究的工具。保留旧版本 schema 与恢复。
- [x] 写 HTTP/浏览器回归：发起后聊天可继续，任务页确认计划/候选，结果刷新保留；普通会话只出现一张任务卡，无研究中间过程。
- [x] 实现任务详情、审核与结果卡；完成时直接投影已保存分析摘要，不新增模型复述调用；下一轮问答按需读取 research://runs/<id>.json。
- [x] Go 全量 test/vet、前端单测/typecheck/build、全量 Playwright，检查截图并同步 README/设计/验收。

## 验收断言

```text
research.session_id != origin_session_id
research.turn_id != origin_turn_id
research.context.history == [] && research.context.notes == []
研究等待模型时，chat.state == completed
同启动身份重放 -> 同 Run；同身份不同输入 -> conflict
删除来源会话 -> 研究任务及成果仍存在
来源会话卡片只含状态/简要结果/位置，无全文、工具结果或研究消息
```

旧的会话消息携带 research 参数作为兼容入口继续支持；新 UI 和新会话工具统一创建独立任务。旧研究检查点不自动升级。阵阵、Claim 与正式报告仍为后续 04-C。


## 实际完成与验证（2026-10-25）

- 领域、012 迁移、原子任务创建、独立队列、v7 工具装配、HTTP、前端页面和来源摘要卡均已落地。内部执行容器不接受普通消息，普通会话不自动请求完整研究列表。
- 创建事务同时保存初始用户消息。回归先复现了遗漏消息导致的重试失败，再验证修复；来源删除后的再次调研沿用已验证的来源引用；取消后重新调研派生新 Run，原取消状态保持不变，同启动身份重放仍返回同一新任务。
- 真实 Graph 回归覆盖工具委派、服务重建后计划/候选审核、逐篇完成、结果投影和下一轮仅携带摘要。并发测试确认研究请求在途时聊天完成。
- SQLite 故障注入验证事务无孤儿；用户/项目隔离、同身份重放及变参冲突、两个队列领取、整轮重试和单篇重试均通过。
- 独立模块全量 Go 测试及 vet、前端 25 项单测/typecheck/build、全量 29 项 Playwright 均通过，已检查任务页和来源会话截图。
- 测试只使用临时数据库、随机测试端口、离线来源和本地脚本模型，没有调用真实收费模型或操作既有服务。旧 v1–v6 和旧会话 research API 继续兼容；阵阵、Claim 和正式报告尚未交付。

任务页用 1 秒轮询同步持久进度和文本快照；来源卡片用 1.5 秒轮询同步状态。chat/research 各一个 worker，同类任务排队，暂不增加研究并发池或独立进程服务。
