package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/deng-jiehui/auto-research/internal/domain"
	"github.com/goharness/goharness/pkg/graph"
	"github.com/goharness/goharness/pkg/schema"
	"github.com/goharness/goharness/pkg/tool"
)

func supportsPaperAnalysis(deps ResearchGraphDeps) bool {
	return deps.Research != nil && (deps.DefinitionVersion == "" || (strings.HasSuffix(deps.DefinitionVersion, "-v6") || (strings.HasSuffix(deps.DefinitionVersion, "-v7") || strings.HasSuffix(deps.DefinitionVersion, "-v8"))))
}

type researchSegmentDeadlineKey struct{}

type analysisResultArgs struct {
	Kind             string                     `json:"kind" jsonschema:"direction、plan 或 paper_analysis；每次只提交一种成果"`
	Direction        *domain.DirectionDraft     `json:"direction,omitempty"`
	Plan             *domain.ResearchPlan       `json:"plan,omitempty"`
	PaperAnalysis    *domain.PaperAnalysisDraft `json:"paper_analysis,omitempty"`
	ExpectedRevision int64                      `json:"expected_revision,omitempty" jsonschema:"plan 当前版本或 WorkItem.revision；从工具返回或 read_file 任务资料读取"`
}

func analysisToolResult(w domain.ResearchWorkItem) map[string]any {
	return map[string]any{"kind": "paper_analysis", "id": w.ID, "revision": w.Revision, "work_item": w, "resource_path": "research://runs/" + w.RunID + ".json"}
}

func analysisResultTool(deps ResearchGraphDeps) tool.Tool {
	original := planResultTool(deps)
	return newAcademicTool[analysisResultArgs, any]("submit_research_result", "保存结构化成果：direction 保存待确认方向；plan 保存待审核计划；paper_analysis 保存一篇已纳入论文的分析或失败原因。先 read_paper 阅读冻结文档，save_evidence 保存引用，再提交 method/datasets/metrics/code/reproduction 五字段；明确 finding/inference/unverified/unknown。finding 必须有同文档真实 EvidenceID；仅摘要不能声称核实了实验细节。成功项不可覆盖，失败项只由用户重试。读取已有结果复用 read_file(research://runs/<id>.json)。矩阵和正式报告尚未开放。", func(ctx context.Context, in analysisResultArgs) (any, error) {
		if in.Kind != "paper_analysis" {
			if in.PaperAnalysis != nil {
				return nil, domain.ErrInvalid
			}
			raw, _ := json.Marshal(in)
			result, err := original.Invoke(ctx, tool.CallInput{RawArgs: string(raw)})
			// 旧 schema 不接受新字段，即使字段为空也不能传递。
			if err != nil {
				return nil, err
			}
			blocks, err := result.ToContentBlocks()
			if err != nil {
				return nil, err
			}
			text := schema.NewMessage(schema.RoleTool, blocks...).JoinedText("")
			if result.IsError {
				return nil, fmt.Errorf("%w：%s", domain.ErrInvalid, text)
			}
			var saved ResearchResult
			if err = json.Unmarshal([]byte(text), &saved); err != nil {
				return nil, err
			}
			return saved, nil
		}
		if in.PaperAnalysis == nil || in.Plan != nil || in.Direction != nil {
			return nil, domain.ErrInvalid
		}
		operation, _ := ctx.Value(researchOperationKey{}).(string)
		w, err := deps.Research.repo.SaveResearchAnalysis(ctx, deps.Scope, deps.TurnID, operation, in.ExpectedRevision, *in.PaperAnalysis)
		if err != nil {
			return nil, err
		}
		return analysisToolResult(w), nil
	})
}

func (s *ResearchService) AnalysisCompletionIssue(ctx context.Context, scope domain.Scope, turnID string) (string, error) {
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
	for _, w := range run.WorkItems {
		if w.State != "completed" && w.State != "failed" {
			return fmt.Sprintf("仍有论文尚未分析：%s。请 read_paper 按片段阅读，save_evidence 核对原文，再 submit_research_result(kind=paper_analysis,expected_revision=WorkItem.revision)。无法分析时明确保存 failed 及原因，继续其他论文。当前资料 research://runs/%s.json", w.Title, run.ID), nil
		}
	}
	if len(run.WorkItems) == 0 {
		return "尚未创建纳入论文的分析项，请先完成候选审核。", nil
	}
	return "", nil
}

// 中断只发生在模型/工具调用之间，不取消已经发出的外部请求。
func researchAnalysisBoundary(ctx context.Context, deps ResearchGraphDeps) error {
	if !supportsPaperAnalysis(deps) {
		return nil
	}
	run, err := deps.Research.ForTurn(ctx, deps.Scope, deps.TurnID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if run.ExecutionState == domain.TurnCancelled || run.CancelRequested {
		return fmt.Errorf("%w：用户已取消执行", domain.ErrConflict)
	}
	if deadline, ok := ctx.Value(researchSegmentDeadlineKey{}).(time.Time); ok && !time.Now().Before(deadline) && !run.PauseRequested && (run.Phase == "analyzing" || run.Phase == "reporting") {
		if err = deps.Research.repo.SetResearchPause(ctx, deps.Scope, deps.TurnID, true); err != nil {
			return err
		}
		run.PauseRequested = true
	}
	if !run.PauseRequested {
		return nil
	}
	value, err := graph.Interrupt(ctx, map[string]any{"kind": "research_pause", "run_id": run.ID, "question": "调研已暂停，已保存的逐篇结果和累计预算会保留。"})
	if err != nil {
		return err
	}
	if value != "continue" {
		return domain.ErrInvalid
	}
	return deps.Research.repo.SetResearchPause(ctx, deps.Scope, deps.TurnID, false)
}

func executeResearchRead(ctx context.Context, deps ResearchGraphDeps, original tool.Tool, input tool.CallInput, run domain.ResearchRun) (tool.Result, error) {
	var args ReadPaperDocumentArgs
	if err := decodeResearchArgs(input.RawArgs, &args); err != nil {
		return researchToolError(err)
	}
	var work domain.ResearchWorkItem
	for _, w := range run.WorkItems {
		if w.PaperID == args.PaperID {
			work = w
		}
	}
	if work.ID == "" {
		return researchToolError(domain.ErrConflict)
	}
	if args.Refresh {
		return researchToolError(fmt.Errorf("%w：调研文档版本冻结，刷新来源请新建任务", domain.ErrConflict))
	}
	if work.DocumentID != "" {
		if args.DocumentID != "" && args.DocumentID != work.DocumentID {
			return researchToolError(domain.ErrConflict)
		}
		if args.Section == "" || args.Section == "abstract" || args.Section == "fulltext" {
			doc, err := deps.Academic.Documents.repo.GetDocument(ctx, deps.Scope, work.DocumentID)
			if err != nil {
				return researchToolError(err)
			}
			if doc.ContentKind != "abstract" {
				doc.Text = ""
				for i := range doc.Blocks {
					doc.Blocks[i].Text = ""
				}
			}
			if _, err = deps.Research.repo.BeginResearchPaper(ctx, deps.Scope, deps.TurnID, args.PaperID, doc.ID); err != nil {
				return researchToolError(err)
			}
			return tool.DataResult(doc), nil
		}
	}
	if _, err := deps.Research.repo.BeginResearchPaper(ctx, deps.Scope, deps.TurnID, args.PaperID, ""); err != nil {
		return researchToolError(err)
	}
	result, err := original.Invoke(ctx, input)
	if err != nil || result.IsError {
		return result, err
	}
	if args.Section == "contents" || args.Section == "block" {
		if _, err = deps.Research.repo.BeginResearchPaper(ctx, deps.Scope, deps.TurnID, args.PaperID, args.DocumentID); err != nil {
			return researchToolError(err)
		}
		return result, nil
	}
	blocks, err := result.ToContentBlocks()
	if err != nil {
		return tool.Result{}, err
	}
	var doc domain.SourceDocument
	if err = json.Unmarshal([]byte(schema.NewMessage(schema.RoleTool, blocks...).JoinedText("")), &doc); err != nil {
		return tool.Result{}, err
	}
	if _, err = deps.Research.repo.BeginResearchPaper(ctx, deps.Scope, deps.TurnID, args.PaperID, doc.ID); err != nil {
		return researchToolError(err)
	}
	return result, nil
}
