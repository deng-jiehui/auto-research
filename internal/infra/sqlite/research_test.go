package sqlite

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/deng-jiehui/auto-research/internal/domain"
)

func TestResearchStartIsAtomicWithTurn(t *testing.T) {
	s, ctx, scope, session, previous := runtimeFixture(t)
	_ = s.SetTurnState(ctx, scope, previous.ID, domain.TurnCompleted, "", "")
	spec := domain.ResearchSpec{Question: "比较时序预测基线"}
	draft := domain.TurnDraft{InputText: "开始调研", IdempotencyKey: "start", DefinitionVersion: "v4", ContextJSON: []byte(`{}`), Research: &spec}
	if _, err := s.db.ExecContext(ctx, `CREATE TRIGGER fail_research BEFORE INSERT ON research_runs BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AdmitTurn(ctx, scope, session.ID, draft); err == nil {
		t.Fatal("事务故障未被传播")
	}
	var n int
	_ = s.db.QueryRowContext(ctx, `SELECT count(*) FROM turns WHERE idempotency_key='start'`).Scan(&n)
	if n != 0 {
		t.Fatal("研究登记失败却留下孤立 Turn")
	}
	_, _ = s.db.ExecContext(ctx, `DROP TRIGGER fail_research`)
	admitted, err := s.AdmitTurn(ctx, scope, session.ID, draft)
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.ResearchForTurn(ctx, scope, admitted.Turn.ID)
	if err != nil || run.Spec.Question != spec.Question || run.TurnID != admitted.Turn.ID {
		t.Fatalf("关联错误: %+v %v", run, err)
	}
	project, _ := s.GetProject(ctx, scope)
	project.Title = "修改后的项目"
	if err := s.UpdateProject(ctx, scope, project, project.Revision); err != nil {
		t.Fatal(err)
	}
	replayed, err := s.AdmitTurn(ctx, scope, session.ID, draft)
	if err != nil || !replayed.Replayed || replayed.Turn.ID != admitted.Turn.ID {
		t.Fatalf("重发不幂等: %+v %v", replayed, err)
	}
	frozen, _ := s.ResearchForTurn(ctx, scope, admitted.Turn.ID)
	if frozen.ProjectSnapshot.Title == project.Title {
		t.Fatal("项目修改污染输入快照")
	}
	other := spec
	other.Question = "不同问题"
	draft.Research = &other
	if _, err := s.AdmitTurn(ctx, scope, session.ID, draft); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("同执行身份不同内容未冲突: %v", err)
	}
}

func TestResearchPlanOperationIdentityIsSeparateFromRevision(t *testing.T) {
	s, ctx, scope, _, turn := runtimeFixture(t)
	plan := domain.ResearchPlan{Summary: "统一评测", Queries: []string{"time series baseline"}, Inclusion: []string{"公开数据集"}}
	first, err := s.SaveResearchPlan(ctx, scope, turn.ID, "operation-a", 0, plan)
	if err != nil {
		t.Fatal(err)
	}
	plan.Summary = "调整评测"
	second, err := s.SaveResearchPlan(ctx, scope, turn.ID, "operation-b", first.PlanRevision, plan)
	if err != nil || second.ID != first.ID || second.PlanRevision != 2 {
		t.Fatalf("修改: %+v %v", second, err)
	}
	plan.Summary = "统一评测"
	replay, err := s.SaveResearchPlan(ctx, scope, turn.ID, "operation-a", 0, plan)
	if err != nil || replay.PlanRevision != 1 {
		t.Fatalf("重发应先查操作，不能被当前版本拒绝: %+v %v", replay, err)
	}
	plan.Summary = "篡改原操作"
	if _, err = s.SaveResearchPlan(ctx, scope, turn.ID, "operation-a", 0, plan); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("执行身份复用未被拒绝: %v", err)
	}
	if _, err = s.SaveResearchPlan(ctx, domain.Scope{UserID: "other", ProjectID: scope.ProjectID}, turn.ID, "foreign", 0, plan); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("跨用户访问: %v", err)
	}
}

func TestResearchApprovalAndResumeAreAtomic(t *testing.T) {
	s, ctx, scope, session, turn := runtimeFixture(t)
	plan := domain.ResearchPlan{Summary: "初始计划", Queries: []string{"forecasting"}}
	run, err := s.SaveResearchPlan(ctx, scope, turn.ID, "plan-op", 0, plan)
	if err != nil {
		t.Fatal(err)
	}
	pending, _ := json.Marshal([]any{map[string]any{"id": "interrupt-a", "payload": map[string]any{"kind": "research_plan", "run_id": run.ID, "revision": run.PlanRevision, "hash": run.PlanHash}}})
	if err = s.SetPendingQuestions(ctx, scope, turn.ID, pending); err != nil {
		t.Fatal(err)
	}
	turn, _ = s.GetTurn(ctx, scope, session.ID, turn.ID)
	review := domain.ResearchPlanReview{RunID: run.ID, PlanRevision: run.PlanRevision, PlanHash: run.PlanHash, Decision: "approve", Plan: plan}
	review.Plan.Summary = "用户修改后的计划"
	payload, _ := json.Marshal([]any{map[string]any{"interrupt_id": "interrupt-a", "research_plan": review}})
	draft := domain.ActionDraft{Kind: "resume", IdempotencyKey: "review-op", RequestHash: hashBytes(payload), PayloadJSON: payload, Revision: turn.Revision}
	_, _ = s.db.ExecContext(ctx, `CREATE TRIGGER fail_resume BEFORE INSERT ON turn_actions BEGIN SELECT RAISE(ABORT,'injected'); END`)
	if _, err = s.ApplyAction(ctx, scope, session.ID, turn.ID, draft); err == nil {
		t.Fatal("恢复故障未传播")
	}
	unchanged, _ := s.ResearchForTurn(ctx, scope, turn.ID)
	if unchanged.ApprovedPlanRevision != 0 {
		t.Fatal("恢复动作失败却已批准计划")
	}
	_, _ = s.db.ExecContext(ctx, `DROP TRIGGER fail_resume`)
	if _, err = s.ApplyAction(ctx, scope, session.ID, turn.ID, draft); err != nil {
		t.Fatal(err)
	}
	approved, _ := s.ResearchForTurn(ctx, scope, turn.ID)
	if approved.Plan.Summary != "用户修改后的计划" || approved.ApprovedPlanRevision != 2 || approved.Phase != "searching" {
		t.Fatalf("审核没有原子保存: %+v", approved)
	}
	replay, err := s.ApplyAction(ctx, scope, session.ID, turn.ID, draft)
	if err != nil || !replay.Replayed {
		t.Fatalf("旧版本成功请求重发失败: %v", err)
	}
	draft.IdempotencyKey = "stale-op"
	if _, err = s.ApplyAction(ctx, scope, session.ID, turn.ID, draft); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("旧审核版本未冲突: %v", err)
	}
}

func TestResearchBudgetSurvivesRetry(t *testing.T) {
	s, ctx, scope, _, turn := runtimeFixture(t)
	run, err := s.SaveResearchPlan(ctx, scope, turn.ID, "plan", 0, domain.ResearchPlan{Summary: "计划", Queries: []string{"forecasting"}})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < run.Spec.ModelCallLimit; i++ {
		if _, err = s.ReserveResearchCall(ctx, scope, turn.ID, "model"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.ReserveResearchCall(ctx, scope, turn.ID, "model"); !errors.Is(err, domain.ErrBudgetExhausted) {
		t.Fatalf("预算可绕过: %v", err)
	}
	after, _ := s.ResearchForTurn(ctx, scope, turn.ID)
	if after.ModelCalls != run.Spec.ModelCallLimit {
		t.Fatalf("累计计数错误: %+v", after)
	}
	if err = s.SaveMessages(ctx, scope, turn.SessionID, 1, []domain.MessageRecord{{TurnID: turn.ID, ContentJSON: []byte(`{"role":"user","content_blocks":[]}`), Committed: true}}); err != nil {
		t.Fatal(err)
	}
	_ = s.SetTurnState(ctx, scope, turn.ID, domain.TurnFailed, "budget_exhausted", "预算耗尽")
	failed, _ := s.GetTurn(ctx, scope, turn.SessionID, turn.ID)
	if _, err = s.ApplyAction(ctx, scope, turn.SessionID, turn.ID, domain.ActionDraft{Kind: "retry", IdempotencyKey: "retry-after-budget", RequestHash: "retry", PayloadJSON: []byte(`{}`), Revision: failed.Revision}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReserveResearchCall(ctx, scope, turn.ID, "model"); !errors.Is(err, domain.ErrBudgetExhausted) {
		t.Fatalf("重试清除了预算: %v", err)
	}
}

func TestResearchUsageAndAttemptsDoNotChangeContentRevision(t *testing.T) {
	s, ctx, scope, _, turn := runtimeFixture(t)
	first, err := s.ReserveResearchCall(ctx, scope, turn.ID, "model")
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.ReserveResearchCall(ctx, scope, turn.ID, "model")
	if err != nil || first == second {
		t.Fatalf("尝试身份没有区分: %s %s %v", first, second, err)
	}
	if err = s.RecordResearchUsage(ctx, scope, first, 13, 7, true); err != nil {
		t.Fatal(err)
	}
	run, err := s.SaveResearchPlan(ctx, scope, turn.ID, "plan", 0, domain.ResearchPlan{Summary: "计划", Queries: []string{"forecasting"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RecordResearchUsage(ctx, scope, first, 13, 7, true); err != nil {
		t.Fatal(err)
	}
	after, err := s.GetResearch(ctx, scope, run.ID)
	if err != nil || after.PlanRevision != 1 || after.ModelCalls != 2 || after.InputTokens != 13 || after.OutputTokens != 7 || after.UnknownUsage != 1 {
		t.Fatalf("执行统计污染内容或重复累计: %+v %v", after, err)
	}
}

func TestResearchEditCannotReuseFrozenRun(t *testing.T) {
	s, ctx, scope, session, turn := runtimeFixture(t)
	_, err := s.SaveResearchPlan(ctx, scope, turn.ID, "plan", 0, domain.ResearchPlan{Summary: "计划", Queries: []string{"forecasting"}})
	if err != nil {
		t.Fatal(err)
	}
	_ = s.SaveMessages(ctx, scope, session.ID, 1, []domain.MessageRecord{{TurnID: turn.ID, ContentJSON: []byte(`{"role":"user","content_blocks":[]}`), Committed: true}})
	_ = s.SetTurnState(ctx, scope, turn.ID, domain.TurnCompleted, "", "")
	turn, _ = s.GetTurn(ctx, scope, session.ID, turn.ID)
	_, err = s.ApplyAction(ctx, scope, session.ID, turn.ID, domain.ActionDraft{Kind: "edit", IdempotencyKey: "edit", RequestHash: "edit", PayloadJSON: []byte(`{}`), Revision: turn.Revision, Replacement: &domain.TurnReplacement{Text: "另一个研究问题", ContextJSON: []byte(`{}`), MessageJSON: []byte(`{"role":"user","content_blocks":[]}`)}})
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("编辑复用了旧任务快照: %v", err)
	}
}
