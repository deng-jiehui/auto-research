package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

func TestResearchSearchPersistsCandidatesAndReplays(t *testing.T) {
	ctx := context.Background()
	s, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p, _ := s.CreateProject(ctx, "alice", "候选审核")
	scope := domain.Scope{UserID: "alice", ProjectID: p.ID}
	session, _ := s.CreateSession(ctx, scope, "调研")
	admitted, err := s.AdmitTurn(ctx, scope, session.ID, domain.TurnDraft{InputText: "预测", IdempotencyKey: "start", DefinitionVersion: "autoresearch-agent-single-v5", Research: &domain.ResearchSpec{Question: "预测"}})
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.SaveResearchPlan(ctx, scope, admitted.Turn.ID, "plan", 0, domain.ResearchPlan{Summary: "预测", Queries: []string{"forecasting"}})
	if err != nil {
		t.Fatal(err)
	}
	pending, _ := json.Marshal([]any{map[string]any{"id": "p", "payload": map[string]any{"kind": "research_plan", "run_id": run.ID, "revision": 1, "hash": run.PlanHash}}})
	if err = s.SetPendingQuestions(ctx, scope, admitted.Turn.ID, pending); err != nil {
		t.Fatal(err)
	}
	turn, _ := s.GetTurn(ctx, scope, session.ID, admitted.Turn.ID)
	payload, _ := json.Marshal([]AnswerInput{{InterruptID: "p", ResearchPlan: &domain.ResearchPlanReview{RunID: run.ID, PlanRevision: 1, PlanHash: run.PlanHash, Decision: "approve", Plan: run.Plan}}})
	if _, err = s.ApplyAction(ctx, scope, session.ID, turn.ID, domain.ActionDraft{Kind: "resume", Revision: turn.Revision, IdempotencyKey: "approve", RequestHash: hash(payload), PayloadJSON: payload}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	original := tool.New[AcademicQueryArgs, []domain.Candidate]("search_papers", "检索", func(_ context.Context, args AcademicQueryArgs) ([]domain.Candidate, error) {
		calls++
		if args.Text == "bulk" {
			items := make([]domain.Candidate, 21)
			for i := range items {
				items[i] = domain.Candidate{SourceKey: fmt.Sprintf("arxiv:2401.%05d", i+1), Title: fmt.Sprintf("候选 %d", i+1), Provider: "arxiv"}
			}
			return items, nil
		}
		paper := domain.Candidate{SourceKey: "arxiv:2301.00001", Title: "Forecasting baseline", Provider: "arxiv"}
		return []domain.Candidate{paper, paper}, nil
	})
	wrapped := researchExecutionTool(original, ResearchGraphDeps{Scope: scope, TurnID: turn.ID, Research: NewResearchService(s), DefinitionVersion: "autoresearch-agent-single-v5"})
	input := tool.CallInput{RawArgs: `{"text":"forecasting","limit":5}`, IdempotencyKey: "search"}
	for i := 0; i < 2; i++ {
		result, err := wrapped.Invoke(ctx, input)
		if err != nil || result.IsError {
			t.Fatalf("检索失败: %+v %v", result, err)
		}
	}
	if calls != 1 {
		t.Fatalf("重放重复调用来源: %d", calls)
	}
	stored, _ := s.ResearchForTurn(ctx, scope, turn.ID)
	raw, _ := json.Marshal(stored)
	var decoded struct {
		Candidates []struct {
			ID string `json:"id"`
		} `json:"candidates"`
	}
	_ = json.Unmarshal(raw, &decoded)
	if len(decoded.Candidates) != 1 || decoded.Candidates[0].ID == "" {
		t.Fatalf("候选未去重持久化: %s", raw)
	}
	changed := input
	changed.RawArgs = `{"text":"different"}`
	result, err := wrapped.Invoke(ctx, changed)
	if err != nil || !result.IsError || calls != 1 {
		t.Fatalf("相同身份不同内容没有冲突: %+v %v", result, err)
	}
	if result, err = wrapped.Invoke(ctx, tool.CallInput{RawArgs: `{"text":"bulk","limit":30}`, IdempotencyKey: "bulk"}); err != nil || result.IsError {
		t.Fatalf("批量候选: %+v %v", result, err)
	}
	resources := &ProjectResources{Scope: scope, Projects: s, Research: NewResearchService(s)}
	page, err := resources.content(ctx, "research://runs/"+run.ID+"/candidates.json")
	if err != nil {
		t.Fatal(err)
	}
	var firstPage struct {
		Next  string                     `json:"next"`
		Items []domain.ResearchCandidate `json:"items"`
	}
	if err = json.Unmarshal([]byte(page), &firstPage); err != nil {
		t.Fatal(err)
	}
	if len(firstPage.Items) != 20 || firstPage.Next == "" {
		t.Fatalf("候选没有分页: %s", page)
	}
	if _, err = resources.Read(ctx, firstPage.Next, ReadFileArgs{}); err != nil {
		t.Fatalf("read_file 无法读取合法下一页: %v", err)
	}
	stored, _ = s.ResearchForTurn(ctx, scope, turn.ID)
	recs := make([]domain.CandidateRecommendation, 0, len(stored.Candidates))
	for _, c := range stored.Candidates {
		recs = append(recs, domain.CandidateRecommendation{CandidateID: c.ID, Recommended: true, Reason: "相关基线"})
	}
	if _, err = s.PrepareCandidateReview(ctx, scope, turn.ID, "publish", stored.CandidateRevision, recs); err != nil {
		t.Fatal(err)
	}
	if _, err = resources.content(ctx, firstPage.Next); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("旧候选页未绑定审核版本: %v", err)
	}
}

func TestV4ResearchReviewStillResumesWithOriginalContract(t *testing.T) {
	for _, mode := range []string{"single", "multi"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			s, err := sqlite.Open(ctx, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			p, _ := s.CreateProject(ctx, "alice", "旧任务")
			scope := domain.Scope{UserID: "alice", ProjectID: p.ID}
			session, _ := s.CreateSession(ctx, scope, "旧会话")
			contextJSON, _ := json.Marshal(turnContext{AgentMode: mode, Project: p, UserMessage: schema.UserMessage("旧计划")})
			admitted, err := s.AdmitTurn(ctx, scope, session.ID, domain.TurnDraft{InputText: "旧计划", IdempotencyKey: "old-start", DefinitionVersion: "autoresearch-agent-" + mode + "-v4", ContextJSON: contextJSON, Research: &domain.ResearchSpec{Question: "旧计划"}})
			if err != nil {
				t.Fatal(err)
			}
			deps := ResearchGraphDeps{Scope: scope, TurnID: admitted.Turn.ID, Research: NewResearchService(s), DefinitionVersion: "autoresearch-agent-" + mode + "-v4"}
			fixture := newSingleFixtureModel(deps)
			model := llm.NewHandlerModel(func(c context.Context, req llm.Request, _ llm.Options) ([]schema.Message, error) {
				for _, fn := range req.Functions {
					if fn.Name == "ask_user" {
						raw, _ := json.Marshal(fn)
						if strings.Contains(string(raw), "recommendations") {
							t.Error("旧图参数出现候选推荐字段")
						}
					}
				}
				out, err := fixture.Invoke(c, req)
				return out.Messages, err
			})
			service := NewTurnService(s, s, s, model)
			service.SetResearchService(NewResearchService(s))
			service.SetCheckpointFactory(func(sc domain.Scope, sid, tid string) state.Store { return sqlite.NewCheckpointStore(s, sc, sid, tid) })
			if _, err = service.RunQueued(ctx); err != nil {
				t.Fatal(err)
			}
			turn, _ := s.GetTurn(ctx, scope, session.ID, admitted.Turn.ID)
			var pending []struct {
				ID string `json:"id"`
			}
			_ = json.Unmarshal(turn.PendingQuestions, &pending)
			if len(pending) != 1 {
				t.Fatalf("旧中断丢失: %+v", turn)
			}
			run, _ := s.ResearchForTurn(ctx, scope, turn.ID)
			_, err = service.Resume(ctx, scope, session.ID, turn.ID, ResumeInput{Revision: turn.Revision, IdempotencyKey: "old-review", Answers: []AnswerInput{{InterruptID: pending[0].ID, ResearchPlan: &domain.ResearchPlanReview{RunID: run.ID, PlanRevision: run.PlanRevision, PlanHash: run.PlanHash, Decision: "approve", Plan: run.Plan}}}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = service.RunQueued(ctx); err != nil {
				t.Fatal(err)
			}
			after, _ := s.GetTurn(ctx, scope, session.ID, turn.ID)
			if after.State != domain.TurnCompleted {
				t.Fatalf("新程序无法恢复旧图: %+v", after)
			}
		})
	}
}

func TestCandidateReviewKeepsTypedAnswerAndScopeAcrossRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := sqlite.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	p, _ := s.CreateProject(ctx, "alice", "审核恢复")
	scope := domain.Scope{UserID: "alice", ProjectID: p.ID}
	session, _ := s.CreateSession(ctx, scope, "调研")
	var observed *domain.ResearchCandidateReview
	var turnID string
	makeService := func() *TurnService {
		academic := NewAcademicService(mcp.NewFixtureSource())
		papers := NewPaperService(s)
		documents := NewDocumentService(s, papers, academic)
		academicDeps := AcademicToolsDeps{Academic: academic, Papers: papers, Evidence: NewEvidenceService(s), Documents: documents}
		fixture := newSingleFixtureModel(ResearchGraphDeps{Scope: scope, TurnID: turnID, Research: NewResearchService(s), Academic: academicDeps, DefinitionVersion: "autoresearch-agent-single-v6"})
		model := llm.NewHandlerModel(func(c context.Context, req llm.Request, _ llm.Options) ([]schema.Message, error) {
			last := req.Messages[len(req.Messages)-1]
			for _, result := range last.FunctionToolResults() {
				if result.Name == "ask_user" && !result.IsError {
					var value AskUserResult
					if e := json.Unmarshal([]byte(schema.NewMessage(schema.RoleTool, result.ContentBlocks...).JoinedText("")), &value); e != nil {
						return nil, e
					}
					if value.ResearchCandidates != nil {
						observed = value.ResearchCandidates
					}
				}
			}
			out, err := fixture.Invoke(c, req)
			return out.Messages, err
		})
		service := NewTurnService(s, s, s, model)
		service.SetResearchService(NewResearchService(s))
		service.SetAcademicTools(academicDeps)
		service.SetCheckpointFactory(func(sc domain.Scope, sid, tid string) state.Store { return sqlite.NewCheckpointStore(s, sc, sid, tid) })
		return service
	}
	service := NewTurnService(s, s, s)
	service.SetResearchService(NewResearchService(s))
	created, err := service.Send(ctx, scope, session.ID, SendInput{Text: "调研预测基线", IdempotencyKey: "start", Research: &domain.ResearchSpec{Question: "调研预测基线"}})
	if err != nil {
		t.Fatal(err)
	}
	turnID = created.Turn.ID
	service = makeService()
	if _, err = service.RunQueued(ctx); err != nil {
		t.Fatal(err)
	}
	turn, _ := s.GetTurn(ctx, scope, session.ID, turnID)
	run, _ := s.ResearchForTurn(ctx, scope, turnID)
	var pending []struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(turn.PendingQuestions, &pending)
	if len(pending) != 1 {
		t.Fatalf("未进入计划审核: %+v", turn)
	}
	_, err = service.Resume(ctx, scope, session.ID, turnID, ResumeInput{Revision: turn.Revision, IdempotencyKey: "plan-review", Answers: []AnswerInput{{InterruptID: pending[0].ID, ResearchPlan: &domain.ResearchPlanReview{RunID: run.ID, PlanRevision: run.PlanRevision, PlanHash: run.PlanHash, Decision: "approve", Plan: run.Plan}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.RunQueued(ctx); err != nil {
		t.Fatal(err)
	}
	turn, _ = s.GetTurn(ctx, scope, session.ID, turnID)
	run, _ = s.ResearchForTurn(ctx, scope, turnID)
	_ = json.Unmarshal(turn.PendingQuestions, &pending)
	if turn.State != domain.TurnAwaitingInput || run.Phase != "candidate_review" || len(run.Candidates) != 1 {
		t.Fatalf("没有候选中断: %+v %+v", turn, run)
	}
	// 回答前和审核提交后各重启一次，不能依赖内存中的中断或回答缓存。
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = sqlite.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	service = makeService()
	restored, _ := s.GetTurn(ctx, scope, session.ID, turnID)
	if string(restored.PendingQuestions) != string(turn.PendingQuestions) {
		t.Fatal("重启后审核表单变化")
	}
	review := domain.ResearchCandidateReview{RunID: run.ID, CandidateRevision: run.CandidateRevision, CandidateHash: run.CandidateHash, Decision: "approve", AnalyzeLimit: 1, Choices: []domain.CandidateDecision{{CandidateID: run.Candidates[0].ID, Selected: true, Reason: "用户修改的纳入理由"}}, Feedback: "只研究单卡基线"}
	resume := ResumeInput{Revision: turn.Revision, IdempotencyKey: "candidate-review", Answers: []AnswerInput{{InterruptID: pending[0].ID, ResearchCandidates: &review}}}
	if _, err = service.Resume(ctx, scope, session.ID, turnID, resume); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = sqlite.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	service = makeService()
	if _, err = service.RunQueued(ctx); err != nil {
		t.Fatal(err)
	}
	if observed == nil || observed.Choices[0].Reason != review.Choices[0].Reason || observed.Choices[0].PaperID == "" || observed.AnalyzeLimit != 1 || observed.Feedback != review.Feedback {
		t.Fatalf("重启后结构化回答或论文映射丢失: %+v", observed)
	}
	after, _ := s.GetTurn(ctx, scope, session.ID, turnID)
	if after.State != domain.TurnCompleted {
		t.Fatalf("未恢复原 Agent: %+v", after)
	}
	deps := ResearchGraphDeps{Scope: scope, TurnID: turnID, Research: NewResearchService(s), DefinitionVersion: "autoresearch-agent-single-v5"}
	called := false
	original := tool.New[ReadPaperDocumentArgs, string]("read_paper", "读取", func(context.Context, ReadPaperDocumentArgs) (string, error) { called = true; return "ok", nil })
	wrapped := researchExecutionTool(original, deps)
	result, err := wrapped.Invoke(ctx, tool.CallInput{RawArgs: `{"paper_id":"excluded-paper","section":"fulltext"}`})
	if err != nil || !result.IsError || called {
		t.Fatalf("未纳入论文被读取: %+v %v", result, err)
	}
	raw, _ := json.Marshal(ReadPaperDocumentArgs{PaperID: observed.Choices[0].PaperID, Section: "abstract"})
	result, err = wrapped.Invoke(ctx, tool.CallInput{RawArgs: string(raw)})
	if err != nil || result.IsError || !called {
		t.Fatalf("已纳入论文不能读取: %+v %v", result, err)
	}
	foreign, _ := s.ImportPaper(ctx, scope, domain.Paper{SourceKey: "arxiv:2401.00002", Title: "项目中未纳入的论文"})
	evidence := NewEvidenceService(s)
	selectedDoc, err := evidence.SaveDocument(ctx, scope, domain.SourceDocument{PaperID: observed.Choices[0].PaperID, Text: "A baseline for selected research.", ContentKind: "abstract", Locator: "selected"})
	if err != nil {
		t.Fatal(err)
	}
	foreignDoc, err := evidence.SaveDocument(ctx, scope, domain.SourceDocument{PaperID: foreign.ID, Text: "A baseline from excluded research.", ContentKind: "abstract", Locator: "excluded"})
	if err != nil {
		t.Fatal(err)
	}
	documents := NewDocumentService(s, NewPaperService(s), nil)
	deps.Academic = AcademicToolsDeps{Scope: scope, Papers: NewPaperService(s), Evidence: evidence, Documents: documents}
	for _, original := range NewAcademicTools(deps.Academic) {
		wrapped := researchExecutionTool(original, deps)
		if original.Spec().Name == "search_project_evidence" {
			result, err := wrapped.Invoke(ctx, tool.CallInput{RawArgs: `{"query":"baseline","limit":1}`})
			if err != nil || result.IsError {
				t.Fatalf("纳入集检索失败: %+v %v", result, err)
			}
			var hits []domain.DocumentHit
			decodeToolData(t, result, &hits)
			if len(hits) != 1 || hits[0].DocumentID != selectedDoc.ID {
				t.Fatalf("检索被项目其他论文挤占或泄漏原文: %+v", hits)
			}
		}
		if original.Spec().Name == "save_evidence" {
			args, _ := json.Marshal(SaveEvidenceArgs{DocumentID: foreignDoc.ID, BlockID: "b1", Quote: "A baseline from excluded research."})
			result, err := wrapped.Invoke(ctx, tool.CallInput{RawArgs: string(args)})
			if err != nil || !result.IsError {
				t.Fatalf("未纳入论文被保存为证据: %+v %v", result, err)
			}
		}
	}
	resources := &ProjectResources{Scope: scope, Projects: s, Research: NewResearchService(s)}
	resources.Papers, resources.Documents = NewPaperService(s), documents
	deps.Resources = resources
	tools, _, err := newSingleAgentTools(deps, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range tools {
		if item.Spec().Name == "read_file" {
			args, _ := json.Marshal(ReadFileArgs{Path: "research://documents/" + foreignDoc.ID + "/blocks/b1.txt"})
			result, err := item.Invoke(ctx, tool.CallInput{RawArgs: string(args)})
			if err == nil && !result.IsError {
				t.Fatalf("文件工具绕过纳入集读取了其他论文: %+v", result)
			}
		}
	}
	page, err := resources.content(ctx, "research://runs/"+run.ID+"/candidates.json")
	if err != nil || !strings.Contains(page, run.Candidates[0].ID) {
		t.Fatalf("候选资料无法读取: %s %v", page, err)
	}
	resources.Scope.UserID = "bob"
	if _, err = resources.content(ctx, "research://runs/"+run.ID+"/candidates.json"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("候选资料跨用户泄漏: %v", err)
	}
}
