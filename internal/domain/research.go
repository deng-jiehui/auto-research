package domain

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

var ErrBudgetExhausted = errors.New("调研累计执行预算已耗尽")
var ErrResearchFrozen = errors.New("调研输入已冻结，请在调研任务页新建任务；旧任务仍可查看或重试")

// ResearchSpec 是冻结的任务输入；身份和执行尝试不属于内容版本。
type ResearchSpec struct {
	Constraints       []string         `json:"constraints,omitempty"`
	PaperIDs          []string         `json:"paper_ids,omitempty"`
	Question          string           `json:"question"`
	DirectionID       string           `json:"direction_id,omitempty"`
	DirectionRevision int64            `json:"direction_revision,omitempty"`
	Settings          ResearchSettings `json:"settings"`
	ModelCallLimit    int              `json:"model_call_limit,omitempty"`
	ToolCallLimit     int              `json:"tool_call_limit,omitempty"`
}

type ResearchPlan struct {
	Summary   string   `json:"summary" jsonschema:"本轮研究目标和计划，最多 4000 字符"`
	Queries   []string `json:"queries" jsonschema:"1 至 10 条拟执行的检索式"`
	Inclusion []string `json:"inclusion,omitempty" jsonschema:"纳入条件"`
	Exclusion []string `json:"exclusion,omitempty" jsonschema:"排除条件"`
}

func ValidateResearchPlan(plan ResearchPlan) error {
	if strings.TrimSpace(plan.Summary) == "" || utf8.RuneCountInString(plan.Summary) > 4000 || len(plan.Queries) < 1 || len(plan.Queries) > 10 || len(plan.Inclusion) > 10 || len(plan.Exclusion) > 10 {
		return fmt.Errorf("%w：计划须有说明、1 至 10 条检索式，条件各不超过 10 条", ErrInvalid)
	}
	for _, list := range [][]string{plan.Queries, plan.Inclusion, plan.Exclusion} {
		for _, value := range list {
			if strings.TrimSpace(value) == "" || utf8.RuneCountInString(value) > 1000 {
				return fmt.Errorf("%w：检索式和条件须为 1 至 1000 字符", ErrInvalid)
			}
		}
	}
	return nil
}

type ResearchRun struct {
	LatestReport              *ReportReference         `json:"latest_report,omitempty"`
	Detached                  bool                     `json:"detached,omitempty"`
	OriginSessionID           string                   `json:"origin_session_id,omitempty"`
	OriginTurnID              string                   `json:"origin_turn_id,omitempty"`
	CancelRequested           bool                     `json:"-"`
	PreviousRunID             string                   `json:"previous_run_id,omitempty"`
	WorkItems                 []ResearchWorkItem       `json:"work_items,omitempty"`
	PauseRequested            bool                     `json:"pause_requested"`
	Candidates                []ResearchCandidate      `json:"candidates,omitempty"`
	CandidateRound            int                      `json:"candidate_round"`
	CandidateSearches         int                      `json:"candidate_searches"`
	CandidateRevision         int64                    `json:"candidate_revision"`
	CandidateHash             string                   `json:"candidate_hash,omitempty"`
	ApprovedCandidateRevision int64                    `json:"approved_candidate_revision"`
	CandidateReview           *ResearchCandidateReview `json:"candidate_review,omitempty"`
	ExecutionState            string                   `json:"execution_state,omitempty"`
	ExecutionAttempt          int                      `json:"execution_attempt"`
	ErrorMessage              string                   `json:"error_message,omitempty"`
	ID                        string                   `json:"id"`
	ProjectID                 string                   `json:"project_id"`
	SessionID                 string                   `json:"session_id"`
	TurnID                    string                   `json:"turn_id"`
	Phase                     string                   `json:"phase"`
	Spec                      ResearchSpec             `json:"spec"`
	SpecHash                  string                   `json:"spec_hash"`
	ProjectSnapshot           Project                  `json:"project_snapshot"`
	DirectionSnapshot         *Direction               `json:"direction_snapshot,omitempty"`
	Plan                      ResearchPlan             `json:"plan"`
	PlanRevision              int64                    `json:"plan_revision"`
	ApprovedPlanRevision      int64                    `json:"approved_plan_revision"`
	PlanHash                  string                   `json:"plan_hash"`
	ModelCalls                int                      `json:"model_calls"`
	ToolCalls                 int                      `json:"tool_calls"`
	InputTokens               int64                    `json:"input_tokens"`
	OutputTokens              int64                    `json:"output_tokens"`
	UnknownUsage              int                      `json:"unknown_usage"`
}

// ResearchPlanReview 来自可信用户 API；模型工具不接受此批准结构。
type ResearchPlanReview struct {
	RunID        string       `json:"run_id"`
	PlanRevision int64        `json:"plan_revision"`
	PlanHash     string       `json:"plan_hash"`
	Decision     string       `json:"decision"`
	Plan         ResearchPlan `json:"plan"`
}

type ResearchRepository interface {
	SaveResearchReport(context.Context, Scope, string, string, int64, ReportDraft) (ResearchReport, error)
	ResearchReportReplay(context.Context, Scope, string, string, int64, ReportDraft) (ResearchReport, error)
	GetResearchReport(context.Context, Scope, string) (ResearchReport, error)
	ListResearchReports(context.Context, Scope, string) ([]ResearchReport, error)
	CreateResearchTask(context.Context, Scope, ResearchTaskDraft) (ResearchTaskCreated, error)
	DeleteResearch(context.Context, Scope, string) error
	ResearchTaskSummaries(context.Context, Scope, string) ([]ResearchTaskSummary, error)
	SetResearchPause(context.Context, Scope, string, bool) error
	BeginResearchPaper(context.Context, Scope, string, string, string) (ResearchWorkItem, error)
	SaveResearchAnalysis(context.Context, Scope, string, string, int64, PaperAnalysisDraft) (ResearchWorkItem, error)
	ResearchAnalysisReplay(context.Context, Scope, string, string, int64, PaperAnalysisDraft) (ResearchWorkItem, error)
	ResearchForTurn(context.Context, Scope, string) (ResearchRun, error)
	GetResearch(context.Context, Scope, string) (ResearchRun, error)
	ListResearch(context.Context, Scope) ([]ResearchRun, error)
	SaveResearchPlan(context.Context, Scope, string, string, int64, ResearchPlan) (ResearchRun, error)
	ReserveResearchCall(context.Context, Scope, string, string) (string, error)
	RecordResearchUsage(context.Context, Scope, string, int64, int64, bool) error
	ResearchSearchReplay(context.Context, Scope, string, string, string) (ResearchRun, error)
	SaveResearchCandidates(context.Context, Scope, string, string, string, []Candidate) (ResearchRun, error)
	PrepareCandidateReview(context.Context, Scope, string, string, int64, []CandidateRecommendation) (ResearchRun, error)
}

// 候选身份在 Run 内稳定；推荐和用户决定分别保存，模型不能写入批准状态。
type ResearchCandidate struct {
	Candidate
	ID                   string `json:"id"`
	Recommended          bool   `json:"recommended"`
	RecommendationReason string `json:"recommendation_reason,omitempty"`
}

type CandidateRecommendation struct {
	CandidateID string `json:"candidate_id"`
	Recommended bool   `json:"recommended"`
	Reason      string `json:"reason"`
}

type CandidateDecision struct {
	CandidateID string `json:"candidate_id"`
	Selected    bool   `json:"selected"`
	Reason      string `json:"reason"`
	PaperID     string `json:"paper_id,omitempty"`
}

// 此结构只由可信审核 API 接收；PaperID 由事务内归档生成，不接受用户指定。
type ResearchCandidateReview struct {
	RunID             string              `json:"run_id"`
	CandidateRevision int64               `json:"candidate_revision"`
	CandidateHash     string              `json:"candidate_hash"`
	Decision          string              `json:"decision"`
	AnalyzeLimit      int                 `json:"analyze_limit"`
	Choices           []CandidateDecision `json:"choices"`
	Feedback          string              `json:"feedback,omitempty"`
}
