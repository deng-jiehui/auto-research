# 单 Agent 与子 Agent 双模式 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 默认使用一个连续上下文的研究 Agent，并通过可选的子 Agent 委派支持复杂任务。

**Architecture:** Graph 保留任务生命周期、检查点、中断恢复、工具执行和结果持久化。单 Agent 模式使用一个模型节点和一个统一工具节点循环；协作模式复用该主循环，只向主模型增加 goHarness subagent 扩展提供的 run_subagent 工具，子 Agent 使用独立消息历史。

**Tech Stack:** Go 1.24、goHarness Graph/LLM/Tool API、goHarness subagent extension、SQLite、现有 AutoResearch SSE。

## Global Constraints

- 不修改 goHarness 框架。
- 默认配置为 `single`；`multi` 只通过主 Agent 的 `run_subagent` 工具委派。
- 工具权限由应用注册表限制，模型不能自行创建工具或绕过项目作用域。
- 子 Agent 不自动共享主 Agent 的聊天历史，只接收委派任务和允许的工具结果。
- 现有旧图测试保持可运行，避免破坏阶段三论文流程。
- 所有新增行为先有失败测试。

---

### Task 1: 模式契约与默认配置

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `main.go`
- Modify: `internal/app/turns.go`
- Test: `internal/app/turns_test.go`

- [x] Add `agent_mode` with `single|multi` validation and default `single`.
- [x] Add TurnService mode setter and pass it to `ResearchGraphDeps`.
- [x] Verify invalid mode is rejected and configured mode reaches graph construction.

### Task 2: 单 Agent 连续工具循环

**Files:**
- Create: `internal/app/single_agent_graph.go`
- Create: `internal/app/single_agent_graph_test.go`
- Modify: `internal/app/research_state.go`
- Modify: `internal/app/interaction_tools.go`

- [x] Write failing test for one model context executing a paper search and answering from its tool result.
- [x] Add unified Agent state and `agent_model ↔ agent_tools` loop.
- [x] Route ask_user interruption back to the same Agent message history.
- [x] Add project-note tool so saving is an explicit Agent tool call.

### Task 3: 协作模式子 Agent

**Files:**
- Modify: `internal/app/single_agent_graph.go`
- Reuse: `go.mod` 已有的 extensions 模块依赖
- Test: `internal/app/single_agent_graph_test.go`

- [x] Register goHarness subagent manager's `run_subagent` only in `multi` mode.
- [x] Use the same model as the parent by default and expose only application-approved tools.
- [x] Verify a parent model can delegate and receive an isolated child result.

### Task 4: 数据流与文档

**Files:**
- Modify: `internal/app/turns.go`
- Modify: `extensions/examples/auto-research/ARCHITECTURE.md`
- Modify: `extensions/examples/auto-research/ROADMAP.md`
- Create: `extensions/examples/auto-research/phases/02-agent-runtime/AGENT_MODES.md`

- [x] Classify unified tool events for SSE without exposing internal prompts.
- [x] Document single and multi mode flows, cache boundary, and known limitations.
- [x] Run Go tests, vet, frontend tests and build, and focused runtime checks with scripted models.

## 实施结果与验证范围

- 模式接收时固定到每轮上下文，定义版本按模式区分；已有 v3 轮次继续按原图恢复。
- 新增测试暴露并修复跨轮前缀改写、项目论文作用域缺失、模式随进程配置变化和正文增量被过滤的问题。
- 保留 SQLite 跨重启恢复、用户确认答案、超长笔记修正与父子 Agent 写入确认测试。
- 验证通过：应用模块 `go test -count=1 ./...`、`go vet ./...`，前端 22 项单元测试及生产构建。
- 未执行收费模型或真实 arXiv 网络复测；未重启用户服务。

