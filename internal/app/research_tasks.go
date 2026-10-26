package app

import (
	"context"
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/deng-jiehui/auto-research/internal/domain"
	"github.com/goharness/goharness/pkg/schema"
	"github.com/goharness/goharness/pkg/tool"
)

type StartResearchInput struct {
	domain.ResearchSpec
	OriginSessionID string `json:"origin_session_id,omitempty"`
	OriginTurnID    string `json:"origin_turn_id,omitempty"`
	IdempotencyKey  string `json:"idempotency_key"`
	previousRunID   string
}

func (s *ResearchService) TaskSummaries(ctx context.Context, scope domain.Scope, sessionID string) ([]domain.ResearchTaskSummary, error) {
	return s.repo.ResearchTaskSummaries(ctx, scope, sessionID)
}

func (s *TurnService) StartResearch(ctx context.Context, scope domain.Scope, in StartResearchInput) (domain.ResearchTaskCreated, error) {
	service := s.ResearchService()
	if service == nil {
		return domain.ResearchTaskCreated{}, domain.ErrUnavailable
	}
	in.Question = strings.TrimSpace(in.Question)
	if in.Question == "" || utf8.RuneCountInString(in.Question) > 4000 || in.IdempotencyKey == "" || len(in.IdempotencyKey) > 1024 || len(in.Constraints) > 12 || len(in.PaperIDs) > 12 {
		return domain.ResearchTaskCreated{}, domain.ErrInvalid
	}
	for _, c := range in.Constraints {
		if strings.TrimSpace(c) == "" || utf8.RuneCountInString(c) > 1000 {
			return domain.ResearchTaskCreated{}, domain.ErrInvalid
		}
	}
	seen := map[string]bool{}
	for _, id := range in.PaperIDs {
		if id == "" || seen[id] {
			return domain.ResearchTaskCreated{}, domain.ErrInvalid
		}
		seen[id] = true
	}
	p, err := s.projects.GetProject(ctx, scope)
	if err != nil {
		return domain.ResearchTaskCreated{}, err
	}
	// 研究只接收明确委派的输入，不继承聊天、笔记或旧项目问题。
	p.Question = in.Question
	user := schema.UserMessage(in.Question)
	if len(in.Constraints) > 0 || len(in.PaperIDs) > 0 {
		data, _ := json.Marshal(map[string]any{"constraints": in.Constraints, "paper_ids": in.PaperIDs})
		user = schema.UserMessage(in.Question + "\n明确委派资料：" + string(data))
	}
	encoded, _ := json.Marshal(turnContext{AgentMode: "single", Project: p, UserMessage: user})
	message, _ := json.Marshal(user)
	created, err := service.repo.CreateResearchTask(ctx, scope, domain.ResearchTaskDraft{Spec: in.ResearchSpec, OriginSessionID: in.OriginSessionID, OriginTurnID: in.OriginTurnID, PreviousRunID: in.previousRunID, OperationID: in.IdempotencyKey, ContextJSON: encoded, MessageJSON: message, DefinitionVersion: "autoresearch-agent-single-v8"})
	if err == nil {
		s.signalWake()
	}
	return created, err
}

type startResearchArgs struct {
	Question    string   `json:"question" jsonschema:"明确的独立研究问题；不要把整个聊天历史复制到这里"`
	Constraints []string `json:"constraints,omitempty" jsonschema:"用户明确要求的研究限制，最多 12 条"`
	PaperIDs    []string `json:"paper_ids,omitempty" jsonschema:"用户指定的本项目已有论文 ID，最多 12 篇；作为起始资料，仍须审核最终纳入集"`
}

type researchDelegationKey struct{}

func newStartResearchTool(deps ResearchGraphDeps) tool.Tool {
	original := newAcademicTool[startResearchArgs, map[string]any]("start_research", "用户明确要求自动调研时，启动独立 Research Agent，立即返回任务 ID、任务页和资料地址。研究在后台独立执行，计划/候选审核在任务页完成；本会话可继续。只传问题、明确约束及指定资料，不继承完整聊天。已启动的任务通过 read_file 按需查询，不要重复启动。", func(ctx context.Context, args startResearchArgs) (map[string]any, error) {
		if deps.StartResearch == nil {
			return nil, domain.ErrUnavailable
		}
		key, _ := ctx.Value(researchDelegationKey{}).(string)
		created, err := deps.StartResearch(ctx, StartResearchInput{ResearchSpec: domain.ResearchSpec{Question: args.Question, Constraints: args.Constraints, PaperIDs: args.PaperIDs}, OriginSessionID: deps.SessionID, OriginTurnID: deps.TurnID, IdempotencyKey: key})
		if err != nil {
			return nil, err
		}
		return map[string]any{"run_id": created.Run.ID, "task_url": "/p/" + deps.Scope.ProjectID + "/research/" + created.Run.ID, "resource_path": "research://runs/" + created.Run.ID + ".json", "status": created.Turn.State}, nil
	})
	return tool.NewFunctionTool(original.Spec(), func(ctx context.Context, input tool.CallInput) (tool.Result, error) {
		return original.Invoke(context.WithValue(ctx, researchDelegationKey{}, input.IdempotencyKey), input)
	})
}
