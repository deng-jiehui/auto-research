# 围绕报告继续对话实施计划

> **For agentic workers:** 使用 executing-plans 在本会话逐项执行，保留当前尚未提交的工作。

**Goal:** 从指定版本报告进入普通会话，多轮按需阅读报告及证据，刷新、编辑与重试保持版本一致。

**Architecture:** 报告选择属于消息输入，解析后的可信引用冻结在 Turn.ContextJSON 中，并通过共用 UI 快照展示。普通会话 Graph 只接收报告元数据及只读地址；模型决定何时读取、追问或委派新研究。历史报告的矩阵和引用从报告快照读取。

**Tech Stack:** Go、SQLite JSON 上下文、goHarness Graph、React、TypeScript、Playwright。

## 全局约束

- 仅修改 extensions/examples/auto-research；不改框架、不调用收费模型、不操作用户已有服务。
- 复用 read_file 与 start_research，不增加工具名，不实现 CLI 命令。
- 保留现有未提交内容；本次不提交、推送或清理。
- 请求省略 report_context 表示继承上一轮；{clear:true} 显式移除；{run_id,report_id} 选择不可变报告。
- 代码校验归属、执行身份和版本；结论语义由模型判断，界面不声称代码能证明推理正确。

## 文件与所有权

- domain/report_context.go：输入选择与可信引用的数据契约。
- app/report_context.go：校验、继承及按需阅读说明；turns.go 负责接入发送与执行。
- infra/sqlite/runtime.go：轮次引用投影及幂等请求冲突检查，复用现有 context_json，无新表。
- app/project_resources.go：报告专属 matrix.json、references.json 只读快照。
- single_agent_graph.go：新会话定义升级 v9；兼容既有 v7/v8，修正普通会话报告能力提示。
- web/components/ResearchReportPanel.tsx：报告提问入口与历史版本深链。
- web/components/ReportContextCard.tsx、web/useReportContext.ts：引用恢复、切换、移除、新版本提示。
- web/pages/SessionPage.tsx、web/api/client.ts、web/types.ts：共用消息协议与发送状态。

## Task 1：可信引用与持久多轮上下文

**接口：** SendInput.ReportContext *domain.ReportSelection；Turn.ReportContext *domain.ReportContext。

- [x] 先写真实 SQLite + TurnService 测试：显式选择 v1、下一轮省略继承、clear 移除、跨项目/错 Run 拒绝、同键换报告冲突。
- [x] 执行 `go test ./internal/app -run TestReportConversation -count=1`，确认缺少引用能力导致失败。
- [x] 实现校验与持久化：输入只接收 ID；可信标题、版本、摘要和地址由报告对象生成。幂等只比较输入选择，不比较每次变化的历史和项目快照。
- [x] 新 v9 普通会话输入追加一次报告元数据消息；沿用原工具循环，修复“报告尚未开放”过时提示，既有检查点保持兼容。
- [x] 执行测试，验证重建服务、编辑、重试仍绑定原报告。

```json
{"text":"哪种适合单卡复现？","idempotency_key":"message-1","report_context":{"run_id":"run-1","report_id":"report-v1"}}
```

## Task 2：历史快照阅读与有出处回答

**接口：** read_file("research://reports/<reportID>/matrix.json")、references.json，沿用分页与内容版本。

- [x] 先写回归：报告 v1 保存后改动当前矩阵，报告地址仍返回 v1；跨项目不能读。
- [x] 执行定向测试并确认新地址尚不可用。
- [x] 从 ResearchReport.Matrix、References 生成只读分页内容，返回绑定身份；原报告地址继续可读。
- [x] 用脚本模型走真实 Graph：先读报告，再读快照证据，返回实际引用；追问不重新启动研究，报告正文不重复注入系统提示。
- [x] 验证工具结果、持久答复和快照引用，覆盖 single/multi。

## Task 3：前端入口、切换与验收

**接口：** 报告按钮进入 `/p/<project>/s/<session>?report=<reportID>`；服务端仍验证报告归属。

- [x] 先写浏览器用例：报告进入来源会话，来源不存在时新建；发送、追问、刷新、移除、版本切换；无效引用禁止发送。
- [x] 执行新增用例确认入口缺失导致失败。
- [x] 报告引用卡片显示名称/版本/摘要/打开按钮，支持版本选择及新版提示；发送成功后清理 URL 选择，靠服务端快照恢复。
- [x] 报告页识别 report 查询参数，历史链接准确打开对应版本；引用载入失败显式提示，不悄悄发送无引用问题。
- [x] 运行 Go 全量测试及 vet、前端单测/typecheck/build、全量 Playwright；截图检查桌面与手机布局。
- [x] 更新阶段实施与验收文档，记录实际结果；CLI 继续延期。

自检：覆盖报告入口、来源缺失、结构化引用、多轮继承、移除切换、刷新重试、旧版隔离、权限及幂等、现有工具复用。未扩展工具数量或研究 Agent 上下文。
