package domain

import "testing"

func TestEvidenceMatrixBindsContentRatherThanExecution(t *testing.T) {
	run := ResearchRun{ID: "run", SpecHash: "spec", PlanHash: "plan", ApprovedCandidateRevision: 2, WorkItems: []ResearchWorkItem{{ID: "w", PaperID: "p", State: "completed", AnalysisRevision: 1, AnalysisHash: "a", Analysis: &PaperAnalysisDraft{Summary: "仅摘要"}}}}
	first := BuildEvidenceMatrix(run)
	run.ModelCalls = 20
	run.ExecutionAttempt = 3
	run.WorkItems[0].Attempts = 9
	if BuildEvidenceMatrix(run).Hash != first.Hash {
		t.Fatal("执行统计改变矩阵内容版本")
	}
	run.WorkItems[0].AnalysisRevision++
	if BuildEvidenceMatrix(run).Hash == first.Hash {
		t.Fatal("分析版本改变未失效矩阵")
	}
}

func TestReportRequiresBoundClaimsAndAllSections(t *testing.T) {
	draft := ReportDraft{Title: "研究报告", Summary: "明确未知项", MatrixHash: "hash", Limitations: []string{"仅为演示"}}
	for _, section := range []string{"comparison", "conflicts", "gaps", "next_steps"} {
		draft.Claims = append(draft.Claims, ReportClaim{Section: section, Status: "unknown", Text: "资料不足", AnalysisRefs: []AnalysisReference{{WorkItemID: "w", AnalysisRevision: 1}}})
	}
	if err := ValidateReportDraft(draft); err != nil {
		t.Fatal(err)
	}
	draft.Claims[0].Status = "finding"
	if ValidateReportDraft(draft) == nil {
		t.Fatal("finding 无证据仍被接受")
	}
	draft.Claims[0].EvidenceIDs = []string{"e", "e"}
	if ValidateReportDraft(draft) == nil {
		t.Fatal("重复证据被接受")
	}
	draft.Claims = draft.Claims[1:]
	if ValidateReportDraft(draft) == nil {
		t.Fatal("缺少比较章节被接受")
	}
}
