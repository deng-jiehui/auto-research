package sqlite

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/deng-jiehui/auto-research/internal/domain"
)

func analysisFixture(t *testing.T) (*Store, domain.Scope, domain.Turn, domain.ResearchRun, domain.SourceDocument) {
	t.Helper()
	s, ctx, scope, turn, run := candidateFixture(t)
	if _, err := s.db.ExecContext(ctx, `UPDATE turns SET definition_version='autoresearch-agent-single-v6' WHERE id=?`, turn.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyAction(ctx, scope, turn.SessionID, turn.ID, candidateReviewDraft(turn, run, "approve")); err != nil {
		t.Fatal(err)
	}
	run, _ = s.ResearchForTurn(ctx, scope, turn.ID)
	doc, err := s.SaveDocument(ctx, scope, domain.SourceDocument{PaperID: run.CandidateReview.Choices[0].PaperID, Text: "公开预测基线", SHA256: domain.SourceTextSHA256("公开预测基线"), ContentKind: "abstract", Locator: "fixture:abstract"})
	if err != nil {
		t.Fatal(err)
	}
	return s, scope, turn, run, doc
}

func unknownAnalysis(doc domain.SourceDocument) domain.PaperAnalysisDraft {
	fields := []domain.AnalysisField{}
	for _, name := range []string{"method", "datasets", "metrics", "code", "reproduction"} {
		fields = append(fields, domain.AnalysisField{Name: name, Status: "unknown", Text: "摘要未提供"})
	}
	return domain.PaperAnalysisDraft{PaperID: doc.PaperID, DocumentID: doc.ID, Status: "completed", Summary: "仅核对摘要，实验细节尚不明确", Fields: fields}
}

func TestResearchAnalysisSkipsExcludedPaper(t *testing.T) {
	s, scope, turn, run, doc := analysisFixture(t)
	ctx := t.Context()
	if len(run.WorkItems) != 1 {
		t.Fatalf("应仅为纳入论文建项: %+v", run.WorkItems)
	}
	draft := unknownAnalysis(doc)
	draft.PaperID = "excluded"
	if _, err := s.SaveResearchAnalysis(ctx, scope, turn.ID, "excluded-op", 0, draft); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("排除论文未拒绝: %v", err)
	}
}

func TestResearchWorkItemReplayIsIdempotent(t *testing.T) {
	s, scope, turn, _, doc := analysisFixture(t)
	ctx := t.Context()
	item, err := s.BeginResearchPaper(ctx, scope, turn.ID, doc.PaperID, doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	draft := unknownAnalysis(doc)
	saved, err := s.SaveResearchAnalysis(ctx, scope, turn.ID, "save", item.Revision, draft)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := s.SaveResearchAnalysis(ctx, scope, turn.ID, "save", item.Revision, draft)
	if err != nil || replay.Revision != saved.Revision {
		t.Fatalf("成功重放失败: %+v %v", replay, err)
	}
	draft.Summary = "同身份不同内容"
	if _, err = s.SaveResearchAnalysis(ctx, scope, turn.ID, "save", item.Revision, draft); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("内容冲突未拒绝: %v", err)
	}
	var count int
	if err = s.db.QueryRowContext(ctx, `SELECT count(*) FROM research_analysis_versions WHERE work_item_id=?`, saved.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("重复保存: %d %v", count, err)
	}
}

func TestResearchAnalysisRequiresMatchingEvidence(t *testing.T) {
	s, scope, turn, _, doc := analysisFixture(t)
	ctx := t.Context()
	item, err := s.BeginResearchPaper(ctx, scope, turn.ID, doc.PaperID, doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	draft := unknownAnalysis(doc)
	draft.Fields[0] = domain.AnalysisField{Name: "method", Status: "finding", Text: "公开基线"}
	if _, err = s.SaveResearchAnalysis(ctx, scope, turn.ID, "missing", item.Revision, draft); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("无依据结论未拒绝: %v", err)
	}
	e, err := s.SaveEvidence(ctx, scope, domain.Evidence{ProjectID: scope.ProjectID, PaperID: doc.PaperID, DocumentID: doc.ID, BlockID: "b1", Quote: doc.Text, StartRune: 0, EndRune: 6, Field: "method", Value: "公开基线"})
	if err != nil {
		t.Fatal(err)
	}
	draft.Fields[0].EvidenceIDs = []string{e.ID}
	if _, err = s.SaveResearchAnalysis(ctx, scope, turn.ID, "valid", item.Revision, draft); err != nil {
		t.Fatal(err)
	}
}

func TestResearchRetryKeepsBudget(t *testing.T) {
	s, scope, turn, run, doc := analysisFixture(t)
	ctx := t.Context()
	item, err := s.BeginResearchPaper(ctx, scope, turn.ID, doc.PaperID, doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	failed, err := s.SaveResearchAnalysis(ctx, scope, turn.ID, "fail", item.Revision, domain.PaperAnalysisDraft{PaperID: doc.PaperID, DocumentID: doc.ID, Status: "failed", Error: "来源不可用"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReserveResearchCall(ctx, scope, turn.ID, "model"); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveMessages(ctx, scope, turn.SessionID, 1, []domain.MessageRecord{{TurnID: turn.ID, ContentJSON: []byte(`{"role":"user","content":"目标"}`), Committed: true}}); err != nil {
		t.Fatal(err)
	}
	_ = s.SetTurnState(ctx, scope, turn.ID, domain.TurnCompleted, "", "")
	current, _ := s.GetTurn(ctx, scope, turn.SessionID, turn.ID)
	payload, _ := json.Marshal(map[string]any{"paper_id": doc.PaperID, "work_revision": failed.Revision})
	draft := domain.ActionDraft{Kind: "research_retry", IdempotencyKey: "retry-one", Revision: current.Revision, RequestHash: hashBytes(payload), PayloadJSON: payload}
	result, err := s.ApplyAction(ctx, scope, turn.SessionID, turn.ID, draft)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := s.GetResearch(ctx, scope, run.ID)
	if result.Turn.State != domain.TurnQueued || after.ModelCalls != 1 || after.WorkItems[0].State != "pending" || after.WorkItems[0].AnalysisRevision != 1 {
		t.Fatalf("重试污染预算/版本: %+v", after)
	}
	if after.WorkItems[0].Analysis != nil || after.WorkItems[0].AnalysisHash != "" || after.WorkItems[0].AttemptID != "" {
		t.Fatalf("重试未清除本次失败内容与执行身份: %+v", after.WorkItems[0])
	}
	if r, e := s.ApplyAction(ctx, scope, turn.SessionID, turn.ID, draft); e != nil || !r.Replayed {
		t.Fatalf("重试动作不幂等: %+v %v", r, e)
	}
}

func TestResearchAnalysisRejectsChangedDocumentAndSummaryExperimentClaims(t *testing.T) {
	s, scope, turn, _, doc := analysisFixture(t)
	ctx := t.Context()
	item, err := s.BeginResearchPaper(ctx, scope, turn.ID, doc.PaperID, doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	newer, err := s.SaveDocument(ctx, scope, domain.SourceDocument{PaperID: doc.PaperID, Text: "另一份文档", SHA256: domain.SourceTextSHA256("另一份文档"), Locator: "newer", ContentKind: "abstract"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.BeginResearchPaper(ctx, scope, turn.ID, doc.PaperID, newer.ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("冻结来源被替换: %v", err)
	}
	draft := unknownAnalysis(doc)
	e, err := s.SaveEvidence(ctx, scope, domain.Evidence{ProjectID: scope.ProjectID, PaperID: doc.PaperID, DocumentID: newer.ID, Quote: newer.Text, StartRune: 0, EndRune: 5, Field: "method", Value: "另一个版本"})
	if err != nil {
		t.Fatal(err)
	}
	draft.Fields[0] = domain.AnalysisField{Name: "method", Status: "finding", Text: "另一个版本", EvidenceIDs: []string{e.ID}}
	if _, err = s.SaveResearchAnalysis(ctx, scope, turn.ID, "foreign-doc", item.Revision, draft); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("其他来源证据未拒绝: %v", err)
	}
	draft = unknownAnalysis(doc)
	draft.Fields[1] = domain.AnalysisField{Name: "datasets", Status: "finding", Text: "摘要不能确认完整实验", EvidenceIDs: []string{e.ID}}
	if _, err = s.SaveResearchAnalysis(ctx, scope, turn.ID, "abstract-claim", item.Revision, draft); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("摘要冒充实验核实: %v", err)
	}
}

func TestResearchAnalysisFreezesApprovedPlan(t *testing.T) {
	s, scope, turn, run, _ := analysisFixture(t)
	if _, err := s.SaveResearchPlan(t.Context(), scope, turn.ID, "change-after-selection", run.PlanRevision, domain.ResearchPlan{Summary: "替换范围", Queries: []string{"another"}}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("分析开始后仍能覆盖计划: %v", err)
	}
}

func TestResearchFailedRunRetryRequeuesFailedWork(t *testing.T) {
	s, scope, turn, _, doc := analysisFixture(t)
	ctx := t.Context()
	item, err := s.BeginResearchPaper(ctx, scope, turn.ID, doc.PaperID, doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveResearchAnalysis(ctx, scope, turn.ID, "failed", item.Revision, domain.PaperAnalysisDraft{PaperID: doc.PaperID, DocumentID: doc.ID, Status: "failed", Error: "读取失败"}); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveMessages(ctx, scope, turn.SessionID, 1, []domain.MessageRecord{{TurnID: turn.ID, ContentJSON: []byte(`{"role":"user","content":"目标"}`), Committed: true}}); err != nil {
		t.Fatal(err)
	}
	_ = s.SetTurnState(ctx, scope, turn.ID, domain.TurnFailed, "execution_failed", "模型暂不可用")
	current, _ := s.GetTurn(ctx, scope, turn.SessionID, turn.ID)
	if _, err = s.ApplyAction(ctx, scope, turn.SessionID, turn.ID, domain.ActionDraft{Kind: "retry", IdempotencyKey: "retry-run", RequestHash: "retry", Revision: current.Revision, PayloadJSON: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	after, _ := s.ResearchForTurn(ctx, scope, turn.ID)
	if after.WorkItems[0].State != "pending" || after.WorkItems[0].DocumentID != doc.ID || after.WorkItems[0].AnalysisRevision != 1 {
		t.Fatalf("失败轮次重试未恢复工作项: %+v", after.WorkItems)
	}
}
