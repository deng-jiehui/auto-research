package sqlite

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/deng-jiehui/auto-research/internal/domain"
)

func reportDraft(run domain.ResearchRun) domain.ReportDraft {
	d := domain.ReportDraft{Title: "可核验研究报告", Summary: "当前实验信息不足，需补读全文。", MatrixHash: domain.BuildEvidenceMatrix(run).Hash, Limitations: []string{"只依据当前冻结资料，未运行实验"}}
	for _, section := range []string{"comparison", "conflicts", "gaps", "next_steps"} {
		d.Claims = append(d.Claims, domain.ReportClaim{Section: section, Status: "unknown", Text: "尚无充分证据，需要后续核实", AnalysisRefs: []domain.AnalysisReference{{WorkItemID: run.WorkItems[0].ID, AnalysisRevision: run.WorkItems[0].AnalysisRevision}}})
	}
	return d
}

func TestReportValidatesOriginalEvidenceAndScope(t *testing.T) {
	s, scope, turn, run, doc := analysisFixture(t)
	ctx := t.Context()
	w, err := s.BeginResearchPaper(ctx, scope, turn.ID, doc.PaperID, doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	e, err := s.SaveEvidence(ctx, scope, domain.Evidence{ProjectID: scope.ProjectID, PaperID: doc.PaperID, DocumentID: doc.ID, BlockID: "b1", Quote: doc.Text, StartRune: 0, EndRune: len([]rune(doc.Text)), Field: "method", Value: "公开基线"})
	if err != nil {
		t.Fatal(err)
	}
	analysis := unknownAnalysis(doc)
	analysis.Fields[0] = domain.AnalysisField{Name: "method", Status: "finding", Text: "公开基线", EvidenceIDs: []string{e.ID}}
	if _, err = s.SaveResearchAnalysis(ctx, scope, turn.ID, "analysis", w.Revision, analysis); err != nil {
		t.Fatal(err)
	}
	run, _ = s.GetResearch(ctx, scope, run.ID)
	draft := reportDraft(run)
	draft.Claims[0].Status = "finding"
	draft.Claims[0].EvidenceIDs = []string{e.ID}
	draft.Claims[0].AnalysisRefs[0].AnalysisRevision++
	if _, err = s.SaveResearchReport(ctx, scope, turn.ID, "old-analysis", 0, draft); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("错误分析版本未拒绝: %v", err)
	}
	draft.Claims[0].AnalysisRefs[0].AnalysisRevision--
	if _, err = s.db.ExecContext(ctx, `UPDATE evidence SET quote='伪造原文' WHERE id=?`, e.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveResearchReport(ctx, scope, turn.ID, "tampered", 0, draft); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("篡改引用未拒绝: %v", err)
	}
	if _, err = s.db.ExecContext(ctx, `UPDATE evidence SET quote=? WHERE id=?`, e.Quote, e.ID); err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateProject(ctx, scope.UserID, "另一个项目")
	if err != nil {
		t.Fatal(err)
	}
	foreignScope := domain.Scope{UserID: scope.UserID, ProjectID: other.ID}
	saved, err := s.SaveResearchReport(ctx, scope, turn.ID, "valid", 0, draft)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.References) != 1 || saved.References[0].Quote != doc.Text {
		t.Fatal("报告未快照实际原文")
	}
	if _, err = s.GetResearchReport(ctx, foreignScope, saved.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("跨项目读取报告未拒绝: %v", err)
	}
	if _, err = s.ListResearchReports(ctx, foreignScope, run.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("跨项目读取目录未拒绝: %v", err)
	}
	if _, err = s.SaveResearchReport(ctx, foreignScope, turn.ID, "foreign", 0, draft); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("跨项目提交报告未拒绝: %v", err)
	}
	if _, err = s.db.ExecContext(ctx, `UPDATE evidence SET project_id=? WHERE id=?`, other.ID, e.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveResearchReport(ctx, scope, turn.ID, "foreign-evidence", 1, draft); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("跨项目证据未拒绝: %v", err)
	}
}

func TestReportRemainsImmutableAfterFailedPaperRetry(t *testing.T) {
	s, scope, turn, run, doc := analysisFixture(t)
	ctx := t.Context()
	w, err := s.BeginResearchPaper(ctx, scope, turn.ID, doc.PaperID, doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	failed, err := s.SaveResearchAnalysis(ctx, scope, turn.ID, "failed", w.Revision, domain.PaperAnalysisDraft{PaperID: doc.PaperID, DocumentID: doc.ID, Status: "failed", Error: "来源暂时不可读"})
	if err != nil {
		t.Fatal(err)
	}
	run, _ = s.GetResearch(ctx, scope, run.ID)
	report, err := s.SaveResearchReport(ctx, scope, turn.ID, "report", 0, reportDraft(run))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetTurnState(ctx, scope, turn.ID, domain.TurnCompleted, "", ""); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveMessages(ctx, scope, turn.SessionID, 1, []domain.MessageRecord{{TurnID: turn.ID, ContentJSON: []byte(`{"role":"user","content":"研究问题"}`), Committed: true}}); err != nil {
		t.Fatal(err)
	}
	current, _ := s.GetTurn(ctx, scope, turn.SessionID, turn.ID)
	payload, _ := json.Marshal(map[string]any{"paper_id": doc.PaperID, "work_revision": failed.Revision})
	if _, err = s.ApplyAction(ctx, scope, turn.SessionID, turn.ID, domain.ActionDraft{Kind: "research_retry", IdempotencyKey: "retry", Revision: current.Revision, RequestHash: hashBytes(payload), PayloadJSON: payload}); err != nil {
		t.Fatal(err)
	}
	run, _ = s.GetResearch(ctx, scope, run.ID)
	if domain.BuildEvidenceMatrix(run).Hash == report.MatrixHash || run.LatestReport == nil || run.LatestReport.ID != report.ID {
		t.Fatal("重试未失效当前矩阵或丢弃历史报告")
	}
	old, err := s.GetResearchReport(ctx, scope, report.ID)
	if err != nil || old.Markdown != report.Markdown || old.Matrix.Rows[0].State != "failed" {
		t.Fatal("单篇重试覆盖原报告", err)
	}
	if _, err = s.SaveResearchAnalysis(ctx, scope, turn.ID, "recovered", run.WorkItems[0].Revision, unknownAnalysis(doc)); err != nil {
		t.Fatal(err)
	}
	run, _ = s.GetResearch(ctx, scope, run.ID)
	stale := reportDraft(run)
	stale.MatrixHash = report.MatrixHash
	if _, err = s.SaveResearchReport(ctx, scope, turn.ID, "stale-report", 1, stale); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("历史输入报告未被拒绝: %v", err)
	}
	next, err := s.SaveResearchReport(ctx, scope, turn.ID, "new-report", 1, reportDraft(run))
	if err != nil || next.Revision != 2 || next.MatrixHash == old.MatrixHash {
		t.Fatalf("分析变化后未保存新报告版本: %+v %v", next, err)
	}
}

func TestResearchReportValidationReplayAndImmutableArtifact(t *testing.T) {
	s, scope, turn, run, doc := analysisFixture(t)
	ctx := t.Context()
	draft := reportDraft(run)
	if _, err := s.SaveResearchReport(ctx, scope, turn.ID, "early", 0, draft); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("过早报告未拒绝: %v", err)
	}
	w, err := s.BeginResearchPaper(ctx, scope, turn.ID, doc.PaperID, doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveResearchAnalysis(ctx, scope, turn.ID, "analysis", w.Revision, unknownAnalysis(doc)); err != nil {
		t.Fatal(err)
	}
	run, _ = s.GetResearch(ctx, scope, run.ID)
	draft = reportDraft(run)
	bad := reportDraft(run)
	bad.MatrixHash = "stale"
	if _, err = s.SaveResearchReport(ctx, scope, turn.ID, "stale", 0, bad); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("旧矩阵未拒绝: %v", err)
	}
	bad = reportDraft(run)
	bad.Claims[0].AnalysisRefs[0].WorkItemID = "other-run"
	if _, err = s.SaveResearchReport(ctx, scope, turn.ID, "foreign", 0, bad); err == nil {
		t.Fatal("跨任务分析引用被接受")
	}
	bad = reportDraft(run)
	bad.Claims[0].Status = "finding"
	bad.Claims[0].EvidenceIDs = []string{"fabricated"}
	if _, err = s.SaveResearchReport(ctx, scope, turn.ID, "fake", 0, bad); err == nil {
		t.Fatal("伪造引用被接受")
	}
	_, err = s.db.ExecContext(ctx, `CREATE TRIGGER fail_report BEFORE INSERT ON research_reports BEGIN SELECT RAISE(ABORT,'injected'); END`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveResearchReport(ctx, scope, turn.ID, "save", 0, draft); err == nil {
		t.Fatal("事务故障未传播")
	}
	var n int
	_ = s.db.QueryRowContext(ctx, `SELECT count(*) FROM events WHERE kind='research.report'`).Scan(&n)
	if n != 0 {
		t.Fatal("报告失败留下成功事件")
	}
	_ = s.db.QueryRowContext(ctx, `SELECT count(*) FROM research_reports`).Scan(&n)
	if n != 0 {
		t.Fatal("报告失败留下元数据")
	}
	_, _ = s.db.ExecContext(ctx, `DROP TRIGGER fail_report`)
	saved, err := s.SaveResearchReport(ctx, scope, turn.ID, "save", 0, draft)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(saved.Markdown, "未知") || saved.ArtifactID == "" || saved.Revision != 1 {
		t.Fatalf("缺少报告正文或版本: %+v", saved)
	}
	replayed, err := s.SaveResearchReport(ctx, scope, turn.ID, "save", 0, draft)
	if err != nil || replayed.ID != saved.ID {
		t.Fatalf("提交重放失败: %v", err)
	}
	draft.Summary = "不同内容"
	if _, err = s.SaveResearchReport(ctx, scope, turn.ID, "save", 0, draft); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("同身份换内容未冲突: %v", err)
	}
	if _, err = s.GetResearchReport(ctx, domain.Scope{UserID: "other", ProjectID: scope.ProjectID}, saved.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("跨用户报告未拒绝: %v", err)
	}
	list, _ := s.ListResearchReports(ctx, scope, run.ID)
	if len(list) != 1 {
		t.Fatalf("重放新增报告: %d", len(list))
	}
	loaded, err := s.GetResearchReport(ctx, scope, saved.ID)
	if err != nil || loaded.Markdown != saved.Markdown {
		t.Fatal("持久正文与原版本不一致", err)
	}
	if _, err = s.SaveResearchReport(ctx, scope, turn.ID, "new", 0, draft); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("旧报告版本未拒绝: %v", err)
	}
	second, err := s.SaveResearchReport(ctx, scope, turn.ID, "new", 1, draft)
	if err != nil || second.Revision != 2 {
		t.Fatalf("新版本未新增: %+v %v", second, err)
	}
	loaded, _ = s.GetResearchReport(ctx, scope, saved.ID)
	if loaded.Markdown != saved.Markdown {
		t.Fatal("新版本覆盖了旧正文")
	}
}
