package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/deng-jiehui/auto-research/internal/domain"
	"github.com/goharness/goharness/pkg/schema"
	"github.com/goharness/goharness/pkg/tool"
)

func supportsResearchReport(deps ResearchGraphDeps) bool {
	return deps.Research != nil && strings.HasSuffix(deps.DefinitionVersion, "-v8")
}

type reportResultArgs struct {
	Kind             string                     `json:"kind" jsonschema:"direction、plan、paper_analysis 或 report；每次只提交一种成果"`
	Direction        *domain.DirectionDraft     `json:"direction,omitempty"`
	Plan             *domain.ResearchPlan       `json:"plan,omitempty"`
	PaperAnalysis    *domain.PaperAnalysisDraft `json:"paper_analysis,omitempty"`
	Report           *domain.ReportDraft        `json:"report,omitempty"`
	ExpectedRevision int64                      `json:"expected_revision,omitempty" jsonschema:"plan 或 WorkItem 的当前版本；report 首次为 0，后续使用 latest_report.revision"`
}

func reportToolResult(r domain.ResearchReport) map[string]any {
	return map[string]any{"kind": "report", "id": r.ID, "revision": r.Revision, "summary": r.Summary, "matrix_hash": r.MatrixHash, "resource_path": r.ResourcePath, "task_url": "/p/" + r.ProjectID + "/research/" + r.RunID}
}

func reportResultTool(deps ResearchGraphDeps) tool.Tool {
	original := analysisResultTool(deps)
	return newAcademicTool[reportResultArgs, any]("submit_research_result", "保存 direction、plan、paper_analysis 或 report，一次一种。逐篇阅读与引用要求不变。全部论文 completed/failed 后，read_file(research://runs/<id>/matrix.json) 读取当前 hash 与分析版本，分页按 next 读取；提交 report 的 title/summary/matrix_hash/limitations/claims。结论必须覆盖 comparison、conflicts、gaps、next_steps，每条绑定本任务 analysis_refs，finding 必须附该分析已使用的真实 evidence_ids；未知不能冒充事实，仅摘要不能声称核实实验细节。代码校验原文并生成不可变 Markdown，返回摘要和成果地址；不接收自编 Markdown 链接。失败可修正草稿重试，成功论文无需重读。", func(ctx context.Context, in reportResultArgs) (any, error) {
		if in.Kind == "report" {
			if in.Report == nil || in.Plan != nil || in.Direction != nil || in.PaperAnalysis != nil {
				return nil, domain.ErrInvalid
			}
			operation, _ := ctx.Value(researchOperationKey{}).(string)
			saved, err := deps.Research.repo.SaveResearchReport(ctx, deps.Scope, deps.TurnID, operation, in.ExpectedRevision, *in.Report)
			if err != nil {
				return nil, err
			}
			return reportToolResult(saved), nil
		}
		if in.Report != nil {
			return nil, domain.ErrInvalid
		}
		raw, _ := json.Marshal(analysisResultArgs{Kind: in.Kind, Direction: in.Direction, Plan: in.Plan, PaperAnalysis: in.PaperAnalysis, ExpectedRevision: in.ExpectedRevision})
		result, err := original.Invoke(ctx, tool.CallInput{RawArgs: string(raw)})
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
		var saved any
		err = json.Unmarshal([]byte(text), &saved)
		return saved, err
	})
}

func (s *ResearchService) Matrix(ctx context.Context, scope domain.Scope, id string) (domain.EvidenceMatrix, error) {
	run, err := s.Get(ctx, scope, id)
	if err != nil {
		return domain.EvidenceMatrix{}, err
	}
	return domain.BuildEvidenceMatrix(run), nil
}
func (s *ResearchService) Reports(ctx context.Context, scope domain.Scope, id string) ([]domain.ResearchReport, error) {
	return s.repo.ListResearchReports(ctx, scope, id)
}
func (s *ResearchService) Report(ctx context.Context, scope domain.Scope, id string) (domain.ResearchReport, error) {
	return s.repo.GetResearchReport(ctx, scope, id)
}

func (s *ResearchService) ReportCompletionIssue(ctx context.Context, scope domain.Scope, turn string) (string, error) {
	run, err := s.ForTurn(ctx, scope, turn)
	if errors.Is(err, domain.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if run.Phase == "cancelled" {
		return "", nil
	}
	if run.LatestReport != nil && run.LatestReport.MatrixHash == domain.BuildEvidenceMatrix(run).Hash {
		return "", nil
	}
	return fmt.Sprintf("报告尚未保存或矩阵版本已变化。请读取 research://runs/%s/matrix.json 及分页，再 submit_research_result(kind=report) 综合比较、冲突未知、待验证空白和下一步实验。不要重做成功分析；报告须绑定当前 hash 和分析版本，无充分资料时标明 unknown。", run.ID), nil
}

const researchReportInstructions = "\n逐篇处理结束后不要直接结束：先 read_file(research://runs/<id>/matrix.json)，按 next 读取全部矩阵（含失败项），由你在当前上下文综合并 submit_research_result(kind=report)。report 包含 title、summary、matrix_hash、limitations、claims；每条 Claim 的 section 为 comparison/conflicts/gaps/next_steps，四类均须覆盖，status 为 finding/inference/unverified/unknown，analysis_refs=[{work_item_id,analysis_revision}]，evidence_ids 仅使用该分析已保存的引用。finding 必须有原文证据，实验建议和研究空白标注待验证，摘要不能升级为核实实验事实。首次 expected_revision=0，之后使用 latest_report.revision。程序校验版本和原文，报告保存成功才可结束；最终只给简短总结和报告资料地址。不要创建额外 planner/reader/writer 或重读成功论文。"

func reportFixtureResponse(run domain.ResearchRun) ([]schema.Message, error) {
	matrix := domain.BuildEvidenceMatrix(run)
	if run.LatestReport != nil && run.LatestReport.MatrixHash == matrix.Hash {
		return []schema.Message{schema.AssistantMessage("离线调研报告已保存，完成逐篇分析与综合。报告位置：" + run.LatestReport.ResourcePath + "。请在任务页查看矩阵、原文引用和 Markdown；演示内容需人工核实。")}, nil
	}
	draft := domain.ReportDraft{Title: "离线研究综合报告", Summary: "离线演示：逐篇处理已完成，已保存证据矩阵和报告；实验细节不足，需补读全文并人工核实。", MatrixHash: matrix.Hash, Limitations: []string{"离线 fixture 演示，不代表真实调研质量；未运行任何实验。"}}
	refs := []domain.AnalysisReference{}
	for _, row := range matrix.Rows {
		refs = append(refs, domain.AnalysisReference{WorkItemID: row.WorkItemID, AnalysisRevision: row.AnalysisRevision})
	}
	for _, section := range []string{"comparison", "conflicts", "gaps", "next_steps"} {
		claim := domain.ReportClaim{Section: section, Status: "unknown", Text: "离线资料不足以确认，需要补读原文与后续实验验证。", AnalysisRefs: refs}
		if section == "comparison" {
			for _, row := range matrix.Rows {
				if row.Analysis != nil {
					for _, field := range row.Analysis.Fields {
						if field.Status == "finding" && len(field.EvidenceIDs) > 0 {
							claim.Status = "finding"
							claim.AnalysisRefs = []domain.AnalysisReference{{WorkItemID: row.WorkItemID, AnalysisRevision: row.AnalysisRevision}}
							claim.Text = "离线资料记录的工作流程，具体语义需人工核实。"
							claim.EvidenceIDs = append(claim.EvidenceIDs, field.EvidenceIDs...)
							break
						}
					}
				}
				if len(claim.EvidenceIDs) > 0 {
					break
				}
			}
		}
		draft.Claims = append(draft.Claims, claim)
	}
	revision := int64(0)
	if run.LatestReport != nil {
		revision = run.LatestReport.Revision
	}
	raw, err := json.Marshal(reportResultArgs{Kind: "report", Report: &draft, ExpectedRevision: revision})
	return []schema.Message{schema.NewMessage(schema.RoleAssistant, schema.NewFunctionToolCallBlock(fmt.Sprintf("fixture-report-%d", run.ModelCalls), "submit_research_result", string(raw)))}, err
}
