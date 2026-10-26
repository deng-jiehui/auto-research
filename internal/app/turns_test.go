package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/deng-jiehui/auto-research/internal/domain"
	"github.com/deng-jiehui/auto-research/internal/infra/sqlite"
	"github.com/goharness/goharness/pkg/llm"
	"github.com/goharness/goharness/pkg/schema"
	"github.com/goharness/goharness/pkg/state"
	"github.com/goharness/goharness/pkg/stream"
)

func TestNewTurnServiceUsesFiveMinuteResearchTimeout(t *testing.T) {
	service := NewTurnService(nil, nil, nil)
	if service.runTimeout != 5*time.Minute {
		t.Fatalf("研究轮次超时=%s，期望 5m", service.runTimeout)
	}
	if service.AgentMode() != "single" {
		t.Fatalf("默认 Agent 模式=%q，期望 single", service.AgentMode())
	}
}

func TestTurnServiceAcceptsAgentMode(t *testing.T) {
	service := NewTurnService(nil, nil, nil)
	if err := service.SetAgentMode("multi"); err != nil {
		t.Fatal(err)
	}
	if service.AgentMode() != "multi" {
		t.Fatalf("Agent 模式=%q，期望 multi", service.AgentMode())
	}
	if err := service.SetAgentMode("unknown"); err == nil {
		t.Fatal("未知 Agent 模式未被拒绝")
	}
}

func TestTurnServiceAcceptsWorkspacePolicy(t *testing.T) {
	service := NewTurnService(nil, nil, nil)
	policy := WorkspacePolicy{Root: t.TempDir(), Shell: "pwsh"}
	service.SetWorkspacePolicy(policy)
	if service.workspacePolicy.Root != policy.Root || service.workspacePolicy.Shell != policy.Shell {
		t.Fatalf("工作区策略=%+v，期望=%+v", service.workspacePolicy, policy)
	}
}

func TestTurnServiceFocusInterruptResumeAndIdempotency(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	project, err := store.CreateProject(ctx, "local-user", "研究项目")
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.CreateSession(ctx, domain.Scope{UserID: "local-user", ProjectID: project.ID}, "目标梳理")
	if err != nil {
		t.Fatal(err)
	}
	service := NewTurnService(store, store, store)
	scope := domain.Scope{UserID: "local-user", ProjectID: project.ID}
	first, err := service.Send(ctx, scope, session.ID, SendInput{Text: "帮我整理下一步关注点并保存为笔记", IdempotencyKey: "message-1"})
	if err != nil {
		t.Fatal(err)
	}
	replay, err := service.Send(ctx, scope, session.ID, SendInput{Text: "帮我整理下一步关注点并保存为笔记", IdempotencyKey: "message-1"})
	if err != nil || !replay.Replayed || replay.Turn.ID != first.Turn.ID {
		t.Fatalf("幂等重放=%+v, err=%v", replay, err)
	}
	if _, err = service.RunQueued(ctx); err != nil {
		t.Fatal(err)
	}
	turn, err := service.Get(ctx, scope, session.ID, first.Turn.ID)
	if err != nil || turn.State != domain.TurnAwaitingInput {
		t.Fatalf("首次运行状态=%+v, err=%v", turn, err)
	}
	var pending []map[string]any
	if err = json.Unmarshal(turn.PendingQuestions, &pending); err != nil || len(pending) != 1 {
		t.Fatalf("待回答问题=%s, err=%v", turn.PendingQuestions, err)
	}
	interruptID, _ := pending[0]["id"].(string)
	resumed, err := service.Resume(ctx, scope, session.ID, first.Turn.ID, ResumeInput{Revision: turn.Revision, IdempotencyKey: "answer-1", Answers: []AnswerInput{{InterruptID: interruptID, Text: "仓库级缺陷"}}})
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Turn.State != domain.TurnQueued {
		t.Fatalf("回答后未重新排队: %+v", resumed.Turn)
	}
	if _, err = service.RunQueued(ctx); err != nil {
		t.Fatal(err)
	}
	turn, err = service.Get(ctx, scope, session.ID, first.Turn.ID)
	if err != nil || turn.State != domain.TurnCompleted {
		t.Fatalf("恢复后状态=%+v, err=%v", turn, err)
	}
	notes, err := service.Notes(ctx, scope, 20)
	if err != nil || len(notes) != 1 {
		t.Fatalf("恢复后笔记=%v, err=%v", notes, err)
	}
}

type immediateAnswerRuntime struct {
	domain.RuntimeRepository
	answer func(context.Context, domain.Scope, string) error
}

func (r *immediateAnswerRuntime) SetPendingQuestions(ctx context.Context, scope domain.Scope, turnID string, questions []byte) error {
	if err := r.RuntimeRepository.SetPendingQuestions(ctx, scope, turnID, questions); err != nil {
		return err
	}
	return r.answer(ctx, scope, turnID)
}

// 用户在问题持久化后立即回答，执行器不得再次写等待状态覆盖已排队的恢复。
func TestImmediateInterruptAnswerRemainsQueued(t *testing.T) {
	ctx := t.Context()
	s, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p, err := s.CreateProject(ctx, "alice", "即时回答")
	if err != nil {
		t.Fatal(err)
	}
	scope := domain.Scope{UserID: "alice", ProjectID: p.ID}
	session, err := s.CreateSession(ctx, scope, "即时回答")
	if err != nil {
		t.Fatal(err)
	}
	runtime := &immediateAnswerRuntime{RuntimeRepository: s}
	service := NewTurnService(runtime, s, s)
	runtime.answer = func(ctx context.Context, scope domain.Scope, turnID string) error {
		turn, e := s.GetTurn(ctx, scope, session.ID, turnID)
		if e != nil {
			return e
		}
		var pending []struct {
			ID string `json:"id"`
		}
		if e = json.Unmarshal(turn.PendingQuestions, &pending); e != nil || len(pending) != 1 {
			t.Fatalf("问题未持久化: %s %v", turn.PendingQuestions, e)
		}
		_, e = service.Resume(ctx, scope, session.ID, turnID, ResumeInput{Revision: turn.Revision, IdempotencyKey: "instant", Answers: []AnswerInput{{InterruptID: pending[0].ID, Text: "仓库级缺陷"}}})
		return e
	}
	created, err := service.Send(ctx, scope, session.ID, SendInput{Text: "帮我整理下一步关注点并保存为笔记", IdempotencyKey: "instant-start"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.RunQueued(ctx); err != nil {
		t.Fatal(err)
	}
	turn, err := s.GetTurn(ctx, scope, session.ID, created.Turn.ID)
	if err != nil || turn.State != domain.TurnQueued {
		t.Fatalf("即时回答后恢复排队状态被覆盖: state=%s revision=%d err=%v", turn.State, turn.Revision, err)
	}
	if _, err = service.RunQueued(ctx); err != nil {
		t.Fatal(err)
	}
	turn, err = s.GetTurn(ctx, scope, session.ID, turn.ID)
	if err != nil || turn.State != domain.TurnCompleted {
		t.Fatalf("即时回答后未完成恢复: %+v %v", turn, err)
	}
}

func TestTurnServicePersistsRuntimeEvents(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	project, err := store.CreateProject(ctx, "local-user", "运行观测")
	if err != nil {
		t.Fatal(err)
	}
	scope := domain.Scope{UserID: "local-user", ProjectID: project.ID}
	session, err := store.CreateSession(ctx, scope, "问答")
	if err != nil {
		t.Fatal(err)
	}
	service := NewTurnService(store, store, store)
	_, err = service.Send(ctx, scope, session.ID, SendInput{Text: "请回答项目里已有笔记说明了什么", IdempotencyKey: "runtime-events-1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.RunQueued(ctx); err != nil {
		t.Fatal(err)
	}
	events, err := service.Events(ctx, scope, session.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	kinds := make(map[string]bool)
	for _, event := range events {
		if strings.HasPrefix(event.Kind, "runtime.") {
			kinds[event.Kind] = true
		}
	}
	for _, kind := range []string{"runtime.run", "runtime.node", "runtime.llm"} {
		if !kinds[kind] {
			t.Fatalf("缺少运行事件 %q，事件=%+v", kind, events)
		}
	}
}

func TestTurnServicePersistsPublicResearchProgressBeforeCompletion(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	project, err := store.CreateProject(ctx, "local-user", "公开进度")
	if err != nil {
		t.Fatal(err)
	}
	scope := domain.Scope{UserID: "local-user", ProjectID: project.ID}
	session, err := store.CreateSession(ctx, scope, "会话")
	if err != nil {
		t.Fatal(err)
	}
	service := NewTurnService(store, store, store)
	sent, err := service.Send(ctx, scope, session.ID, SendInput{Text: "请回答项目里已有笔记说明了什么", IdempotencyKey: "progress-events-1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.RunQueued(ctx); err != nil {
		t.Fatal(err)
	}
	events, err := service.Events(ctx, scope, session.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	progress, assistantDelta, completed := 0, 0, -1
	for index, event := range events {
		if event.Kind == "research.progress" {
			progress++
		}
		if event.Kind == "turn.completed" {
			completed = index
		}
		if event.Kind == "assistant.delta" {
			assistantDelta++
		}
		if event.Kind == "assistant.raw" {
			t.Fatal("不应持久化内部模型原始消息")
		}
	}
	if progress == 0 || assistantDelta == 0 || completed < 0 {
		t.Fatalf("事件缺少公开进度、答复增量或完成事件: progress=%d assistantDelta=%d completed=%d events=%+v", progress, assistantDelta, completed, events)
	}
	for index, event := range events {
		if event.Kind == "research.progress" && index > completed {
			t.Fatalf("完成后不应追加本轮进度: index=%d completed=%d", index, completed)
		}
	}
	_ = sent
}

func TestTurnServicePersistsWorkspaceToolEvents(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	project, err := store.CreateProject(ctx, "local-user", "工作区工具")
	if err != nil {
		t.Fatal(err)
	}
	scope := domain.Scope{UserID: "local-user", ProjectID: project.ID}
	session, err := store.CreateSession(ctx, scope, "代码检查")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	writeTestWorkspaceFile(t, root, "README.md", "service workspace\n")
	model := llm.NewHandlerModel(func(_ context.Context, request llm.Request, _ llm.Options) ([]schema.Message, error) {
		if strings.Contains(request.Messages[len(request.Messages)-1].JoinedText(""), "严格 JSON") {
			return []schema.Message{schema.AssistantMessage(`{"route":"workspace","scope":"","save":false}`)}, nil
		}
		for _, message := range request.Messages {
			if message.Role == schema.RoleTool {
				return []schema.Message{schema.AssistantMessage("已读取工作区文件。")}, nil
			}
		}
		return []schema.Message{schema.NewMessage(schema.RoleAssistant, schema.NewFunctionToolCallBlock("read-service", "read_file", `{"path":"README.md"}`))}, nil
	})
	service := NewTurnService(store, store, store, model)
	service.SetWorkspacePolicy(WorkspacePolicy{Root: root, Shell: "pwsh"})
	sent, err := service.Send(ctx, scope, session.ID, SendInput{Text: "请读取 README.md", IdempotencyKey: "workspace-event-1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.RunQueued(ctx); err != nil {
		t.Fatal(err)
	}
	events, err := service.Events(ctx, scope, session.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events {
		if event.Kind == "workspace.tool" && strings.Contains(string(event.PayloadJSON), "read_file") {
			found = true
		}
	}
	if !found {
		t.Fatalf("未找到工作区工具事件，events=%+v", events)
	}
	_ = sent
}

func TestTurnServiceAbandonsUnfinishedCheckpointBeforeNewExecution(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	project, err := store.CreateProject(ctx, "local-user", "残留运行恢复")
	if err != nil {
		t.Fatal(err)
	}
	scope := domain.Scope{UserID: "local-user", ProjectID: project.ID}
	session, err := store.CreateSession(ctx, scope, "会话")
	if err != nil {
		t.Fatal(err)
	}
	service := NewTurnService(store, store, store)
	sent, err := service.Send(ctx, scope, session.ID, SendInput{Text: "请回答项目里已有笔记说明了什么", IdempotencyKey: "stale-checkpoint-1"})
	if err != nil {
		t.Fatal(err)
	}
	conversationID := "autoresearch:" + sent.Turn.ID
	if _, err = service.checkpoints.Save(ctx, state.Checkpoint{
		ConversationID: conversationID,
		RunID:          "stale-run",
		Status:         state.StatusInterrupted,
		SchemaVersion:  state.CurrentCheckpointSchemaVersion,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = service.RunQueued(ctx); err != nil {
		t.Fatalf("存在残留 checkpoint 时新执行失败: %v", err)
	}
	finished, err := service.Get(ctx, scope, session.ID, sent.Turn.ID)
	if err != nil || finished.State != domain.TurnCompleted {
		t.Fatalf("残留 checkpoint 未恢复完成: %+v, err=%v", finished, err)
	}
	checkpoint, found, err := service.checkpoints.Load(ctx, conversationID)
	if err != nil || !found || checkpoint.Unfinished() {
		t.Fatalf("残留 checkpoint 未被放弃: %+v, found=%v, err=%v", checkpoint, found, err)
	}
}

func TestTurnServiceResumesFromSQLiteCheckpointAfterStoreReopen(t *testing.T) {
	for _, oldContext := range []bool{false, true} {
		name := "single"
		if oldContext {
			name = "legacy"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			dir := t.TempDir()
			store, err := sqlite.Open(ctx, dir)
			if err != nil {
				t.Fatal(err)
			}
			project, err := store.CreateProject(ctx, "local-user", "重启恢复")
			if err != nil {
				t.Fatal(err)
			}
			scope := domain.Scope{UserID: "local-user", ProjectID: project.ID}
			session, err := store.CreateSession(ctx, scope, "会话")
			if err != nil {
				t.Fatal(err)
			}
			service := NewTurnService(store, store, store)
			service.SetCheckpointFactory(func(scope domain.Scope, sessionID, turnID string) state.Store {
				return sqlite.NewCheckpointStore(store, scope, sessionID, turnID)
			})
			var sent SendResult
			if oldContext {
				user := schema.UserMessage("整理下一步关注点并保存笔记")
				encoded, marshalErr := json.Marshal(turnContext{Project: project, UserMessage: user})
				if marshalErr != nil {
					t.Fatal(marshalErr)
				}
				admitted, admitErr := store.AdmitTurn(ctx, scope, session.ID, domain.TurnDraft{InputText: user.JoinedText(""), IdempotencyKey: "restart-1", DefinitionVersion: researchDefinitionVersion, ContextJSON: encoded})
				err, sent.Turn = admitErr, admitted.Turn
			} else {
				sent, err = service.Send(ctx, scope, session.ID, SendInput{Text: "整理下一步关注点并保存笔记", IdempotencyKey: "restart-1"})
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = service.RunQueued(ctx); err != nil {
				t.Fatal(err)
			}
			paused, err := service.Get(ctx, scope, session.ID, sent.Turn.ID)
			if err != nil || paused.State != domain.TurnAwaitingInput {
				t.Fatalf("暂停状态=%+v err=%v", paused, err)
			}
			var questions []map[string]any
			if err = json.Unmarshal(paused.PendingQuestions, &questions); err != nil {
				t.Fatal(err)
			}
			interruptID, _ := questions[0]["id"].(string)
			if err = store.Close(); err != nil {
				t.Fatal(err)
			}
			store, err = sqlite.Open(ctx, dir)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			restarted := NewTurnService(store, store, store)
			restarted.SetCheckpointFactory(func(scope domain.Scope, sessionID, turnID string) state.Store {
				return sqlite.NewCheckpointStore(store, scope, sessionID, turnID)
			})
			paused, err = restarted.Get(ctx, scope, session.ID, sent.Turn.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = restarted.Resume(ctx, scope, session.ID, sent.Turn.ID, ResumeInput{Revision: paused.Revision, IdempotencyKey: "restart-answer", Answers: []AnswerInput{{InterruptID: interruptID, Text: "仓库级缺陷"}}}); err != nil {
				t.Fatal(err)
			}
			if _, err = restarted.RunQueued(ctx); err != nil {
				t.Fatal(err)
			}
			finished, err := restarted.Get(ctx, scope, session.ID, sent.Turn.ID)
			if err != nil || finished.State != domain.TurnCompleted {
				t.Fatalf("重启后状态=%+v err=%v", finished, err)
			}
			notes, err := restarted.Notes(ctx, scope, 20)
			if err != nil || len(notes) != 1 {
				t.Fatalf("重启后笔记=%v err=%v", notes, err)
			}
		})
	}
}

func TestTurnServiceKeepsConfirmedAnswersInCurrentAndNextTurn(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	project, err := store.CreateProject(ctx, "local-user", "检索研究")
	if err != nil {
		t.Fatal(err)
	}
	scope := domain.Scope{UserID: "local-user", ProjectID: project.ID}
	session, err := store.CreateSession(ctx, scope, "计算预算")
	if err != nil {
		t.Fatal(err)
	}
	followUp, sawHistory := false, false
	model := llm.NewHandlerModel(func(_ context.Context, request llm.Request, _ llm.Options) ([]schema.Message, error) {
		if followUp {
			for _, message := range request.Messages {
				sawHistory = sawHistory || strings.Contains(message.JoinedText(""), "用户已确认：可使用什么资源？：仅 CPU")
			}
			return []schema.Message{schema.AssistantMessage("继续制定小规模基线。")}, nil
		}
		last := request.Messages[len(request.Messages)-1]
		if last.Role == schema.RoleTool {
			if interactionResultText(last) != "仅 CPU" {
				t.Error("同一个 Agent 必须收到用户的真实工具回答")
			}
			return []schema.Message{schema.AssistantMessage("先采用小规模方案。")}, nil
		}
		return []schema.Message{schema.NewMessage(schema.RoleAssistant, schema.NewFunctionToolCallBlock("ask-service-budget", "ask_user", `{"question":"可使用什么资源？"}`))}, nil
	})
	service := NewTurnService(store, store, store, model)
	sent, err := service.Send(ctx, scope, session.ID, SendInput{Text: "给我一份检索实验建议", IdempotencyKey: "confirmed-first"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.RunQueued(ctx); err != nil {
		t.Fatal(err)
	}
	paused, err := service.Get(ctx, scope, session.ID, sent.Turn.ID)
	if err != nil || paused.State != domain.TurnAwaitingInput {
		t.Fatalf("未等待用户回答: turn=%v err=%v", paused, err)
	}
	var pending []struct {
		ID string `json:"id"`
	}
	if err = json.Unmarshal(paused.PendingQuestions, &pending); err != nil || len(pending) != 1 {
		t.Fatalf("pending=%s err=%v", paused.PendingQuestions, err)
	}
	if _, err = service.Resume(ctx, scope, session.ID, sent.Turn.ID, ResumeInput{Revision: paused.Revision, IdempotencyKey: "confirmed-answer", Answers: []AnswerInput{{InterruptID: pending[0].ID, Text: "仅 CPU"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err = service.RunQueued(ctx); err != nil {
		t.Fatal(err)
	}
	followUp = true
	if _, err = service.Send(ctx, scope, session.ID, SendInput{Text: "根据刚才确认的条件继续", IdempotencyKey: "confirmed-next"}); err != nil {
		t.Fatal(err)
	}
	if _, err = service.RunQueued(ctx); err != nil {
		t.Fatal(err)
	}
	if !sawHistory {
		t.Fatal("经过 SQLite 消息持久化后，下一轮丢失了用户确认答案")
	}
}

func TestTurnServicePinsAgentModeAcrossQueueAndResume(t *testing.T) {
	for _, mode := range []string{"single", "multi"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			store, err := sqlite.Open(ctx, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			project, err := store.CreateProject(ctx, "local-user", "固定任务模式")
			if err != nil {
				t.Fatal(err)
			}
			scope := domain.Scope{UserID: "local-user", ProjectID: project.ID}
			session, err := store.CreateSession(ctx, scope, "模式恢复")
			if err != nil {
				t.Fatal(err)
			}
			model := llm.NewHandlerModel(func(_ context.Context, req llm.Request, _ llm.Options) ([]schema.Message, error) {
				hasDelegate := false
				for _, fn := range req.Functions {
					hasDelegate = hasDelegate || fn.Name == "run_subagent"
				}
				if hasDelegate != (mode == "multi") {
					t.Errorf("本轮模式随进程配置变化：mode=%s delegate=%v", mode, hasDelegate)
				}
				if req.Messages[len(req.Messages)-1].Role == schema.RoleTool {
					return []schema.Message{schema.AssistantMessage("按已确认条件完成。")}, nil
				}
				return []schema.Message{schema.NewMessage(schema.RoleAssistant, schema.NewFunctionToolCallBlock("pin-ask", "ask_user", `{"question":"使用什么资源？"}`))}, nil
			})
			service := NewTurnService(store, store, store, model)
			if err = service.SetAgentMode(mode); err != nil {
				t.Fatal(err)
			}
			service.SetCheckpointFactory(func(s domain.Scope, sessionID, turnID string) state.Store {
				return sqlite.NewCheckpointStore(store, s, sessionID, turnID)
			})
			sent, err := service.Send(ctx, scope, session.ID, SendInput{Text: "设计实验", IdempotencyKey: "mode-pin"})
			if err != nil {
				t.Fatal(err)
			}
			opposite := "multi"
			if mode == "multi" {
				opposite = "single"
			}
			if err = service.SetAgentMode(opposite); err != nil {
				t.Fatal(err)
			}
			if _, err = service.RunQueued(ctx); err != nil {
				t.Fatal(err)
			}
			paused, err := service.Get(ctx, scope, session.ID, sent.Turn.ID)
			var pending []struct {
				ID string `json:"id"`
			}
			if err != nil || json.Unmarshal(paused.PendingQuestions, &pending) != nil || len(pending) != 1 {
				t.Fatalf("未获得中断问题：state=%s err=%v", paused.State, err)
			}
			restarted := NewTurnService(store, store, store, model)
			if err = restarted.SetAgentMode(opposite); err != nil {
				t.Fatal(err)
			}
			restarted.SetCheckpointFactory(func(s domain.Scope, sessionID, turnID string) state.Store {
				return sqlite.NewCheckpointStore(store, s, sessionID, turnID)
			})
			if _, err = restarted.Resume(ctx, scope, session.ID, sent.Turn.ID, ResumeInput{Revision: paused.Revision, IdempotencyKey: "mode-answer", Answers: []AnswerInput{{InterruptID: pending[0].ID, Text: "仅 CPU"}}}); err != nil {
				t.Fatal(err)
			}
			if _, err = restarted.RunQueued(ctx); err != nil {
				t.Fatal(err)
			}
			finished, err := restarted.Get(ctx, scope, session.ID, sent.Turn.ID)
			if err != nil || finished.State != domain.TurnCompleted {
				t.Fatalf("重建服务后没有按原模式恢复：state=%s err=%v", finished.State, err)
			}
		})
	}
}

func TestTurnServiceStreamsAgentTextWithoutInternalBlocks(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	project, err := store.CreateProject(ctx, "local-user", "流式输出")
	if err != nil {
		t.Fatal(err)
	}
	scope := domain.Scope{UserID: "local-user", ProjectID: project.ID}
	session, err := store.CreateSession(ctx, scope, "正文")
	if err != nil {
		t.Fatal(err)
	}
	model := llm.NewHandlerModel(func(ctx context.Context, _ llm.Request, _ llm.Options) ([]schema.Message, error) {
		writer, err := stream.StartMessage(ctx, schema.RoleAssistant)
		if err != nil {
			return nil, err
		}
		if _, err = writer.AddBlock(schema.ContentBlock{Type: schema.ContentBlockTypeReasoning, Reasoning: &schema.Reasoning{Text: "内部分析不可公开"}}); err != nil {
			return nil, err
		}
		for _, text := range []string{"第一段。", "第二段。"} {
			if err = writer.AppendText(text); err != nil {
				return nil, err
			}
		}
		message, err := writer.End()
		return []schema.Message{message}, err
	})
	service := NewTurnService(store, store, store, model)
	if _, err = service.Send(ctx, scope, session.ID, SendInput{Text: "解释一下", IdempotencyKey: "stream-agent"}); err != nil {
		t.Fatal(err)
	}
	if _, err = service.RunQueued(ctx); err != nil {
		t.Fatal(err)
	}
	events, err := service.Events(ctx, scope, session.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var chunks []string
	for _, event := range events {
		if event.Kind != "assistant.delta" {
			continue
		}
		if strings.Contains(string(event.PayloadJSON), "内部分析") {
			t.Fatal("内部推理被公开")
		}
		var update stream.MessageUpdate
		if err = json.Unmarshal(event.PayloadJSON, &update); err != nil {
			t.Fatal(err)
		}
		if update.Delta != nil && update.Delta.Op == schema.BlockAppend {
			chunks = append(chunks, update.Delta.Text)
		}
	}
	if strings.Join(chunks, "") != "第一段。第二段。" || len(chunks) < 2 {
		t.Fatalf("正文未逐段推送：%v", chunks)
	}
}

func TestTurnServiceRejectsOldDefinitionBeforeModelOrCheckpoint(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	project, err := store.CreateProject(ctx, "local-user", "旧版任务")
	if err != nil {
		t.Fatal(err)
	}
	scope := domain.Scope{UserID: "local-user", ProjectID: project.ID}
	session, err := store.CreateSession(ctx, scope, "旧版会话")
	if err != nil {
		t.Fatal(err)
	}
	admitted, err := store.AdmitTurn(ctx, scope, session.ID, domain.TurnDraft{InputText: "旧任务", IdempotencyKey: "old-definition", DefinitionVersion: "autoresearch-phase2-graph-v1", ContextJSON: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	modelCalls := 0
	service := NewTurnService(store, store, store, llm.NewHandlerModel(func(context.Context, llm.Request, llm.Options) ([]schema.Message, error) {
		modelCalls++
		return nil, nil
	}))
	if _, err = service.RunQueued(ctx); err == nil || !strings.Contains(err.Error(), "recovery_required") {
		t.Fatalf("旧版轮次应明确拒绝自动套用新图: %v", err)
	}
	turn, err := service.Get(ctx, scope, session.ID, admitted.Turn.ID)
	if err != nil || turn.State != domain.TurnFailed || turn.ErrorCode != "recovery_required" || modelCalls != 0 {
		t.Fatalf("旧版本检查没有在执行前生效: turn=%v modelCalls=%d err=%v", turn, modelCalls, err)
	}
	if _, found, err := service.checkpoints.Load(ctx, "autoresearch:"+turn.ID); err != nil || found {
		t.Fatalf("旧版运行不应读写新检查点: found=%v err=%v", found, err)
	}
}
