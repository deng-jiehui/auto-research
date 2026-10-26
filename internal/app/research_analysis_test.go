package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deng-jiehui/auto-research/internal/domain"
	"github.com/deng-jiehui/auto-research/internal/infra/mcp"
	"github.com/deng-jiehui/auto-research/internal/infra/sqlite"
	"github.com/goharness/goharness/pkg/graph"
	"github.com/goharness/goharness/pkg/runnable"
	"github.com/goharness/goharness/pkg/schema"
	"github.com/goharness/goharness/pkg/state"
	"github.com/goharness/goharness/pkg/tool"
)

func analysisService(s *sqlite.Store, source domain.PaperSource) *TurnService {
	academic := NewAcademicService(source)
	papers := NewPaperService(s)
	documents := NewDocumentService(s, papers, academic)
	service := NewTurnService(s, s, s)
	research := NewResearchService(s)
	service.SetResearchService(research)
	service.SetAcademicTools(AcademicToolsDeps{Academic: academic, Papers: papers, Evidence: NewEvidenceService(s), Documents: documents})
	service.SetResearchResources(nil, &ProjectResources{Projects: s, Research: research, Papers: papers, Documents: documents})
	service.SetCheckpointFactory(func(sc domain.Scope, sid, tid string) state.Store { return sqlite.NewCheckpointStore(s, sc, sid, tid) })
	return service
}

func approveAnalysisInputs(t *testing.T, service *TurnService, s *sqlite.Store, definitions ...string) (domain.Scope, domain.Turn, domain.ResearchRun) {
	t.Helper()
	ctx := t.Context()
	p, err := s.CreateProject(ctx, "alice", "逐篇分析")
	if err != nil {
		t.Fatal(err)
	}
	scope := domain.Scope{UserID: "alice", ProjectID: p.ID}
	session, err := s.CreateSession(ctx, scope, "研究")
	if err != nil {
		t.Fatal(err)
	}
	var created SendResult
	if len(definitions) > 0 {
		message := schema.UserMessage("研究可重复基线")
		encoded, _ := json.Marshal(turnContext{AgentMode: "single", Project: p, UserMessage: message})
		if strings.HasSuffix(definitions[0], "-v7") || strings.HasSuffix(definitions[0], "-v8") {
			messageJSON, _ := json.Marshal(message)
			admitted, e := s.CreateResearchTask(ctx, scope, domain.ResearchTaskDraft{Spec: domain.ResearchSpec{Question: "研究可重复基线"}, OperationID: "start", ContextJSON: encoded, MessageJSON: messageJSON, DefinitionVersion: definitions[0]})
			created = SendResult{Turn: admitted.Turn}
			err = e
			session.ID = admitted.Turn.SessionID
		} else {
			admitted, e := s.AdmitTurn(ctx, scope, session.ID, domain.TurnDraft{InputText: "研究可重复基线", IdempotencyKey: "start", DefinitionVersion: definitions[0], ContextJSON: encoded, Research: &domain.ResearchSpec{Question: "研究可重复基线"}})
			created = SendResult{Turn: admitted.Turn}
			err = e
		}
	} else {
		created, err = service.Send(ctx, scope, session.ID, SendInput{Text: "研究可重复基线", IdempotencyKey: "start", Research: &domain.ResearchSpec{Question: "研究可重复基线"}})
	}
	if err != nil {
		t.Fatal(err)
	}
	for step := 0; step < 2; step++ {
		if _, err = service.RunQueued(ctx); err != nil {
			t.Fatal(err)
		}
		turn, err := s.GetTurn(ctx, scope, session.ID, created.Turn.ID)
		if err != nil {
			t.Fatal(err)
		}
		run, err := s.ResearchForTurn(ctx, scope, turn.ID)
		if err != nil {
			t.Fatal(err)
		}
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
			review := &domain.ResearchCandidateReview{RunID: run.ID, CandidateRevision: run.CandidateRevision, CandidateHash: run.CandidateHash, Decision: "approve", AnalyzeLimit: len(run.Candidates)}
			for _, c := range run.Candidates {
				review.Choices = append(review.Choices, domain.CandidateDecision{CandidateID: c.ID, Selected: true, Reason: "纳入基线"})
			}
			answer.ResearchCandidates = review
		}
		if _, err = service.Resume(ctx, scope, session.ID, turn.ID, ResumeInput{Revision: turn.Revision, IdempotencyKey: time.Now().String(), Answers: []AnswerInput{answer}}); err != nil {
			t.Fatal(err)
		}
	}
	turn, _ := s.GetTurn(ctx, scope, session.ID, created.Turn.ID)
	run, _ := s.ResearchForTurn(ctx, scope, turn.ID)
	return scope, turn, run
}

func TestV5ResearchKeepsCandidateOnlyCompletion(t *testing.T) {
	s, err := sqlite.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	service := analysisService(s, mcp.NewFixtureSource())
	scope, _, run := approveAnalysisInputs(t, service, s, "autoresearch-agent-single-v5")
	if _, err = service.RunQueued(t.Context()); err != nil {
		t.Fatal(err)
	}
	after, _ := s.GetResearch(t.Context(), scope, run.ID)
	if after.Phase != "candidates_selected" || after.ExecutionState != domain.TurnCompleted || len(after.WorkItems) != 0 {
		t.Fatalf("旧图被强制进入新分析流程: %+v", after)
	}
}

func TestResearchAnalysisMultiKeepsSameResultContract(t *testing.T) {
	s, err := sqlite.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	service := analysisService(s, mcp.NewFixtureSource())
	if err = service.SetAgentMode("multi"); err != nil {
		t.Fatal(err)
	}
	scope, _, run := approveAnalysisInputs(t, service, s)
	if _, err = service.RunQueued(t.Context()); err != nil {
		t.Fatal(err)
	}
	after, _ := s.GetResearch(t.Context(), scope, run.ID)
	if after.ExecutionState != domain.TurnCompleted || len(after.WorkItems) != 1 || after.WorkItems[0].State != "completed" {
		t.Fatalf("multi 模式未使用相同成果约束: %+v", after)
	}
}

type partialPaperSource struct {
	domain.PaperSource
	unavailable  atomic.Bool
	firstReads   atomic.Int32
	partialSaved atomic.Bool
	store        *sqlite.Store
	scope        domain.Scope
	turnID       string
}

func (s *partialPaperSource) Search(ctx context.Context, q domain.PaperQuery) ([]domain.Candidate, error) {
	items, err := s.PaperSource.Search(ctx, q)
	if err != nil {
		return nil, err
	}
	return append(items, domain.Candidate{SourceKey: "arxiv:2401.00002", Title: "Second fixture", Provider: "arxiv", Abstract: "Second reproducible baseline"}), nil
}
func (s *partialPaperSource) Read(ctx context.Context, ref, section string) (domain.SourceDocument, error) {
	if ref == "arxiv:2401.00002" {
		run, err := s.store.ResearchForTurn(ctx, s.scope, s.turnID)
		if err != nil {
			return domain.SourceDocument{}, err
		}
		if len(run.WorkItems) > 0 && run.WorkItems[0].State == "completed" {
			s.partialSaved.Store(true)
		}
		if s.unavailable.Load() {
			return domain.SourceDocument{}, domain.ErrUnavailable
		}
		return s.PaperSource.Read(ctx, "arxiv:2401.00001", section)
	}
	if section == "fulltext" {
		s.firstReads.Add(1)
	}
	return s.PaperSource.Read(ctx, ref, section)
}
func TestResearchPartialFailureAndSingleRetryKeepsCompletedPaper(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	s, err := sqlite.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	source := &partialPaperSource{PaperSource: mcp.NewFixtureSource(), store: s}
	source.unavailable.Store(true)
	service := analysisService(s, source)
	scope, turn, run := approveAnalysisInputs(t, service, s)
	source.scope = scope
	source.turnID = turn.ID
	if _, err = service.RunQueued(ctx); err != nil {
		t.Fatal(err)
	}
	before, _ := s.GetResearch(ctx, scope, run.ID)
	if len(before.WorkItems) != 2 || before.WorkItems[0].State != "completed" || before.WorkItems[1].State != "failed" || !source.partialSaved.Load() {
		t.Fatalf("失败影响了其他篇/未逐篇保存: %+v", before)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = sqlite.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	source.store = s
	source.unavailable.Store(false)
	service = analysisService(s, source)
	current, _ := s.GetTurn(ctx, scope, turn.SessionID, turn.ID)
	if _, err = service.ControlResearch(ctx, scope, run.ID, "retry", ResearchControlInput{TurnActionInput: TurnActionInput{Revision: current.Revision, IdempotencyKey: "retry-second"}, PaperID: before.WorkItems[1].PaperID, WorkRevision: before.WorkItems[1].Revision}); err != nil {
		t.Fatal(err)
	}
	if _, err = service.RunQueued(ctx); err != nil {
		t.Fatal(err)
	}
	after, _ := s.GetResearch(ctx, scope, run.ID)
	if after.WorkItems[0].AnalysisHash != before.WorkItems[0].AnalysisHash || after.WorkItems[0].Revision != before.WorkItems[0].Revision || after.WorkItems[1].State != "completed" || after.WorkItems[1].Attempts != 2 || after.ModelCalls <= before.ModelCalls || source.firstReads.Load() != 1 {
		t.Fatalf("单篇重试重复已完成工作或清空预算: %+v", after)
	}
}

func TestResearchAnalysisCompletesWithEvidence(t *testing.T) {
	s, err := sqlite.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	service := analysisService(s, mcp.NewFixtureSource())
	scope, turn, run := approveAnalysisInputs(t, service, s)
	if _, err = service.RunQueued(t.Context()); err != nil {
		t.Fatal(err)
	}
	after, err := s.GetResearch(t.Context(), scope, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.ExecutionState != domain.TurnCompleted || after.Phase != "analyses_completed" || len(after.WorkItems) != 1 || after.WorkItems[0].State != "completed" {
		t.Fatalf("未完成逐篇分析: %+v", after)
	}
	w := after.WorkItems[0]
	if w.Analysis == nil || len(w.Analysis.Fields[0].EvidenceIDs) != 1 {
		t.Fatalf("未保存证据: %+v", w)
	}
	evidence, err := s.GetEvidence(t.Context(), scope, w.Analysis.Fields[0].EvidenceIDs[0])
	if err != nil || evidence.DocumentID != w.DocumentID {
		t.Fatalf("证据不匹配: %+v %v", evidence, err)
	}
	found := false
	afterID := int64(0)
	for {
		events, err := s.ListEvents(t.Context(), scope, turn.SessionID, afterID, 100)
		if err != nil {
			t.Fatal(err)
		}
		if len(events) == 0 {
			break
		}
		for _, event := range events {
			afterID = event.ID
			if event.Kind == "research.progress" {
				var p map[string]any
				_ = json.Unmarshal(event.PayloadJSON, &p)
				if p["stage"] == "paper_analysis" {
					found = true
				}
			}
		}
	}
	if !found {
		t.Fatal("未推送逐篇进度")
	}
}

type blockedPaperSource struct {
	domain.PaperSource
	started chan struct{}
	release chan struct{}
	calls   atomic.Int32
}

func (s *blockedPaperSource) Read(ctx context.Context, reference, section string) (domain.SourceDocument, error) {
	if section == "fulltext" {
		s.calls.Add(1)
		select {
		case s.started <- struct{}{}:
		default:
		}
		select {
		case <-s.release:
		case <-ctx.Done():
			return domain.SourceDocument{}, ctx.Err()
		}
	}
	return s.PaperSource.Read(ctx, reference, section)
}
func TestResearchPauseWaitsForToolBoundary(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	dir := t.TempDir()
	s, err := sqlite.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	source := &blockedPaperSource{PaperSource: mcp.NewFixtureSource(), started: make(chan struct{}, 1), release: make(chan struct{})}
	service := analysisService(s, source)
	scope, turn, run := approveAnalysisInputs(t, service, s)
	done := make(chan error, 1)
	go func() { _, e := service.RunQueued(ctx); done <- e }()
	select {
	case <-source.started:
	case <-ctx.Done():
		t.Fatal("阅读未启动")
	}
	running, _ := s.GetTurn(ctx, scope, turn.SessionID, turn.ID)
	if _, err = service.ControlResearch(ctx, scope, run.ID, "pause", ResearchControlInput{TurnActionInput: TurnActionInput{Revision: running.Revision, IdempotencyKey: "pause"}}); err != nil {
		t.Fatal(err)
	}
	before, _ := s.GetResearch(ctx, scope, run.ID)
	if !before.PauseRequested || before.ExecutionState != domain.TurnRunning {
		t.Fatalf("暂停提前打断在途工具: %+v", before)
	}
	close(source.release)
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("未在工具返回后暂停")
	}
	paused, _ := s.GetTurn(ctx, scope, turn.SessionID, turn.ID)
	if paused.State != domain.TurnAwaitingInput {
		t.Fatalf("没有暂停检查点: %+v", paused)
	}
	if _, err = service.ControlResearch(ctx, scope, run.ID, "continue", ResearchControlInput{TurnActionInput: TurnActionInput{Revision: paused.Revision, IdempotencyKey: "continue"}}); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = sqlite.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	service = analysisService(s, source)
	if _, err = service.RunQueued(ctx); err != nil {
		t.Fatal(err)
	}
	after, _ := s.GetResearch(ctx, scope, run.ID)
	if after.ExecutionState != domain.TurnCompleted || after.WorkItems[0].State != "completed" || source.calls.Load() != 1 || after.PauseRequested {
		t.Fatalf("重启恢复丢失/重复分析: %+v 来源调用=%d", after, source.calls.Load())
	}
}

func TestResearchCompletedRetryCreatesLinkedRun(t *testing.T) {
	s, err := sqlite.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	service := analysisService(s, mcp.NewFixtureSource())
	scope, turn, run := approveAnalysisInputs(t, service, s)
	if _, err = service.RunQueued(t.Context()); err != nil {
		t.Fatal(err)
	}
	completed, _ := s.GetTurn(t.Context(), scope, turn.SessionID, turn.ID)
	input := TurnActionInput{Revision: completed.Revision, IdempotencyKey: "new-research"}
	restarted, err := service.Retry(t.Context(), scope, turn.SessionID, turn.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	if restarted.Turn.ID == turn.ID {
		t.Fatal("已完成调研被原地清空")
	}
	next, err := s.ResearchForTurn(t.Context(), scope, restarted.Turn.ID)
	if err != nil {
		t.Fatal(err)
	}
	if next.PreviousRunID != run.ID || next.ModelCalls != 0 || next.SpecHash != run.SpecHash {
		t.Fatalf("新任务未保留来源和范围: %+v", next)
	}
	replay, err := service.Retry(t.Context(), scope, turn.SessionID, turn.ID, input)
	if err != nil || !replay.Replayed || replay.Turn.ID != restarted.Turn.ID {
		t.Fatalf("新任务重发不幂等: %+v %v", replay, err)
	}
	old, _ := s.GetResearch(t.Context(), scope, run.ID)
	if old.WorkItems[0].State != "completed" {
		t.Fatal("旧成果丢失")
	}
}

func TestResearchSegmentDeadlinePausesAtBoundary(t *testing.T) {
	s, err := sqlite.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	service := analysisService(s, mcp.NewFixtureSource())
	scope, turn, run := approveAnalysisInputs(t, service, s)
	g := graph.NewGraph[string, string]("research-segment")
	if err = graph.SetStateFactory(g, func(context.Context, string) (*ResearchState, error) { return &ResearchState{}, nil }); err != nil {
		t.Fatal(err)
	}
	codec, err := newResearchStateCodec()
	if err != nil {
		t.Fatal(err)
	}
	if err = g.SetStateCodec(codec); err != nil {
		t.Fatal(err)
	}
	deps := ResearchGraphDeps{Scope: scope, TurnID: turn.ID, DefinitionVersion: "autoresearch-agent-single-v6", Research: NewResearchService(s)}
	if err = g.AddNode("boundary", runnable.Func[string, string](func(ctx context.Context, in string, _ ...runnable.Option) (string, error) {
		return in, nil
	}), graph.WithPreHandler(func(ctx context.Context, _ *ResearchState) (string, error) {
		return "", researchAnalysisBoundary(ctx, deps)
	})); err != nil {
		t.Fatal(err)
	}
	if err = g.AddEdge(graph.Start, "boundary"); err != nil {
		t.Fatal(err)
	}
	if err = g.AddEdge("boundary", graph.End); err != nil {
		t.Fatal(err)
	}
	compiled, err := g.Compile()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(t.Context(), researchSegmentDeadlineKey{}, time.Now().Add(-time.Second))
	_, err = compiled.Invoke(ctx, "pause", graph.WithCheckpoint(state.NewMemoryStore(), "segment"))
	var interrupted *graph.InterruptedError
	if !errors.As(err, &interrupted) {
		t.Fatalf("执行段超时没有安全暂停: %v", err)
	}
	after, _ := s.GetResearch(t.Context(), scope, run.ID)
	if !after.PauseRequested || after.ModelCalls != run.ModelCalls {
		t.Fatalf("超时后仍新增工作: %+v", after)
	}
}

func TestResearchCancelStopsNewWorkAfterCurrentTool(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	s, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	source := &blockedPaperSource{PaperSource: mcp.NewFixtureSource(), started: make(chan struct{}, 1), release: make(chan struct{})}
	service := analysisService(s, source)
	scope, turn, run := approveAnalysisInputs(t, service, s)
	done := make(chan error, 1)
	go func() { _, e := service.RunQueued(ctx); done <- e }()
	select {
	case <-source.started:
	case <-ctx.Done():
		t.Fatal("阅读未启动")
	}
	current, _ := s.GetTurn(ctx, scope, turn.SessionID, turn.ID)
	before, _ := s.GetResearch(ctx, scope, run.ID)
	if _, err = service.ControlResearch(ctx, scope, run.ID, "cancel", ResearchControlInput{TurnActionInput: TurnActionInput{Revision: current.Revision, IdempotencyKey: "cancel"}}); err != nil {
		close(source.release)
		t.Fatal(err)
	}
	close(source.release)
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("取消未生效")
	}
	after, _ := s.GetResearch(ctx, scope, run.ID)
	if after.ExecutionState != domain.TurnCancelled || after.Phase != "cancelled" || after.ModelCalls != before.ModelCalls || after.WorkItems[0].Analysis != nil {
		t.Fatalf("取消后仍执行了新工作: %+v", after)
	}
}

func TestResearchAnalysisToolReplaySurvivesExhaustedBudget(t *testing.T) {
	s, err := sqlite.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	service := analysisService(s, mcp.NewFixtureSource())
	scope, turn, run := approveAnalysisInputs(t, service, s)
	doc, err := NewEvidenceService(s).SaveDocument(t.Context(), scope, domain.SourceDocument{PaperID: run.WorkItems[0].PaperID, Text: "摘要", ContentKind: "abstract", Locator: "test:abstract"})
	if err != nil {
		t.Fatal(err)
	}
	w, err := s.BeginResearchPaper(t.Context(), scope, turn.ID, doc.PaperID, doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	fields := []domain.AnalysisField{}
	for _, name := range []string{"method", "datasets", "metrics", "code", "reproduction"} {
		fields = append(fields, domain.AnalysisField{Name: name, Status: "unknown", Text: "摘要未知"})
	}
	raw, _ := json.Marshal(map[string]any{"kind": "paper_analysis", "expected_revision": w.Revision, "paper_analysis": domain.PaperAnalysisDraft{PaperID: w.PaperID, DocumentID: w.DocumentID, Status: "completed", Summary: "仅摘要", Fields: fields}})
	deps := ResearchGraphDeps{Scope: scope, TurnID: turn.ID, Research: NewResearchService(s), DefinitionVersion: "autoresearch-agent-single-v6"}
	wrapped := researchExecutionTool(analysisResultTool(deps), deps)
	input := tool.CallInput{RawArgs: string(raw), IdempotencyKey: "stable-analysis"}
	if result, e := wrapped.Invoke(t.Context(), input); e != nil || result.IsError {
		t.Fatalf("首次保存失败: %+v %v", result, e)
	}
	for {
		_, e := s.ReserveResearchCall(t.Context(), scope, turn.ID, "tool")
		if errors.Is(e, domain.ErrBudgetExhausted) {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
	}
	if result, e := wrapped.Invoke(t.Context(), input); e != nil || result.IsError {
		t.Fatalf("成功操作被新预算拒绝: %+v %v", result, e)
	}
	for _, invalid := range []string{
		strings.Replace(string(raw), "仅摘要", "替换已保存内容", 1),
		strings.TrimSuffix(string(raw), "}") + `,"unexpected":true}`,
	} {
		changed := input
		changed.RawArgs = invalid
		if result, e := wrapped.Invoke(t.Context(), changed); e == nil && !result.IsError {
			t.Fatalf("重放放过了身份冲突或未知参数: %s", invalid)
		}
	}
	after, _ := s.GetResearch(t.Context(), scope, run.ID)
	if after.ToolCalls != after.Spec.ToolCallLimit || after.WorkItems[0].AnalysisRevision != 1 {
		t.Fatalf("重放重复消耗预算或保存内容: %+v", after)
	}
}
