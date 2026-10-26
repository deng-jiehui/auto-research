package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/deng-jiehui/auto-research/internal/domain"
	"github.com/deng-jiehui/auto-research/internal/infra/mcp"
	"github.com/deng-jiehui/auto-research/internal/infra/sqlite"
	"github.com/goharness/goharness/pkg/llm"
	"github.com/goharness/goharness/pkg/schema"
	"github.com/goharness/goharness/pkg/state"
	"github.com/goharness/goharness/pkg/tool"
)

func TestResearchPlanReviewKeepsTypedAnswerAcrossRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := sqlite.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	p, _ := store.CreateProject(ctx, "alice", "计划审核")
	scope := domain.Scope{UserID: "alice", ProjectID: p.ID}
	session, _ := store.CreateSession(ctx, scope, "调研")
	plan := domain.ResearchPlan{Summary: "比较基线", Queries: []string{"forecasting baseline"}}
	var searched bool
	model := llm.NewHandlerModel(func(_ context.Context, req llm.Request, _ llm.Options) ([]schema.Message, error) {
		last := req.Messages[len(req.Messages)-1]
		text := last.JoinedText("")
		if last.Role == schema.RoleTool {
			for _, result := range last.FunctionToolResults() {
				text = schema.NewMessage(schema.RoleTool, result.ContentBlocks...).JoinedText("")
				switch result.Name {
				case "submit_research_result":
					var saved struct {
						Revision int64 `json:"revision"`
					}
					_ = json.Unmarshal([]byte(text), &saved)
					args, _ := json.Marshal(map[string]any{"kind": "research_plan", "expected_revision": saved.Revision, "question": "请确认计划"})
					return []schema.Message{schema.NewMessage(schema.RoleAssistant, schema.NewFunctionToolCallBlock("ask", "ask_user", string(args)))}, nil
				case "ask_user":
					if !strings.Contains(text, "用户调整的检索式") {
						t.Errorf("结构化回答丢失: %s", text)
					}
					return []schema.Message{schema.NewMessage(schema.RoleAssistant, schema.NewFunctionToolCallBlock("search", "search_papers", `{"text":"用户调整的检索式","limit":1}`))}, nil
				case "search_papers":
					searched = !result.IsError
					var saved struct {
						Revision   int64                      `json:"candidate_revision"`
						Candidates []domain.ResearchCandidate `json:"candidates"`
					}
					if err := json.Unmarshal([]byte(text), &saved); err != nil {
						t.Fatal(err)
					}
					recs := make([]domain.CandidateRecommendation, 0, len(saved.Candidates))
					for _, c := range saved.Candidates {
						recs = append(recs, domain.CandidateRecommendation{CandidateID: c.ID, Recommended: true, Reason: "相关预测基线"})
					}
					args, _ := json.Marshal(map[string]any{"kind": "research_candidates", "question": "请审核候选", "expected_revision": saved.Revision, "recommendations": recs})
					return []schema.Message{schema.NewMessage(schema.RoleAssistant, schema.NewFunctionToolCallBlock("candidates", "ask_user", string(args)))}, nil
				}
			}
		}
		args, _ := json.Marshal(map[string]any{"kind": "plan", "plan": plan, "expected_revision": 0})
		return []schema.Message{schema.NewMessage(schema.RoleAssistant, schema.NewFunctionToolCallBlock("plan", "submit_research_result", string(args)))}, nil
	})
	service := NewTurnService(store, store, store, model)
	service.SetResearchService(NewResearchService(store))
	service.SetAcademicTools(AcademicToolsDeps{Academic: NewAcademicService(mcp.NewFixtureSource())})
	service.SetCheckpointFactory(func(sc domain.Scope, sid, tid string) state.Store {
		return sqlite.NewCheckpointStore(store, sc, sid, tid)
	})
	result, err := service.Send(ctx, scope, session.ID, SendInput{Text: "比较时序基线并制定调研计划", IdempotencyKey: "start", Research: &domain.ResearchSpec{Question: "比较时序基线"}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.RunQueued(ctx)
	if err != nil {
		t.Fatal(err)
	}
	turn, _ := service.Get(ctx, scope, session.ID, result.Turn.ID)
	if turn.State != domain.TurnAwaitingInput {
		t.Fatalf("没有审核中断: %+v", turn)
	}
	var pending []struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(turn.PendingQuestions, &pending)
	run, _ := store.ResearchForTurn(ctx, scope, turn.ID)
	plan.Queries = []string{"用户调整的检索式"}
	_, err = service.Resume(ctx, scope, session.ID, turn.ID, ResumeInput{Revision: turn.Revision, IdempotencyKey: "review", Answers: []AnswerInput{{InterruptID: pending[0].ID, ResearchPlan: &domain.ResearchPlanReview{RunID: run.ID, PlanRevision: run.PlanRevision, PlanHash: run.PlanHash, Decision: "approve", Plan: plan}}}})
	if err != nil {
		t.Fatal(err)
	}
	store.Close()
	store, err = sqlite.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service = NewTurnService(store, store, store, model)
	service.SetResearchService(NewResearchService(store))
	service.SetAcademicTools(AcademicToolsDeps{Academic: NewAcademicService(mcp.NewFixtureSource())})
	service.SetCheckpointFactory(func(sc domain.Scope, sid, tid string) state.Store {
		return sqlite.NewCheckpointStore(store, sc, sid, tid)
	})
	_, err = service.RunQueued(ctx)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := store.ResearchForTurn(ctx, scope, turn.ID)
	if !searched || after.ApprovedPlanRevision != 2 || after.ModelCalls < 3 {
		t.Fatalf("未从原上下文恢复: searched=%v run=%+v", searched, after)
	}
	if _, err = store.GetResearch(ctx, domain.Scope{UserID: "bob", ProjectID: p.ID}, after.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("跨用户读取: %v", err)
	}
}

func TestResearchSearchRespectsFrozenScope(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	p, _ := store.CreateProject(ctx, "alice", "范围检查")
	scope := domain.Scope{UserID: "alice", ProjectID: p.ID}
	session, _ := store.CreateSession(ctx, scope, "调研")
	spec := domain.ResearchSpec{Question: "预测基线", Settings: domain.ResearchSettings{YearFrom: 2020, YearTo: 2024, Providers: []string{"arxiv"}, CandidateLimit: 3, AnalyzeLimit: 2}}
	admitted, err := store.AdmitTurn(ctx, scope, session.ID, domain.TurnDraft{InputText: "预测基线", IdempotencyKey: "start", DefinitionVersion: "v4", Research: &spec})
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.SaveResearchPlan(ctx, scope, admitted.Turn.ID, "plan", 0, domain.ResearchPlan{Summary: "预测基线", Queries: []string{"forecasting"}})
	if err != nil {
		t.Fatal(err)
	}
	pending, _ := json.Marshal([]any{map[string]any{"id": "review", "payload": map[string]any{"kind": "research_plan", "run_id": run.ID, "revision": 1, "hash": run.PlanHash}}})
	_ = store.SetPendingQuestions(ctx, scope, admitted.Turn.ID, pending)
	turn, _ := store.GetTurn(ctx, scope, session.ID, admitted.Turn.ID)
	payload, _ := json.Marshal([]AnswerInput{{InterruptID: "review", ResearchPlan: &domain.ResearchPlanReview{RunID: run.ID, PlanRevision: 1, PlanHash: run.PlanHash, Decision: "approve", Plan: run.Plan}}})
	_, err = store.ApplyAction(ctx, scope, session.ID, turn.ID, domain.ActionDraft{Kind: "resume", Revision: turn.Revision, IdempotencyKey: "approve", RequestHash: hash(payload), PayloadJSON: payload})
	if err != nil {
		t.Fatal(err)
	}
	var received AcademicQueryArgs
	original := tool.New[AcademicQueryArgs, []domain.Candidate]("search_papers", "检索", func(_ context.Context, args AcademicQueryArgs) ([]domain.Candidate, error) {
		received = args
		return nil, nil
	})
	wrapped := researchExecutionTool(original, ResearchGraphDeps{Scope: scope, TurnID: turn.ID, Research: NewResearchService(store)})
	result, err := wrapped.Invoke(ctx, tool.CallInput{RawArgs: `{"text":"forecasting","limit":30}`, IdempotencyKey: "search"})
	if err != nil || result.IsError {
		t.Fatalf("检索失败: %+v %v", result, err)
	}
	if received.YearFrom != 2020 || received.YearTo != 2024 || received.Limit != 3 {
		t.Fatalf("未应用冻结范围: %+v", received)
	}
}

func TestResearchReviewWrongVersionReturnsCorrectableToolError(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	p, _ := store.CreateProject(ctx, "alice", "工具修正")
	scope := domain.Scope{UserID: "alice", ProjectID: p.ID}
	session, _ := store.CreateSession(ctx, scope, "调研")
	var calls int
	model := llm.NewHandlerModel(func(_ context.Context, req llm.Request, _ llm.Options) ([]schema.Message, error) {
		calls++
		switch calls {
		case 1:
			return []schema.Message{schema.AssistantMessage("我已经完成调研了。")}, nil
		case 2:
			if !strings.Contains(req.Messages[len(req.Messages)-1].JoinedText(""), "应用验收反馈") {
				t.Error("缺少完成条件反馈")
			}
			return []schema.Message{schema.NewMessage(schema.RoleAssistant, schema.NewFunctionToolCallBlock("plan", "submit_research_result", `{"kind":"plan","expected_revision":0,"plan":{"summary":"先检索","queries":["forecasting"]}}`))}, nil
		case 3:
			return []schema.Message{schema.NewMessage(schema.RoleAssistant, schema.NewFunctionToolCallBlock("wrong", "ask_user", `{"kind":"research_plan","expected_revision":999,"question":"确认计划"}`))}, nil
		case 4:
			last := req.Messages[len(req.Messages)-1].FunctionToolResults()
			if len(last) != 1 || !last[0].IsError {
				t.Fatal("没有配对工具错误")
			}
			return []schema.Message{schema.NewMessage(schema.RoleAssistant, schema.NewFunctionToolCallBlock("correct", "ask_user", `{"kind":"research_plan","expected_revision":1,"question":"确认计划"}`))}, nil
		default:
			t.Fatal("不应跳过审核继续执行")
			return nil, nil
		}
	})
	service := NewTurnService(store, store, store, model)
	service.SetResearchService(NewResearchService(store))
	created, err := service.Send(ctx, scope, session.ID, SendInput{Text: "调研预测", IdempotencyKey: "start", Research: &domain.ResearchSpec{Question: "调研预测"}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.RunQueued(ctx)
	if err != nil {
		t.Fatal(err)
	}
	turn, _ := service.Get(ctx, scope, session.ID, created.Turn.ID)
	if turn.State != domain.TurnAwaitingInput || calls != 4 {
		t.Fatalf("未纠正工具调用: %+v calls=%d", turn, calls)
	}
}
