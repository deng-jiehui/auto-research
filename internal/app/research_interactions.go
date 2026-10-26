package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/deng-jiehui/auto-research/internal/domain"
	"github.com/goharness/goharness/pkg/graph"
	"github.com/goharness/goharness/pkg/runnable"
	"github.com/goharness/goharness/pkg/schema"
	"github.com/goharness/goharness/pkg/tool"
)

type researchAskUserArgs struct {
	AskUserArgs
	Kind             string `json:"kind,omitempty" jsonschema:"clarification（默认）或 research_plan；研究计划审核必须先保存计划"`
	ExpectedRevision int64  `json:"expected_revision,omitempty" jsonschema:"research_plan 必须传入待审核计划的版本"`
}

type researchReviewPrompt struct {
	Kind            string              `json:"kind"`
	Question        string              `json:"question"`
	RunID           string              `json:"run_id"`
	Revision        int64               `json:"revision"`
	Hash            string              `json:"hash"`
	Plan            domain.ResearchPlan `json:"plan"`
	RequestRevision int64               `json:"request_revision,omitempty"`
	CandidateIDs    []string            `json:"candidate_ids,omitempty"`
	AnalyzeLimit    int                 `json:"analyze_limit,omitempty"`
}

func newPlanAskUserTool(prompt *researchReviewPrompt, bindings ...ResearchGraphDeps) tool.Tool {
	return tool.New[researchAskUserArgs, AskUserResult]("ask_user", "向用户请求 clarification 或 research_plan 审核，独占调用批次。普通补充可提供可编辑选项；计划审核在 submit_research_result(kind=plan) 后调用，携带 expected_revision，只展示后台保存的真实计划，用户可编辑、确认或取消。", func(ctx context.Context, args researchAskUserArgs) (AskUserResult, error) {
		if args.Kind == "" || args.Kind == "clarification" {
			raw, _ := json.Marshal(args.AskUserArgs)
			result, err := newAskUserTool().Invoke(ctx, tool.CallInput{RawArgs: string(raw)})
			if err != nil {
				return AskUserResult{}, err
			}
			blocks, err := result.ToContentBlocks()
			if err != nil {
				return AskUserResult{}, err
			}
			var answer AskUserResult
			err = json.Unmarshal([]byte(schema.NewMessage(schema.RoleTool, blocks...).JoinedText("")), &answer)
			return answer, err
		}
		if args.Kind != "research_plan" || prompt == nil || args.ExpectedRevision != prompt.Revision {
			return AskUserResult{}, fmt.Errorf("%w：未绑定待审核计划", domain.ErrInvalid)
		}
		value, err := graph.Interrupt(ctx, prompt)
		if err != nil {
			return AskUserResult{}, err
		}
		data, err := json.Marshal(value)
		if err != nil {
			return AskUserResult{}, err
		}
		var review domain.ResearchPlanReview
		if err = json.Unmarshal(data, &review); err != nil {
			return AskUserResult{}, err
		}
		if review.RunID != prompt.RunID || review.PlanRevision != prompt.Revision || review.PlanHash != prompt.Hash || (review.Decision != "approve" && review.Decision != "cancel") {
			return AskUserResult{}, domain.ErrConflict
		}
		if len(bindings) == 0 || bindings[0].Research == nil {
			return AskUserResult{}, domain.ErrUnavailable
		}
		deps := bindings[0]
		saved, err := deps.Research.ForTurn(ctx, deps.Scope, deps.TurnID)
		if err != nil {
			return AskUserResult{}, err
		}
		if saved.ID != review.RunID || (review.Decision == "approve" && (saved.ApprovedPlanRevision == 0 || saved.ApprovedPlanRevision != saved.PlanRevision)) || (review.Decision == "cancel" && saved.Phase != "cancelled") {
			return AskUserResult{}, domain.ErrConflict
		}
		review.PlanRevision, review.PlanHash, review.Plan = saved.PlanRevision, saved.PlanHash, saved.Plan
		return AskUserResult{Answer: "用户已提交研究计划审核：" + review.Decision, ResearchPlan: &review}, nil
	})
}

type candidateAskUserArgs struct {
	AskUserArgs
	Kind             string                           `json:"kind,omitempty" jsonschema:"clarification（默认）、research_plan 或 research_candidates；候选审核须先检索并提供全部推荐理由"`
	ExpectedRevision int64                            `json:"expected_revision,omitempty" jsonschema:"审核对象的当前版本：research_plan 使用计划版本，research_candidates 使用 candidate_revision"`
	Recommendations  []domain.CandidateRecommendation `json:"recommendations,omitempty" jsonschema:"research_candidates 专用；为当前全部候选给出 candidate_id、recommended 和 reason（1 至 1000 字符），不允许重建论文元数据或填写批准状态"`
}

func (a candidateAskUserArgs) planArgs() researchAskUserArgs {
	return researchAskUserArgs{AskUserArgs: a.AskUserArgs, Kind: a.Kind, ExpectedRevision: a.ExpectedRevision}
}

func newResearchAskUserTool(prompt *researchReviewPrompt, bindings ...ResearchGraphDeps) tool.Tool {
	if len(bindings) == 0 || !supportsCandidateReview(bindings[0]) {
		return newPlanAskUserTool(prompt, bindings...)
	}
	deps := bindings[0]
	return tool.New[candidateAskUserArgs, AskUserResult]("ask_user", "向用户请求信息或审核，必须独占批次。kind=clarification 使用可编辑选项；research_plan 审核已保存计划；research_candidates 审核 search_papers 保存的真实候选，传 expected_revision 与全部候选的 recommendations。用户可勾选、编辑纳入/排除理由、调整分析上限、全部排除并重新检索或取消。模型的推荐不等于用户批准。", func(ctx context.Context, args candidateAskUserArgs) (AskUserResult, error) {
		if args.Kind != "research_candidates" {
			if len(args.Recommendations) != 0 {
				return AskUserResult{}, domain.ErrInvalid
			}
			raw, _ := json.Marshal(args.planArgs())
			result, err := newPlanAskUserTool(prompt, bindings...).Invoke(ctx, tool.CallInput{RawArgs: string(raw)})
			if err != nil {
				return AskUserResult{}, err
			}
			blocks, err := result.ToContentBlocks()
			if err != nil {
				return AskUserResult{}, err
			}
			var answer AskUserResult
			err = json.Unmarshal([]byte(schema.NewMessage(schema.RoleTool, blocks...).JoinedText("")), &answer)
			return answer, err
		}
		if prompt == nil || prompt.Kind != args.Kind || args.ExpectedRevision != prompt.RequestRevision {
			return AskUserResult{}, domain.ErrInvalid
		}
		value, err := graph.Interrupt(ctx, prompt)
		if err != nil {
			return AskUserResult{}, err
		}
		data, err := json.Marshal(value)
		if err != nil {
			return AskUserResult{}, err
		}
		var review domain.ResearchCandidateReview
		if err = json.Unmarshal(data, &review); err != nil {
			return AskUserResult{}, err
		}
		if review.RunID != prompt.RunID || review.CandidateRevision != prompt.Revision || review.CandidateHash != prompt.Hash {
			return AskUserResult{}, domain.ErrConflict
		}
		saved, err := deps.Research.ForTurn(ctx, deps.Scope, deps.TurnID)
		if err != nil {
			return AskUserResult{}, err
		}
		if saved.CandidateReview == nil || saved.CandidateReview.CandidateRevision != review.CandidateRevision || saved.CandidateReview.Decision != review.Decision || saved.CandidateReview.CandidateHash != review.CandidateHash {
			return AskUserResult{}, domain.ErrConflict
		}
		return AskUserResult{Answer: "用户已提交候选审核：" + review.Decision, ResearchCandidates: saved.CandidateReview}, nil
	})
}

type researchClarifyInput struct {
	Messages []schema.Message
	Prompt   *researchReviewPrompt
}

func researchClarifyNode(deps ResearchGraphDeps) runnable.Runnable[researchClarifyInput, []schema.Message] {
	return runnable.Func[researchClarifyInput, []schema.Message](func(ctx context.Context, input researchClarifyInput, options ...runnable.Option) ([]schema.Message, error) {
		wrapped := researchExecutionTool(newResearchAskUserTool(input.Prompt, deps), deps)
		node, err := tool.NewToolsNode(tool.ToolNodeConfig{Name: "clarify", Tools: []tool.Tool{wrapped}, MaxConcurrency: 1})
		if err != nil {
			return nil, err
		}
		return node.Invoke(ctx, input.Messages, options...)
	})
}

func prepareResearchInteraction(ctx context.Context, deps ResearchGraphDeps, state *ResearchState, messages []schema.Message) (bool, error) {
	for _, message := range messages {
		for _, call := range message.FunctionToolCalls() {
			if call.Name != "ask_user" {
				continue
			}
			var parsed candidateAskUserArgs
			var args researchAskUserArgs
			var decodeErr error
			if supportsCandidateReview(deps) {
				decodeErr = decodeResearchArgs(call.Arguments, &parsed)
				args = parsed.planArgs()
			} else {
				decodeErr = decodeResearchArgs(call.Arguments, &args)
			}
			if decodeErr != nil {
				return false, decodeErr
			}
			if args.Kind != "research_candidates" && len(parsed.Recommendations) != 0 {
				return false, domain.ErrInvalid
			}
			if args.Kind == "research_candidates" {
				if !supportsCandidateReview(deps) || len(messages) != 1 || len(message.FunctionToolCalls()) != 1 || call.ID == "" || len(args.Choices) != 0 || validateAskUser(args.AskUserArgs) != nil {
					return false, domain.ErrInvalid
				}
				op := fmt.Sprintf("%s:%d:%s", deps.TurnID, args.ExpectedRevision, call.ID)
				run, err := deps.Research.repo.PrepareCandidateReview(ctx, deps.Scope, deps.TurnID, op, args.ExpectedRevision, parsed.Recommendations)
				if err != nil {
					return false, err
				}
				state.Question = args.Question
				state.Interaction.Pending = schema.CloneMessages(messages)
				state.Interaction.ReturnNode = "agent_model"
				ids := make([]string, 0, len(run.Candidates))
				for _, candidate := range run.Candidates {
					ids = append(ids, candidate.ID)
				}
				state.Interaction.ResearchReview = &researchReviewPrompt{Kind: args.Kind, Question: args.Question, RunID: run.ID, Revision: run.CandidateRevision, Hash: run.CandidateHash, RequestRevision: args.ExpectedRevision, CandidateIDs: ids, AnalyzeLimit: run.Spec.Settings.AnalyzeLimit}
				return true, nil
			}
			if args.Kind == "" || args.Kind == "clarification" {
				// 旧校验只看原有字段，新 schema 仍保留旧澄清行为。
				raw, _ := json.Marshal(args.AskUserArgs)
				copy := schema.NewMessage(schema.RoleAssistant, schema.NewFunctionToolCallBlock(call.ID, "ask_user", string(raw)))
				asking, err := prepareInteraction(state, []schema.Message{copy}, "agent_model")
				if err == nil && asking {
					state.Interaction.Pending = schema.CloneMessages(messages)
					state.Interaction.ResearchReview = nil
				}
				return asking, err
			}
			if len(messages) != 1 || len(message.FunctionToolCalls()) != 1 || call.ID == "" || args.Kind != "research_plan" || args.ExpectedRevision < 1 || strings.TrimSpace(args.Question) == "" || len(args.Choices) != 0 {
				return false, domain.ErrInvalid
			}
			if err := validateAskUser(args.AskUserArgs); err != nil {
				return false, fmt.Errorf("%w：%v", domain.ErrInvalid, err)
			}
			run, err := deps.Research.ForTurn(ctx, deps.Scope, deps.TurnID)
			if err != nil {
				return false, err
			}
			if run.PlanRevision != args.ExpectedRevision || run.ApprovedPlanRevision != 0 || run.Phase == "cancelled" {
				return false, fmt.Errorf("%w：待审核计划版本已变化或已确认", domain.ErrConflict)
			}
			state.Question = args.Question
			state.Interaction.Pending = schema.CloneMessages(messages)
			state.Interaction.ReturnNode = "agent_model"
			state.Interaction.ResearchReview = &researchReviewPrompt{Kind: "research_plan", Question: args.Question, RunID: run.ID, Revision: run.PlanRevision, Hash: run.PlanHash, Plan: run.Plan}
			return true, nil
		}
	}
	return false, nil
}

func decodeResearchArgs(raw string, target any) error {
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("%w：%v", domain.ErrInvalid, err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return domain.ErrInvalid
	}
	return nil
}
