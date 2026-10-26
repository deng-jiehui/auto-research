package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/deng-jiehui/auto-research/internal/domain"
)

func candidateFixture(t *testing.T) (*Store, context.Context, domain.Scope, domain.Turn, domain.ResearchRun) {
	t.Helper()
	s, ctx, scope, _, turn := runtimeFixture(t)
	run, err := s.SaveResearchPlan(ctx, scope, turn.ID, "plan", 0, domain.ResearchPlan{Summary: "预测基线", Queries: []string{"forecasting"}})
	if err != nil {
		t.Fatal(err)
	}
	// 计划审核的事务路径已有独立测试，这里固定其确认结果。
	tx, _ := s.db.BeginTx(ctx, nil)
	run.ApprovedPlanRevision, run.Phase = run.PlanRevision, "searching"
	if err = putResearch(ctx, tx, scope, run); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	candidates := []domain.Candidate{{SourceKey: "arxiv:2301.00001", Title: "Baseline", Abstract: "公开预测基线", Provider: "arxiv"}, {SourceKey: "arxiv:2301.00002", Title: "Other", Provider: "arxiv"}}
	run, err = s.SaveResearchCandidates(ctx, scope, turn.ID, "search", "query-hash", candidates)
	if err != nil {
		t.Fatal(err)
	}
	recs := []domain.CandidateRecommendation{{CandidateID: run.Candidates[0].ID, Recommended: true, Reason: "公开基线"}, {CandidateID: run.Candidates[1].ID, Reason: "范围不匹配"}}
	run, err = s.PrepareCandidateReview(ctx, scope, turn.ID, "review", run.CandidateRevision, recs)
	if err != nil {
		t.Fatal(err)
	}
	pending, _ := json.Marshal([]any{map[string]any{"id": "candidate-interrupt", "payload": map[string]any{"kind": "research_candidates", "run_id": run.ID, "revision": run.CandidateRevision, "hash": run.CandidateHash}}})
	if err = s.SetPendingQuestions(ctx, scope, turn.ID, pending); err != nil {
		t.Fatal(err)
	}
	turn, _ = s.GetTurn(ctx, scope, turn.SessionID, turn.ID)
	return s, ctx, scope, turn, run
}

func candidateReviewDraft(turn domain.Turn, run domain.ResearchRun, operation string) domain.ActionDraft {
	review := domain.ResearchCandidateReview{RunID: run.ID, CandidateRevision: run.CandidateRevision, CandidateHash: run.CandidateHash, Decision: "approve", AnalyzeLimit: 1, Choices: []domain.CandidateDecision{{CandidateID: run.Candidates[0].ID, Selected: true, Reason: "保留基线"}, {CandidateID: run.Candidates[1].ID, Reason: "暂不纳入"}}}
	return candidateReviewAction(turn, operation, review)
}

func candidateReviewAction(turn domain.Turn, operation string, review domain.ResearchCandidateReview) domain.ActionDraft {
	payload, _ := json.Marshal([]any{map[string]any{"interrupt_id": "candidate-interrupt", "research_candidates": review}})
	return domain.ActionDraft{Kind: "resume", Revision: turn.Revision, IdempotencyKey: operation, RequestHash: hashBytes(payload), PayloadJSON: payload}
}

func TestCandidateApprovalAndResumeAreAtomic(t *testing.T) {
	s, ctx, scope, turn, run := candidateFixture(t)
	_, err := s.db.ExecContext(ctx, `CREATE TRIGGER fail_candidate_resume BEFORE INSERT ON turn_actions BEGIN SELECT RAISE(ABORT,'injected'); END`)
	if err != nil {
		t.Fatal(err)
	}
	draft := candidateReviewDraft(turn, run, "approve-candidates")
	if _, err = s.ApplyAction(ctx, scope, turn.SessionID, turn.ID, draft); err == nil {
		t.Fatal("事务故障未传播")
	}
	after, _ := s.ResearchForTurn(ctx, scope, turn.ID)
	papers, _ := s.ListPapers(ctx, scope, domain.PaperQuery{})
	if after.ApprovedCandidateRevision != 0 || len(papers) != 0 {
		t.Fatal("恢复失败却留下候选批准或论文")
	}
	_, _ = s.db.ExecContext(ctx, `DROP TRIGGER fail_candidate_resume`)
	if _, err = s.ApplyAction(ctx, scope, turn.SessionID, turn.ID, draft); err != nil {
		t.Fatal(err)
	}
	after, _ = s.ResearchForTurn(ctx, scope, turn.ID)
	papers, _ = s.ListPapers(ctx, scope, domain.PaperQuery{})
	if after.ApprovedCandidateRevision != run.CandidateRevision || after.Phase != "candidates_selected" || len(papers) != 1 || after.CandidateReview.Choices[0].PaperID != papers[0].ID {
		t.Fatalf("批准/归档未生效: %+v %+v", after, papers)
	}
	result, err := s.ApplyAction(ctx, scope, turn.SessionID, turn.ID, draft)
	if err != nil || !result.Replayed {
		t.Fatalf("成功请求旧版本重发应复用结果: %+v %v", result, err)
	}
	draft.IdempotencyKey = "stale"
	if _, err = s.ApplyAction(ctx, scope, turn.SessionID, turn.ID, draft); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("旧表单未冲突: %v", err)
	}
}

func TestCandidateReviewRejectsInvalidScopeVersionAndLimit(t *testing.T) {
	for _, kind := range []string{"foreign_run", "foreign_candidate", "stale_revision", "stale_hash", "too_many", "duplicate", "forged_paper", "empty_approve", "mixed_answer"} {
		t.Run(kind, func(t *testing.T) {
			s, ctx, scope, turn, run := candidateFixture(t)
			draft := candidateReviewDraft(turn, run, "invalid")
			var answers []struct {
				InterruptID        string                         `json:"interrupt_id"`
				ResearchCandidates domain.ResearchCandidateReview `json:"research_candidates"`
				ResearchPlan       *domain.ResearchPlanReview     `json:"research_plan,omitempty"`
			}
			_ = json.Unmarshal(draft.PayloadJSON, &answers)
			r := &answers[0].ResearchCandidates
			switch kind {
			case "foreign_run":
				r.RunID = "another-run"
			case "foreign_candidate":
				r.Choices[0].CandidateID = "another-run-candidate"
			case "stale_revision":
				r.CandidateRevision--
			case "stale_hash":
				r.CandidateHash = "old"
			case "too_many":
				r.Choices[1].Selected = true
			case "duplicate":
				r.Choices[1].CandidateID = r.Choices[0].CandidateID
			case "forged_paper":
				r.Choices[0].PaperID = "fake"
			case "empty_approve":
				r.Choices[0].Selected = false
			case "mixed_answer":
				answers[0].ResearchPlan = &domain.ResearchPlanReview{}
			}
			draft.PayloadJSON, _ = json.Marshal(answers)
			draft.RequestHash = hashBytes(draft.PayloadJSON)
			if _, err := s.ApplyAction(ctx, scope, turn.SessionID, turn.ID, draft); err == nil {
				t.Fatal("非法审核被接受")
			}
			after, _ := s.ResearchForTurn(ctx, scope, turn.ID)
			if after.ApprovedCandidateRevision != 0 {
				t.Fatal("非法审核改变了批准状态")
			}
		})
	}
}

func TestCandidateSearchAgainKeepsHistoryAndStartsNewRound(t *testing.T) {
	s, ctx, scope, turn, run := candidateFixture(t)
	review := domain.ResearchCandidateReview{RunID: run.ID, CandidateRevision: run.CandidateRevision, CandidateHash: run.CandidateHash, Decision: "search_again", AnalyzeLimit: 1, Feedback: "改查轻量基线", Choices: []domain.CandidateDecision{{CandidateID: run.Candidates[0].ID, Reason: "不满足目标"}, {CandidateID: run.Candidates[1].ID, Reason: "不匹配"}}}
	draft := candidateReviewAction(turn, "again", review)
	if _, err := s.ApplyAction(ctx, scope, turn.SessionID, turn.ID, draft); err != nil {
		t.Fatal(err)
	}
	after, _ := s.ResearchForTurn(ctx, scope, turn.ID)
	if len(after.Candidates) != 0 || after.CandidateRound != 2 || after.CandidateSearches != 0 || after.CandidateReview.Feedback != review.Feedback {
		t.Fatalf("没有开启新轮次: %+v", after)
	}
	var n int
	_ = s.db.QueryRowContext(ctx, `SELECT count(*) FROM research_candidate_reviews WHERE run_id=?`, run.ID).Scan(&n)
	if n != 1 {
		t.Fatal("旧审核记录丢失")
	}
	newRun, err := s.SaveResearchCandidates(ctx, scope, turn.ID, "new-search", "new-query", []domain.Candidate{run.Candidates[0].Candidate})
	if err != nil || len(newRun.Candidates) != 1 || newRun.Candidates[0].ID != run.Candidates[0].ID || newRun.CandidateRevision <= run.CandidateRevision {
		t.Fatalf("新检索错误: %+v %v", newRun, err)
	}
	if _, err = s.PrepareCandidateReview(ctx, scope, turn.ID, "stale-review", run.CandidateRevision, nil); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("旧集合可审核新轮次: %v", err)
	}
}

func TestCandidateImportPreservesExistingPaperNotes(t *testing.T) {
	s, ctx, scope, turn, run := candidateFixture(t)
	c := run.Candidates[0]
	paper, err := s.ImportPaper(ctx, scope, domain.Paper{SourceKey: c.SourceKey, Title: c.Title, Note: "用户已有的阅读笔记"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplyAction(ctx, scope, turn.SessionID, turn.ID, candidateReviewDraft(turn, run, "keep-note")); err != nil {
		t.Fatal(err)
	}
	after, _ := s.ResearchForTurn(ctx, scope, turn.ID)
	stored, _ := s.GetPaper(ctx, scope, paper.ID)
	if stored.Note != paper.Note || after.CandidateReview.Choices[0].PaperID != paper.ID {
		t.Fatalf("重复归档覆盖笔记或创建新身份: %+v %+v", stored, after.CandidateReview)
	}
}
