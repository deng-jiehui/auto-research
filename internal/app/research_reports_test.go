package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/deng-jiehui/auto-research/internal/domain"
	"github.com/deng-jiehui/auto-research/internal/infra/mcp"
	"github.com/deng-jiehui/auto-research/internal/infra/sqlite"
	"github.com/goharness/goharness/pkg/llm"
	"github.com/goharness/goharness/pkg/schema"
	"github.com/goharness/goharness/pkg/tool"
)

func TestV8ResearchProducesBoundReportAndReadableResources(t *testing.T) {
	ctx := t.Context()
	s, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	service := analysisService(s, mcp.NewFixtureSource())
	scope, turn, run := approveAnalysisInputs(t, service, s, "autoresearch-agent-single-v8")
	if issue, err := service.ResearchService().ReportCompletionIssue(ctx, scope, turn.ID); err != nil || issue == "" {
		t.Fatalf("未完成分析可直接结束: %q %v", issue, err)
	}
	if _, err = service.RunQueued(ctx); err != nil {
		t.Fatal(err)
	}
	run, err = s.GetResearch(ctx, scope, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if run.ExecutionState != domain.TurnCompleted || run.Phase != "reported" || run.LatestReport == nil {
		t.Fatalf("研究未生成报告: %+v", run)
	}
	report, err := s.GetResearchReport(ctx, scope, run.LatestReport.ID)
	if err != nil {
		t.Fatal(err)
	}
	if report.MatrixHash != domain.BuildEvidenceMatrix(run).Hash || len(report.References) == 0 {
		t.Fatalf("报告未绑定当前矩阵和引用: %+v", report)
	}
	resources := &ProjectResources{Projects: s, Research: service.ResearchService(), Scope: scope}
	for _, path := range []string{"research://runs/" + run.ID + "/matrix.json", "research://runs/" + run.ID + "/reports.json", report.ResourcePath} {
		content, e := resources.Read(ctx, path, ReadFileArgs{})
		if e != nil || content.Content == "" {
			t.Fatalf("资料不可读取 %s: %v", path, e)
		}
	}
	if !strings.Contains(report.Markdown, "离线") || !strings.Contains(report.Markdown, "/papers?paper=") {
		t.Fatal("报告缺少演示标记或真实引用")
	}
}

func TestV8PrematureFinalAndReportFailureReuseAnalysis(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	s, err := sqlite.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	source := &partialPaperSource{PaperSource: mcp.NewFixtureSource(), store: s}
	source.unavailable.Store(true)
	service := analysisService(s, source)
	scope, turn, run := approveAnalysisInputs(t, service, s, "autoresearch-agent-single-v8")
	source.scope = scope
	source.turnID = turn.ID
	deps := ResearchGraphDeps{Scope: scope, TurnID: turn.ID, DefinitionVersion: "autoresearch-agent-single-v8", Research: service.ResearchService(), Academic: service.academic}
	fixture := newSingleFixtureModel(deps)
	premature, feedback, failed := false, false, false
	service.model = llm.NewHandlerModel(func(ctx context.Context, req llm.Request, _ llm.Options) ([]schema.Message, error) {
		if strings.Contains(req.Messages[0].JoinedText(""), "报告尚未开放") {
			return nil, errors.New("v8 系统指令仍含旧报告能力限制")
		}
		current, e := s.GetResearch(ctx, scope, run.ID)
		if e != nil {
			return nil, e
		}
		if current.Phase == "reporting" && !premature {
			premature = true
			return []schema.Message{schema.AssistantMessage("分析已结束。")}, nil
		}
		if premature && !failed && current.Phase == "reporting" {
			for _, message := range req.Messages {
				if strings.Contains(message.JoinedText(""), "报告尚未保存") {
					feedback = true
				}
			}
			failed = true
			return nil, errors.New("报告模型暂时不可用")
		}
		response, e := fixture.Invoke(ctx, req)
		return response.Messages, e
	})
	_, _ = service.RunQueued(ctx)
	after, err := s.GetResearch(ctx, scope, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !premature || !feedback || after.ExecutionState != domain.TurnFailed || after.LatestReport != nil || after.WorkItems[0].State != "completed" {
		t.Fatalf("提前结束或报告失败处理错误: %+v feedback=%v", after, feedback)
	}
	savedItem := after.WorkItems[0]
	failedItem := after.WorkItems[1]
	if failedItem.State != "failed" {
		t.Fatal("测试未进入部分失败场景")
	}
	current, _ := s.GetTurn(ctx, scope, turn.SessionID, turn.ID)
	if _, err = service.Retry(ctx, scope, turn.SessionID, turn.ID, TurnActionInput{Revision: current.Revision, IdempotencyKey: "retry-report"}); err != nil {
		t.Fatal(err)
	}
	// 重新打开数据库和服务，报告重试仍复用之前冻结的分析版本。
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = sqlite.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	source.store = s
	service = analysisService(s, source)
	if _, err = service.RunQueued(ctx); err != nil {
		t.Fatal(err)
	}
	after, err = s.GetResearch(ctx, scope, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.ExecutionState != domain.TurnCompleted || after.LatestReport == nil || after.WorkItems[0].AnalysisRevision != savedItem.AnalysisRevision || after.WorkItems[0].Attempts != savedItem.Attempts || after.ModelCalls <= run.ModelCalls {
		t.Fatalf("报告重试重复分析或重置预算: %+v", after)
	}
	if after.WorkItems[1].AnalysisRevision != failedItem.AnalysisRevision || after.WorkItems[1].Attempts != failedItem.Attempts {
		t.Fatal("只重试报告却重复执行失败篇")
	}
	report, err := s.GetResearchReport(ctx, scope, after.LatestReport.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = sqlite.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := s.GetResearchReport(ctx, scope, report.ID)
	if err != nil || restored.Markdown != report.Markdown || restored.MatrixHash != report.MatrixHash {
		t.Fatal("重启后报告正文或输入版本变化", err)
	}
}

func TestV8ReportToolReplayBeforeBudgetAndStrictArgs(t *testing.T) {
	ctx := t.Context()
	s, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	service := analysisService(s, mcp.NewFixtureSource())
	scope, turn, run := approveAnalysisInputs(t, service, s, "autoresearch-agent-single-v8")
	w := run.WorkItems[0]
	_, err = s.SaveResearchAnalysis(ctx, scope, turn.ID, "fail-analysis", w.Revision, domain.PaperAnalysisDraft{PaperID: w.PaperID, Status: "failed", Error: "无法取得来源"})
	if err != nil {
		t.Fatal(err)
	}
	run, _ = s.GetResearch(ctx, scope, run.ID)
	messages, err := reportFixtureResponse(run)
	if err != nil {
		t.Fatal(err)
	}
	raw := messages[0].FunctionToolCalls()[0].Arguments
	deps := ResearchGraphDeps{Scope: scope, TurnID: turn.ID, Research: service.ResearchService(), DefinitionVersion: "autoresearch-agent-single-v8"}
	wrapped := researchExecutionTool(reportResultTool(deps), deps)
	input := tool.CallInput{RawArgs: raw, IdempotencyKey: "stable-report"}
	first, err := wrapped.Invoke(ctx, input)
	if err != nil || first.IsError {
		t.Fatalf("报告保存失败: %+v %v", first, err)
	}
	for {
		_, e := s.ReserveResearchCall(ctx, scope, turn.ID, "tool")
		if errors.Is(e, domain.ErrBudgetExhausted) {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
	}
	replay, err := wrapped.Invoke(ctx, input)
	if err != nil || replay.IsError {
		t.Fatalf("报告重放被新预算拒绝: %+v %v", replay, err)
	}
	for _, invalid := range []string{strings.Replace(raw, "离线研究综合报告", "不同标题", 1), strings.TrimSuffix(raw, "}") + `,"unexpected":true}`} {
		changed := input
		changed.RawArgs = invalid
		result, e := wrapped.Invoke(ctx, changed)
		if e == nil && !result.IsError {
			t.Fatal("身份冲突或未知字段被接受")
		}
	}
	blocks, _ := replay.ToContentBlocks()
	text := schema.NewMessage(schema.RoleTool, blocks...).JoinedText("")
	var summary map[string]any
	if err = json.Unmarshal([]byte(text), &summary); err != nil {
		t.Fatal(err)
	}
	if summary["resource_path"] == nil || summary["markdown"] != nil || summary["claims"] != nil {
		t.Fatalf("工具结果泄露完整报告: %s", text)
	}
	versions, _ := s.ListResearchReports(ctx, scope, run.ID)
	after, _ := s.GetResearch(ctx, scope, run.ID)
	if len(versions) != 1 || after.ToolCalls != after.Spec.ToolCallLimit {
		t.Fatal("重放重复保存或消耗预算")
	}
}

func TestV7ResearchStillCompletesWithoutReport(t *testing.T) {
	s, err := sqlite.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	service := analysisService(s, mcp.NewFixtureSource())
	scope, _, run := approveAnalysisInputs(t, service, s, "autoresearch-agent-single-v7")
	if _, err = service.RunQueued(t.Context()); err != nil {
		t.Fatal(err)
	}
	run, _ = s.GetResearch(t.Context(), scope, run.ID)
	if run.Phase != "analyses_completed" || run.LatestReport != nil || run.ExecutionState != domain.TurnCompleted {
		t.Fatalf("旧版本结束语义改变: %+v", run)
	}
}
