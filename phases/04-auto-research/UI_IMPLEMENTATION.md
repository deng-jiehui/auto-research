# 共用 UI 交互层实施计划

> 使用 writing-plans 制定，使用 executing-plans 在当前工作区逐项执行。用户已要求先实现共用 UI 层，CLI 命令延期。本轮延续已有未提交成果，不派生子 Agent，不修改框架，不提交推送。

**Goal:** 前端与未来 CLI 使用同一套快照、用户中断、回答及事件接口，先完成 Web 接入。

**Architecture:** `internal/ui` 是应用服务的展示与交互入口，不持有第二套执行状态。下接现有 TurnService/ResearchService，上接 HTTP；未来终端适配器直接复用 UI 服务。Graph 继续产生持久中断，UI 将其转换为明确的交互类型；审核、权限、幂等和恢复仍由原应用服务及仓储负责。

**Tech Stack:** Go、SQLite、现有 HTTP/SSE、React/TypeScript、Vitest、Playwright；不增加依赖。

## 范围与所有权

| 文件 | 职责 |
| --- | --- |
| internal/ui/model.go | Target、State、Interaction、View；将内部中断转为端无关数据，未知类型只读提示 |
| internal/ui/service.go | Snapshot、Events、Respond、Act；根据 session/research 目标绑定真实会话与 Turn |
| internal/ui/*_test.go | 类型投影、过期数据、研究隔离、跨项目拒绝、幂等回答、重建服务继续 |
| internal/transport/httpapi/ui.go、turns.go、research.go、router.go | HTTP/SSE 适配 UI 服务，保持已有快照字段；研究回答不要求调用方操作隐藏会话 |
| web/src/interaction.ts | 端无关交互类型的消费及旧快照兼容 |
| web/src/components/UserInteraction.tsx | 统一澄清、计划审核、候选审核和未知交互显示 |
| web/src/components/ResearchPlanReview.tsx、ResearchCandidateReview.tsx | 通过 Target 提交同一回答协议，保留可编辑表单 |
| web/src/pages/SessionPage.tsx、ResearchTaskPage.tsx | 消费统一 UI 数据和组件；研究快照自带 Run，减少重复请求 |

UI 协议嵌入已有快照的 `ui` 字段：`version`、`target:{kind,id}`、`event_cursor`、`interaction`、`available_actions`。Interaction 保留稳定中断 ID、kind、问题、可编辑选项、自定义输入配置及审核绑定版本；计划和候选字段沿用领域类型。`research` 字段只在研究快照返回对应 Run。消息与事件沿用已有契约，不新增一套模型工具或检查点格式。

```go
func (s *Service) Snapshot(ctx context.Context, scope domain.Scope, target Target, limit int) (View, error)
func (s *Service) Events(ctx context.Context, scope domain.Scope, target Target, after int64, limit int) ([]domain.SessionEvent, error)
func (s *Service) Respond(ctx context.Context, scope domain.Scope, target Target, turnID string, in app.ResumeInput) (app.TurnActionResult, error)
func (s *Service) Act(ctx context.Context, scope domain.Scope, target Target, turnID, kind string, in app.ResearchControlInput) (app.TurnActionResult, error)
```

Action 是界面提示，不构成授权；提交仍交给原应用服务校验。回答成功重放不能因 UI 当前已结束等待而失败。研究目标绑定固定 Run/Turn，不能携带另一个任务或普通会话的 TurnID。事件提供持久递增游标，HTTP 使用 SSE，未来 CLI 可以轮询；网络断开不会取消执行。未知中断不伪装成普通问题。

## 任务

### 1. 实现展示协议及应用入口

- [x] 先写 `TestPresentationNormalizesInteractions`：手工构造澄清/计划/候选/暂停/未知中断，断言结构化交互；非等待状态不得显示旧问题，事件游标取最大 ID。
- [x] 先写 `TestResearchUIUsesBoundTargetAndDurableAnswers`：临时 SQLite 创建独立任务，运行至计划审核；通过 UI 获取身份并提交回答，重复提交保持幂等；另一 Run 的 Turn 和另一项目均拒绝。重建 UI 服务读取同一持久状态。
- [x] 运行 `go test ./internal/ui`，观察缺少实现造成失败；实现 model.go、service.go 后重跑。

```go
view, err := screen.Snapshot(ctx, scope, ui.Target{Kind: "research", ID: run.ID}, 100)
answer := app.AnswerInput{InterruptID: view.UI.Interaction.ID,
    ResearchPlan: &domain.ResearchPlanReview{RunID: run.ID, PlanRevision: run.PlanRevision,
        PlanHash: run.PlanHash, Decision: "approve", Plan: run.Plan}}
_, err = screen.Respond(ctx, scope, view.UI.Target, view.Turn.ID,
    app.ResumeInput{Revision: view.Turn.Revision, IdempotencyKey: "approve-1", Answers: []app.AnswerInput{answer}})
```

### 2. HTTP 适配与前端接入

- [x] HTTP 回归先断言既有 snapshot 返回 ui 协议，研究快照包含相同 Run，并使用 `/research/{runID}/turns/{turnID}/resume` 回答；错误 TurnID 返回 409，跨项目返回 404。研究 events 支持与会话相同的 after 游标和 SSE。
- [x] 实现 HTTP 适配；普通会话和研究都通过 UI Service 读取、回答与控制，保留旧路由兼容。
- [x] Vitest 先断言结构化 UI 优先于旧 Base64 中断，未知类型不可回答，明确无交互不回退旧数据，旧服务快照仍兼容。
- [x] 实现 interaction.ts、UserInteraction 和目标感知的回答 API；两个页面使用同一组件。研究页面直接读取快照附带的 Run。

### 3. 验证与文档

- [x] 浏览器验收实际返回 ui 协议，前端不读取原始中断仍能完成计划/候选审核；普通澄清保留编辑选项和自定义输入，刷新保留。
- [x] 执行 Go 全量 test/vet、前端 test/typecheck/build、相关及全量浏览器回归；检查实际输出。
- [x] 同步 ARCHITECTURE、README、阶段计划与验收。告告后续追问和整阶段故障注入保留在 04-C2；CLI 实现标记延期，不列为本轮交付或阶段必需入口。

验证均使用临时数据、本地模型和隔离测试端口。程序继续校验告告来源与版本，不以 UI 抽象代替研究质量评估。

2026-10-25 实际通过：Go 全量 test/vet，前端 29 项单测、typecheck/build，全量 32 项 Playwright；截图稳定性调整后另跑 UI 专项 2 项通过。已查看桌面研究页与 390px 会话交互截图。完整记录与手测见 [ACCEPTANCE.md](ACCEPTANCE.md) 的“共用 UI 交互层”。
