package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/deng-jiehui/auto-research/internal/domain"
	"github.com/deng-jiehui/auto-research/internal/infra/mcp"
	"github.com/deng-jiehui/auto-research/internal/infra/sqlite"
	"github.com/goharness/goharness/pkg/llm"
	"github.com/goharness/goharness/pkg/runnable"
	"github.com/goharness/goharness/pkg/schema"
)

func TestCancelledDetachedResearchForksWithoutRevivingOldRun(t *testing.T) {
	ctx := t.Context()
	s, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	service := analysisService(s, mcp.NewFixtureSource())
	p, _ := s.CreateProject(ctx, "alice", "取消后重新调研")
	scope := domain.Scope{UserID: "alice", ProjectID: p.ID}
	created, err := service.StartResearch(ctx, scope, StartResearchInput{ResearchSpec: domain.ResearchSpec{Question: "可重复研究"}, IdempotencyKey: "start"})
	if err != nil {
		t.Fatal(err)
	}
	cancelled, err := service.Cancel(ctx, scope, created.Turn.SessionID, created.Turn.ID, TurnActionInput{Revision: created.Turn.Revision, IdempotencyKey: "cancel"})
	if err != nil {
		t.Fatal(err)
	}
	input := TurnActionInput{Revision: cancelled.Turn.Revision, IdempotencyKey: "restart"}
	next, err := service.Retry(ctx, scope, cancelled.Turn.SessionID, cancelled.Turn.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	if next.Turn.ID == created.Turn.ID {
		t.Fatal("取消后重新调研复活了原 Run")
	}
	old, _ := s.GetResearch(ctx, scope, created.Run.ID)
	if old.Phase != "cancelled" || old.ExecutionState != domain.TurnCancelled {
		t.Fatalf("原取消记录被修改: %+v", old)
	}
	replayed, err := service.Retry(ctx, scope, cancelled.Turn.SessionID, cancelled.Turn.ID, input)
	if err != nil || !replayed.Replayed || replayed.Turn.ID != next.Turn.ID {
		t.Fatalf("取消后新任务重放不幂等: %+v %v", replayed, err)
	}
}

func TestDetachedResearchDelegationAndDurableLifecycle(t *testing.T) {
	ctx := t.Context()
	s, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	service := analysisService(s, mcp.NewFixtureSource())
	p, _ := s.CreateProject(ctx, "alice", "独立研究全流程")
	scope := domain.Scope{UserID: "alice", ProjectID: p.ID}
	session, _ := s.CreateSession(ctx, scope, "来源会话")
	chat, err := service.Send(ctx, scope, session.ID, SendInput{Text: "开始调研可重复基线", IdempotencyKey: "delegate"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.RunQueued(ctx); err != nil {
		t.Fatal(err)
	}
	chatTurn, _ := s.GetTurn(ctx, scope, session.ID, chat.Turn.ID)
	runs, _ := s.ListResearch(ctx, scope)
	if chatTurn.State != domain.TurnCompleted || len(runs) != 1 || !runs[0].Detached || runs[0].OriginTurnID != chatTurn.ID {
		t.Fatalf("会话未通过工具委派独立研究: chat=%+v runs=%+v", chatTurn, runs)
	}
	run := runs[0]
	for step := 0; step < 2; step++ {
		// 重建应用服务，继续使用持久检查点，不能依赖之前的内存上下文。
		service = analysisService(s, mcp.NewFixtureSource())
		if _, err = service.RunQueued(ctx); err != nil {
			t.Fatal(err)
		}
		turn, _ := s.GetTurn(ctx, scope, run.SessionID, run.TurnID)
		run, _ = s.GetResearch(ctx, scope, run.ID)
		var pending []struct {
			ID string `json:"id"`
		}
		if err = json.Unmarshal(turn.PendingQuestions, &pending); err != nil || len(pending) != 1 {
			t.Fatalf("审核中断缺失: %+v %v", turn, err)
		}
		answer := AnswerInput{InterruptID: pending[0].ID}
		if step == 0 {
			answer.ResearchPlan = &domain.ResearchPlanReview{RunID: run.ID, PlanRevision: run.PlanRevision, PlanHash: run.PlanHash, Decision: "approve", Plan: run.Plan}
		} else {
			if len(run.Candidates) == 0 {
				t.Fatalf("v7 未进入候选审核: %+v", run)
			}
			review := &domain.ResearchCandidateReview{RunID: run.ID, CandidateRevision: run.CandidateRevision, CandidateHash: run.CandidateHash, Decision: "approve", AnalyzeLimit: 1}
			for _, c := range run.Candidates {
				review.Choices = append(review.Choices, domain.CandidateDecision{CandidateID: c.ID, Selected: true, Reason: "可重复基线"})
			}
			answer.ResearchCandidates = review
		}
		if _, err = service.Resume(ctx, scope, run.SessionID, turn.ID, ResumeInput{Revision: turn.Revision, IdempotencyKey: time.Now().String(), Answers: []AnswerInput{answer}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = service.RunQueued(ctx); err != nil {
		t.Fatal(err)
	}
	run, _ = s.GetResearch(ctx, scope, run.ID)
	if run.ExecutionState != domain.TurnCompleted || run.Phase != "reported" || run.LatestReport == nil || len(run.WorkItems) != 1 {
		t.Fatalf("独立任务未完成逐篇分析: %+v", run)
	}
	cards, err := s.ResearchTaskSummaries(ctx, scope, session.ID)
	if err != nil || len(cards) != 1 || !strings.Contains(cards[0].Summary, "完成 1 篇") || !strings.Contains(cards[0].ResourcePath, run.ID) {
		t.Fatalf("会话结果投影不完整: %+v %v", cards, err)
	}
	followup, err := service.Send(ctx, scope, session.ID, SendInput{Text: "研究结果在哪里看", IdempotencyKey: "result-location"})
	if err != nil {
		t.Fatal(err)
	}
	contextText := string(followup.Turn.ContextJSON)
	if !strings.Contains(contextText, cards[0].ResourcePath) || strings.Contains(contextText, "work_items") || strings.Contains(contextText, "analysis_hash") {
		t.Fatalf("后续会话未仅接收摘要和位置: %s", contextText)
	}
	if _, err = service.RunQueued(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Send(ctx, scope, run.SessionID, SendInput{Text: "不能向研究容器聊天", IdempotencyKey: "hidden-chat"}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("内部研究容器接受了普通消息: %v", err)
	}
	if err = s.DeleteSession(ctx, scope, session.ID); err != nil {
		t.Fatal(err)
	}
	turn, _ := s.GetTurn(ctx, scope, run.SessionID, run.TurnID)
	if _, err = service.Retry(ctx, scope, run.SessionID, turn.ID, TurnActionInput{Revision: turn.Revision, IdempotencyKey: "fork-after-delete"}); err != nil {
		t.Fatalf("来源删除后不能重新研究: %v", err)
	}
}

// 框架脚本模型为消费固定响应而串行加锁；并发回归为每次请求创建独立测试实例。
type concurrentTaskTestModel struct {
	llm.ChatModel
	handler llm.Handler
}

func (m *concurrentTaskTestModel) Stream(ctx context.Context, req llm.Request, opts ...runnable.Option) (*runnable.StreamReader[llm.Response], error) {
	return llm.NewHandlerModel(m.handler).Stream(ctx, req, opts...)
}

func TestResearchWorkerDoesNotBlockOrdinaryChat(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	s, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	service := analysisService(s, mcp.NewFixtureSource())
	p, _ := s.CreateProject(ctx, "alice", "并发聊天")
	scope := domain.Scope{UserID: "alice", ProjectID: p.ID}
	session, _ := s.CreateSession(ctx, scope, "普通会话")
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	model := &concurrentTaskTestModel{handler: func(ctx context.Context, req llm.Request, _ llm.Options) ([]schema.Message, error) {
		for _, message := range req.Messages {
			if strings.Contains(message.JoinedText(""), "当前为正式调研任务") {
				once.Do(func() { close(entered) })
				select {
				case <-release:
					return nil, errors.New("测试研究结束")
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
		}
		return []schema.Message{schema.AssistantMessage("普通会话仍能响应")}, nil
	}}
	model.ChatModel = llm.NewScriptedModel()
	service.model = model
	created, err := service.StartResearch(ctx, scope, StartResearchInput{ResearchSpec: domain.ResearchSpec{Question: "研究基线"}, OriginSessionID: session.ID, IdempotencyKey: "worker-start"})
	if err != nil {
		t.Fatal(err)
	}
	service.Start(ctx)
	defer func() { close(release); cancel(); service.Stop() }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("研究 worker 未开始")
	}
	chat, err := service.Send(ctx, scope, session.ID, SendInput{Text: "你好", IdempotencyKey: "parallel-chat"})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		turn, e := s.GetTurn(ctx, scope, session.ID, chat.Turn.ID)
		if e != nil {
			t.Fatal(e)
		}
		if turn.State == domain.TurnCompleted {
			research, _ := s.GetTurn(ctx, scope, created.Run.SessionID, created.Turn.ID)
			if research.State != domain.TurnRunning {
				t.Fatalf("研究尚未在途: %s", research.State)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	turn, _ := s.GetTurn(ctx, scope, session.ID, chat.Turn.ID)
	t.Fatalf("普通会话被在途研究阻塞: state=%s code=%s error=%s", turn.State, turn.ErrorCode, turn.ErrorMessage)
}
