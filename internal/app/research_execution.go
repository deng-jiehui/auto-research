package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/deng-jiehui/auto-research/internal/domain"
	"github.com/goharness/goharness/pkg/llm"
	"github.com/goharness/goharness/pkg/runnable"
	"github.com/goharness/goharness/pkg/schema"
	"github.com/goharness/goharness/pkg/tool"
)

const researchUsageSaveTimeout = 3 * time.Second

type researchOperationKey struct{}

// 执行预算适配只负责调用预留，模型观测仍沿用已有 callback；子 Agent 共用此预算。
type researchBudgetModel struct {
	llm.ChatModel
	service *ResearchService
	scope   domain.Scope
	turnID  string
}

func (m *researchBudgetModel) Stream(ctx context.Context, req llm.Request, opts ...runnable.Option) (*runnable.StreamReader[llm.Response], error) {
	id, err := m.service.repo.ReserveResearchCall(ctx, m.scope, m.turnID, "model")
	if err != nil {
		return nil, err
	}
	upstream, err := m.ChatModel.Stream(ctx, req, opts...)
	if err != nil {
		return nil, err
	}
	reader, writer := runnable.Pipe[llm.Response](ctx)
	go func() {
		defer upstream.Close()
		var usage *llm.Usage
		for {
			value, recvErr := upstream.Recv()
			if recvErr != nil {
				if usage != nil {
					saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), researchUsageSaveTimeout)
					saveErr := m.service.repo.RecordResearchUsage(saveCtx, m.scope, id, int64(usage.InputTokens), int64(usage.OutputTokens), true)
					cancel()
					if saveErr != nil {
						_ = writer.CloseWithError(saveErr)
						return
					}
				}
				if errors.Is(recvErr, io.EOF) {
					_ = writer.Close()
				} else {
					_ = writer.CloseWithError(recvErr)
				}
				return
			}
			if value.Usage != nil {
				copied := *value.Usage
				usage = &copied
			}
			if err := writer.Send(value); err != nil {
				if usage != nil {
					saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), researchUsageSaveTimeout)
					_ = m.service.repo.RecordResearchUsage(saveCtx, m.scope, id, int64(usage.InputTokens), int64(usage.OutputTokens), true)
					cancel()
				}
				_ = writer.CloseWithError(err)
				return
			}
		}
	}()
	return reader, nil
}
func (m *researchBudgetModel) Invoke(ctx context.Context, req llm.Request, opts ...runnable.Option) (llm.Response, error) {
	r, err := m.Stream(ctx, req, opts...)
	if err != nil {
		return llm.Response{}, err
	}
	return llm.CollectResponse(ctx, r)
}
func (m *researchBudgetModel) Collect(ctx context.Context, in *runnable.StreamReader[llm.Request], opts ...runnable.Option) (llm.Response, error) {
	return runnable.CollectFromInvoke(ctx, in, m.Invoke, opts...)
}
func (m *researchBudgetModel) Transform(ctx context.Context, in *runnable.StreamReader[llm.Request], opts ...runnable.Option) (*runnable.StreamReader[llm.Response], error) {
	return runnable.TransformFromInvoke(ctx, in, m.Invoke, opts...)
}

func researchExecutionTool(original tool.Tool, deps ResearchGraphDeps) tool.Tool {
	spec := original.Spec()
	if supportsCandidateReview(deps) {
		switch spec.Name {
		case "search_papers":
			spec.IsReadOnly = false
			spec.Description += " 正式调研中自动按当前 Run 合并、去重并保存候选，返回 candidates（含 candidate_id 对应的 id）、candidate_revision 和 candidate_hash；总数不超过任务候选上限，随后 ask_user(kind=research_candidates) 提交推荐并请求审核。普通聊天仍返回论文数组。"
		case "save_paper":
			spec.Description += " 正式调研中，用户确认的纳入论文已自动归档，此工具只返回已纳入论文的现有 PaperID；不解析或导入其他论文。"
		case "read_paper", "list_papers", "search_project_evidence", "save_evidence":
			spec.Description += " 正式调研须先完成候选审核，只能读取或引用用户纳入的论文。"
		}
	}
	return tool.NewFunctionTool(spec, func(ctx context.Context, input tool.CallInput) (tool.Result, error) {
		if err := researchAnalysisBoundary(ctx, deps); err != nil {
			return tool.Result{}, err
		}
		var run domain.ResearchRun
		var searchFingerprint string
		candidateFlow := supportsCandidateReview(deps)
		if candidateFlow {
			var err error
			run, err = deps.Research.ForTurn(ctx, deps.Scope, deps.TurnID)
			if err != nil && !errors.Is(err, domain.ErrNotFound) {
				return tool.Result{}, err
			}
			candidateFlow = err == nil
		}
		if candidateFlow && original.Spec().Name == "search_papers" {
			if input.IdempotencyKey == "" {
				return tool.ErrorResult(schema.NewTextBlock("检索操作缺少执行身份")), nil
			}
			searchFingerprint = hash([]byte(deps.TurnID + ":" + input.RawArgs))
			previous, err := deps.Research.repo.ResearchSearchReplay(ctx, deps.Scope, deps.TurnID, input.IdempotencyKey, searchFingerprint)
			if err == nil {
				return candidateSearchResult(previous), nil
			}
			if !errors.Is(err, domain.ErrNotFound) {
				return researchToolError(err)
			}
		}
		// 成功操作只返回已保存结果，不再占用预算或修改工作项版本。
		if candidateFlow && supportsPaperAnalysis(deps) && spec.Name == "submit_research_result" {
			var args analysisResultArgs
			if supportsResearchReport(deps) {
				var in reportResultArgs
				if err := decodeResearchArgs(input.RawArgs, &in); err != nil {
					return researchToolError(err)
				}
				if in.Kind == "report" {
					if in.Report == nil || in.Plan != nil || in.Direction != nil || in.PaperAnalysis != nil {
						return researchToolError(domain.ErrInvalid)
					}
					previous, err := deps.Research.repo.ResearchReportReplay(ctx, deps.Scope, deps.TurnID, input.IdempotencyKey, in.ExpectedRevision, *in.Report)
					if err == nil {
						return tool.DataResult(reportToolResult(previous)), nil
					}
					if !errors.Is(err, domain.ErrNotFound) {
						return researchToolError(err)
					}
				} else if in.Report != nil {
					return researchToolError(domain.ErrInvalid)
				}
				args = analysisResultArgs{Kind: in.Kind, Direction: in.Direction, Plan: in.Plan, PaperAnalysis: in.PaperAnalysis, ExpectedRevision: in.ExpectedRevision}
			} else if err := decodeResearchArgs(input.RawArgs, &args); err != nil {
				return researchToolError(err)
			}
			if args.Kind == "paper_analysis" {
				if args.PaperAnalysis == nil || args.Plan != nil || args.Direction != nil {
					return researchToolError(domain.ErrInvalid)
				}
				previous, err := deps.Research.repo.ResearchAnalysisReplay(ctx, deps.Scope, deps.TurnID, input.IdempotencyKey, args.ExpectedRevision, *args.PaperAnalysis)
				if err == nil {
					return tool.DataResult(analysisToolResult(previous)), nil
				}
				if !errors.Is(err, domain.ErrNotFound) {
					return researchToolError(err)
				}
			}
		}
		if err := deps.Research.Guard(ctx, deps.Scope, deps.TurnID, original.Spec().Name); err != nil {
			return tool.ErrorResult(schema.NewTextBlock(err.Error())), nil
		}
		if candidateFlow {
			if err := guardCandidateTool(ctx, deps, run, original.Spec().Name, input.RawArgs); err != nil {
				return researchToolError(err)
			}
		}
		if original.Spec().Name == "search_papers" {
			run, err := deps.Research.ForTurn(ctx, deps.Scope, deps.TurnID)
			if err == nil {
				var args AcademicQueryArgs
				if err = decodeResearchArgs(input.RawArgs, &args); err != nil {
					return tool.ErrorResult(schema.NewTextBlock(err.Error())), nil
				}
				args.YearFrom, args.YearTo = run.Spec.Settings.YearFrom, run.Spec.Settings.YearTo
				if args.Limit <= 0 {
					args.Limit = 5
				}
				args.Limit = min(args.Limit, run.Spec.Settings.CandidateLimit, 30)
				data, _ := json.Marshal(args)
				input.RawArgs = string(data)
				input.Args, _ = tool.ParseArgsStrict(input.RawArgs)
			} else if !errors.Is(err, domain.ErrNotFound) {
				return tool.Result{}, err
			}
		}
		if _, err := deps.Research.repo.ReserveResearchCall(ctx, deps.Scope, deps.TurnID, "tool"); err != nil {
			return tool.Result{}, err
		}
		// 框架调用键含运行和批次身份，恢复重放稳定；内容及 expected_revision 另存请求指纹。
		ctx = context.WithValue(ctx, researchOperationKey{}, input.IdempotencyKey)
		if candidateFlow && original.Spec().Name == "save_paper" {
			if deps.Academic.Papers == nil {
				return researchToolError(domain.ErrUnavailable)
			}
			var args PaperReferenceArgs
			if err := decodeResearchArgs(input.RawArgs, &args); err != nil {
				return researchToolError(err)
			}
			key, _ := domain.NormalizePaperKey(args.Reference)
			for _, c := range run.Candidates {
				if c.SourceKey != key {
					continue
				}
				for _, choice := range run.CandidateReview.Choices {
					if choice.CandidateID == c.ID && choice.Selected {
						paper, err := deps.Academic.Papers.Get(ctx, deps.Scope, choice.PaperID)
						if err != nil {
							return researchToolError(err)
						}
						return tool.DataResult(paper), nil
					}
				}
			}
			return researchToolError(fmt.Errorf("%w：此论文未被用户纳入", domain.ErrConflict))
		}
		if candidateFlow && original.Spec().Name == "search_project_evidence" {
			if deps.Academic.Documents == nil {
				return researchToolError(domain.ErrUnavailable)
			}
			var args EvidenceSearchArgs
			if err := decodeResearchArgs(input.RawArgs, &args); err != nil {
				return researchToolError(err)
			}
			ids := make([]string, 0)
			for _, choice := range run.CandidateReview.Choices {
				if choice.Selected {
					ids = append(ids, choice.PaperID)
				}
			}
			hits, err := deps.Academic.Documents.SearchForPapers(ctx, deps.Scope, args.Query, args.Limit, ids)
			if err != nil {
				return researchToolError(err)
			}
			return tool.DataResult(hits), nil
		}
		if candidateFlow && original.Spec().Name == "list_papers" {
			var args AcademicQueryArgs
			if err := decodeResearchArgs(input.RawArgs, &args); err != nil {
				return researchToolError(err)
			}
			papers, err := selectedResearchPapers(ctx, deps.Scope, deps.Academic.Papers, run, args.query())
			if err != nil {
				return researchToolError(err)
			}
			return tool.DataResult(papers), nil
		}
		if candidateFlow && supportsPaperAnalysis(deps) && original.Spec().Name == "read_paper" && deps.Academic.Documents != nil {
			return executeResearchRead(ctx, deps, original, input, run)
		}
		result, err := original.Invoke(ctx, input)
		if err != nil || result.IsError || !candidateFlow || original.Spec().Name != "search_papers" {
			return result, err
		}
		blocks, err := result.ToContentBlocks()
		if err != nil {
			return tool.Result{}, err
		}
		var candidates []domain.Candidate
		if err = json.Unmarshal([]byte(schema.NewMessage(schema.RoleTool, blocks...).JoinedText("")), &candidates); err != nil {
			return tool.Result{}, err
		}
		saved, err := deps.Research.repo.SaveResearchCandidates(ctx, deps.Scope, deps.TurnID, input.IdempotencyKey, searchFingerprint, candidates)
		if err != nil {
			return researchToolError(err)
		}
		return candidateSearchResult(saved), nil
	})
}

func supportsCandidateReview(deps ResearchGraphDeps) bool {
	return deps.Research != nil && (deps.DefinitionVersion == "" || strings.HasSuffix(deps.DefinitionVersion, "-v5") || strings.HasSuffix(deps.DefinitionVersion, "-v6") || (strings.HasSuffix(deps.DefinitionVersion, "-v7") || strings.HasSuffix(deps.DefinitionVersion, "-v8")))
}

func researchToolError(err error) (tool.Result, error) {
	if errors.Is(err, domain.ErrInvalid) || errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrConflict) {
		return tool.ErrorResult(schema.NewTextBlock(err.Error())), nil
	}
	return tool.Result{}, err
}

func candidateSearchResult(run domain.ResearchRun) tool.Result {
	return tool.DataResult(map[string]any{"run_id": run.ID, "candidate_revision": run.CandidateRevision, "candidate_hash": run.CandidateHash, "candidates": run.Candidates, "candidate_limit": run.Spec.Settings.CandidateLimit, "review_hint": "为当前全部候选提供推荐和理由，调用 ask_user(kind=research_candidates,expected_revision=candidate_revision,recommendations=[{candidate_id,recommended,reason}])。结果为空时也应请求用户重新检索或取消。"})
}

func guardCandidateTool(ctx context.Context, deps ResearchGraphDeps, run domain.ResearchRun, name, raw string) error {
	approved := run.ApprovedCandidateRevision > 0 && run.ApprovedCandidateRevision == run.CandidateRevision && run.CandidateReview != nil && run.CandidateReview.Decision == "approve"
	if name == "search_papers" && approved {
		return fmt.Errorf("%w：候选已冻结，请使用已选论文；改变范围需新建任务", domain.ErrConflict)
	}
	switch name {
	case "read_paper", "list_papers", "save_paper", "search_project_evidence", "save_evidence":
		if !approved {
			return fmt.Errorf("%w：candidates_not_approved，必须先检索并调用 ask_user(kind=research_candidates) 请求审核", domain.ErrConflict)
		}
	default:
		return nil
	}
	allowed := func(id string) bool {
		for _, choice := range run.CandidateReview.Choices {
			if choice.Selected && choice.PaperID == id {
				return true
			}
		}
		return false
	}
	if name == "read_paper" {
		var args ReadPaperDocumentArgs
		if err := decodeResearchArgs(raw, &args); err != nil {
			return err
		}
		if !allowed(args.PaperID) {
			return fmt.Errorf("%w：论文未被本任务纳入", domain.ErrConflict)
		}
	}
	if name == "save_evidence" {
		var args SaveEvidenceArgs
		if err := decodeResearchArgs(raw, &args); err != nil {
			return err
		}
		if deps.Academic.Documents == nil {
			return domain.ErrUnavailable
		}
		doc, err := deps.Academic.Documents.Contents(ctx, deps.Scope, args.DocumentID)
		if err != nil {
			return err
		}
		if !allowed(doc.PaperID) {
			return fmt.Errorf("%w：文档所属论文未被本任务纳入", domain.ErrConflict)
		}
	}
	return nil
}

func researchSelectedPaper(run domain.ResearchRun, paperID string) bool {
	if run.ApprovedCandidateRevision == 0 || run.ApprovedCandidateRevision != run.CandidateRevision || run.CandidateReview == nil || run.CandidateReview.Decision != "approve" || run.Phase == "cancelled" {
		return false
	}
	for _, choice := range run.CandidateReview.Choices {
		if choice.Selected && choice.PaperID == paperID {
			return true
		}
	}
	return false
}

// 按已批准身份读取，避免项目列表的分页上限漏掉较早归档的入选论文。
func selectedResearchPapers(ctx context.Context, scope domain.Scope, papers *PaperService, run domain.ResearchRun, query domain.PaperQuery) ([]domain.Paper, error) {
	if papers == nil {
		return nil, domain.ErrUnavailable
	}
	if query.Limit < 0 || query.Limit > 100 || query.YearFrom < 0 || query.YearTo < 0 || query.YearFrom > 0 && query.YearTo > 0 && query.YearFrom > query.YearTo {
		return nil, domain.ErrInvalid
	}
	limit := query.Limit
	if limit == 0 {
		limit = 50
	}
	result := make([]domain.Paper, 0)
	if run.CandidateReview == nil {
		return result, nil
	}
	for _, choice := range run.CandidateReview.Choices {
		if !choice.Selected || !researchSelectedPaper(run, choice.PaperID) {
			continue
		}
		paper, err := papers.Get(ctx, scope, choice.PaperID)
		if err != nil {
			return nil, err
		}
		if query.YearFrom > 0 && paper.Year < query.YearFrom || query.YearTo > 0 && paper.Year > query.YearTo {
			continue
		}
		if text := strings.ToLower(strings.TrimSpace(query.Text)); text != "" && !strings.Contains(strings.ToLower(paper.Title+" "+paper.Abstract+" "+paper.SourceKey), text) {
			continue
		}
		result = append(result, paper)
		if len(result) == limit {
			break
		}
	}
	return result, nil
}

func planResultTool(deps ResearchGraphDeps) tool.Tool {
	type args struct {
		Kind             string                 `json:"kind" jsonschema:"direction 或 plan；只提交草案，用户确认由 ask_user 和可信 API 完成"`
		Direction        *domain.DirectionDraft `json:"direction,omitempty"`
		Plan             *domain.ResearchPlan   `json:"plan,omitempty"`
		ExpectedRevision int64                  `json:"expected_revision,omitempty" jsonschema:"plan 当前版本；首次为 0，修改必须携带工具返回的最新版本"`
	}
	return newAcademicTool[args, ResearchResult]("submit_research_result", "提交研究成果：kind=direction 保存方向提案；kind=plan 保存当前调研计划，再调用 ask_user(kind=research_plan) 审核。二者内容字段只能出现一个。计划保存返回版本和资料地址；不能伪造批准状态。首次计划绑定当前 Turn，不创建新的消息或重复任务。", func(ctx context.Context, in args) (ResearchResult, error) {
		key, _ := ctx.Value(researchOperationKey{}).(string)
		if key == "" {
			return ResearchResult{}, domain.ErrInvalid
		}
		switch in.Kind {
		case "plan":
			if in.Plan == nil || in.Direction != nil {
				return ResearchResult{}, domain.ErrInvalid
			}
			run, err := deps.Research.repo.SaveResearchPlan(ctx, deps.Scope, deps.TurnID, key, in.ExpectedRevision, *in.Plan)
			if err != nil {
				return ResearchResult{}, err
			}
			return ResearchResult{Kind: "plan", ID: run.ID, Revision: run.PlanRevision, ResourcePath: "research://runs/" + run.ID + ".json"}, nil
		case "direction":
			if deps.Directions == nil || in.Direction == nil || in.Plan != nil || in.ExpectedRevision != 0 {
				return ResearchResult{}, domain.ErrInvalid
			}
			d, err := deps.Directions.Propose(ctx, deps.Scope, *in.Direction, deps.SessionID, deps.TurnID, "operation:"+key)
			if err != nil {
				return ResearchResult{}, err
			}
			return ResearchResult{Kind: "direction", ID: d.ID, Revision: d.Revision, ResourcePath: "research://directions/" + d.ID + ".json", Direction: &d}, nil
		default:
			return ResearchResult{}, domain.ErrInvalid
		}
	})
}
