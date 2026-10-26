package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/deng-jiehui/auto-research/internal/domain"
)

type ResearchService struct{ repo domain.ResearchRepository }

func NewResearchService(repo domain.ResearchRepository) *ResearchService {
	return &ResearchService{repo: repo}
}
func (s *ResearchService) ForTurn(ctx context.Context, scope domain.Scope, turnID string) (domain.ResearchRun, error) {
	return s.repo.ResearchForTurn(ctx, scope, turnID)
}
func (s *ResearchService) Get(ctx context.Context, scope domain.Scope, id string) (domain.ResearchRun, error) {
	return s.repo.GetResearch(ctx, scope, id)
}
func (s *ResearchService) List(ctx context.Context, scope domain.Scope) ([]domain.ResearchRun, error) {
	return s.repo.ListResearch(ctx, scope)
}
func (s *ResearchService) Delete(ctx context.Context, scope domain.Scope, id string) error {
	if id == "" {
		return domain.ErrInvalid
	}
	return s.repo.DeleteResearch(ctx, scope, id)
}

func (s *ResearchService) Guard(ctx context.Context, scope domain.Scope, turnID, name string) error {
	run, err := s.ForTurn(ctx, scope, turnID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if name == "ask_user" {
		return nil
	}
	if run.Phase == "cancelled" {
		return fmt.Errorf("%w：用户已取消任务，请结束本轮并说明", domain.ErrConflict)
	}
	switch name {
	case "search_papers", "save_paper", "read_paper", "search_project_evidence", "save_evidence":
		if run.PlanRevision == 0 || run.ApprovedPlanRevision != run.PlanRevision {
			return fmt.Errorf("%w：plan_not_approved，先提交计划并调用 ask_user(kind=research_plan) 请求用户确认；资料 research://runs/%s.json", domain.ErrConflict, run.ID)
		}
	}
	return nil
}

// 完成答复与研究任务完成分离；本批只验收计划确认，绝不将任务标记成报告完成。
func (s *ResearchService) CompletionIssue(ctx context.Context, scope domain.Scope, turnID string) (string, error) {
	run, err := s.ForTurn(ctx, scope, turnID)
	if errors.Is(err, domain.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if run.Phase == "cancelled" {
		return "", nil
	}
	if run.PlanRevision == 0 {
		return "研究计划尚未提交，请使用 submit_research_result(kind=plan) 保存计划，再请求用户审核。", nil
	}
	if run.ApprovedPlanRevision != run.PlanRevision {
		return fmt.Sprintf("计划 v%d 尚未获得用户确认，请调用 ask_user(kind=research_plan, expected_revision=%d)。不能以文字宣布任务完成。", run.PlanRevision, run.PlanRevision), nil
	}
	return "", nil
}

// v5 的完成门只验收候选确认，不将它标记为报告已完成；v4 保留原恢复语义。
func (s *ResearchService) CandidateCompletionIssue(ctx context.Context, scope domain.Scope, turnID string) (string, error) {
	run, err := s.ForTurn(ctx, scope, turnID)
	if errors.Is(err, domain.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if run.Phase == "cancelled" || run.ApprovedCandidateRevision == run.CandidateRevision && run.ApprovedCandidateRevision > 0 {
		return "", nil
	}
	if run.CandidateSearches == 0 {
		return "候选尚未检索，请按确认计划调用 search_papers；用户重新检索时请结合审核反馈。", nil
	}
	return fmt.Sprintf("候选尚未由用户确认，请为全部候选给出推荐理由并调用 ask_user(kind=research_candidates,expected_revision=%d,recommendations=...)。资料 research://runs/%s/candidates.json", run.CandidateRevision, run.ID), nil
}
