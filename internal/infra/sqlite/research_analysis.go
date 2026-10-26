package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/deng-jiehui/auto-research/internal/domain"
)

func createResearchWorkItems(ctx context.Context, tx *sql.Tx, run domain.ResearchRun, review domain.ResearchCandidateReview) error {
	for _, choice := range review.Choices {
		if !choice.Selected {
			continue
		}
		title := ""
		for _, c := range run.Candidates {
			if c.ID == choice.CandidateID {
				title = c.Title
			}
		}
		w := domain.ResearchWorkItem{ID: randomID(), RunID: run.ID, PaperID: choice.PaperID, Title: title, SpecHash: run.SpecHash, State: "pending", Revision: 1}
		if err := putWorkItem(ctx, tx, &w); err != nil {
			return err
		}
	}
	return nil
}
func putWorkItem(ctx context.Context, tx *sql.Tx, w *domain.ResearchWorkItem) error {
	identity, _ := json.Marshal([]string{w.RunID, w.PaperID, w.DocumentID, w.SpecHash})
	w.IdentityHash = hashBytes(identity)
	data, err := json.Marshal(w)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO research_work_items(id,run_id,paper_id,content_json) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET content_json=excluded.content_json`, w.ID, w.RunID, w.PaperID, string(data))
	return dbError(err)
}
func findWorkItem(run domain.ResearchRun, paperID string) (domain.ResearchWorkItem, error) {
	for _, w := range run.WorkItems {
		if w.PaperID == paperID {
			return w, nil
		}
	}
	return domain.ResearchWorkItem{}, fmt.Errorf("%w：此论文未被纳入当前分析任务", domain.ErrConflict)
}
func writableResearch(ctx context.Context, tx *sql.Tx, scope domain.Scope, turnID string) (domain.ResearchRun, error) {
	run, err := researchForTurn(ctx, tx, scope, turnID)
	if err != nil {
		return run, err
	}
	if run.CancelRequested || run.ExecutionState != domain.TurnRunning && run.ExecutionState != domain.TurnQueued {
		return run, domain.ErrConflict
	}
	if run.Phase == "cancelled" || run.CandidateReview == nil || run.CandidateReview.Decision != "approve" {
		return run, domain.ErrConflict
	}
	return run, nil
}

// 读取开始即显示进度，首次成功读取后绑定文档；内容冻结不随重试改变。
func (s *Store) BeginResearchPaper(ctx context.Context, scope domain.Scope, turnID, paperID, documentID string) (domain.ResearchWorkItem, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.ResearchWorkItem{}, err
	}
	defer tx.Rollback()
	run, err := writableResearch(ctx, tx, scope, turnID)
	if err != nil {
		return domain.ResearchWorkItem{}, err
	}
	w, err := findWorkItem(run, paperID)
	if err != nil {
		return w, err
	}
	if w.State == "completed" || w.State == "failed" {
		return w, nil
	}
	for _, other := range run.WorkItems {
		if other.ID != w.ID && other.State == "reading" {
			return w, fmt.Errorf("%w：请先完成正在阅读的论文", domain.ErrConflict)
		}
	}
	changed := false
	if w.State == "pending" {
		w.State = "reading"
		w.Attempts++
		w.AttemptID = randomID()
		changed = true
	}
	if documentID != "" {
		var paper, kind string
		if err = tx.QueryRowContext(ctx, `SELECT paper_id,content_kind FROM source_documents WHERE id=? AND owner_id=? AND project_id=?`, documentID, scope.UserID, scope.ProjectID).Scan(&paper, &kind); err != nil {
			return w, dbError(err)
		}
		if paper != paperID || w.DocumentID != "" && w.DocumentID != documentID {
			return w, fmt.Errorf("%w：分析文档已冻结，不能换用来源版本", domain.ErrConflict)
		}
		if w.DocumentID == "" {
			w.DocumentID = documentID
			w.ContentKind = kind
			changed = true
		}
	}
	if changed {
		w.Revision++
		if err = putWorkItem(ctx, tx, &w); err != nil {
			return w, err
		}
		if err = analysisProgressEvent(ctx, tx, scope, run, w); err != nil {
			return w, err
		}
	}
	return w, dbError(tx.Commit())
}
func analysisFingerprint(turnID string, expected int64, draft domain.PaperAnalysisDraft) string {
	req, _ := json.Marshal(struct {
		Turn     string
		Expected int64
		Draft    domain.PaperAnalysisDraft
	}{turnID, expected, draft})
	return hashBytes(req)
}

func (s *Store) ResearchAnalysisReplay(ctx context.Context, scope domain.Scope, turnID, operation string, expected int64, draft domain.PaperAnalysisDraft) (domain.ResearchWorkItem, error) {
	if operation == "" || len(operation) > 1024 {
		return domain.ResearchWorkItem{}, domain.ErrInvalid
	}
	run, err := researchOperation(ctx, s.db, scope, turnID, "analysis:"+operation, analysisFingerprint(turnID, expected, draft))
	if err != nil {
		return domain.ResearchWorkItem{}, err
	}
	return findWorkItem(run, draft.PaperID)
}

func (s *Store) SaveResearchAnalysis(ctx context.Context, scope domain.Scope, turnID, operation string, expected int64, draft domain.PaperAnalysisDraft) (domain.ResearchWorkItem, error) {
	fingerprint := analysisFingerprint(turnID, expected, draft)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.ResearchWorkItem{}, err
	}
	defer tx.Rollback()
	previous, err := researchOperation(ctx, tx, scope, turnID, "analysis:"+operation, fingerprint)
	if err == nil {
		return findWorkItem(previous, draft.PaperID)
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return domain.ResearchWorkItem{}, err
	}
	if operation == "" || len(operation) > 1024 {
		return domain.ResearchWorkItem{}, domain.ErrInvalid
	}
	run, err := writableResearch(ctx, tx, scope, turnID)
	if err != nil {
		return domain.ResearchWorkItem{}, err
	}
	w, err := findWorkItem(run, draft.PaperID)
	if err != nil {
		return w, err
	}
	if w.Revision != expected || w.State == "completed" || w.State == "failed" {
		return w, fmt.Errorf("%w：工作项版本已变化，成功结果不可覆盖；失败项需由用户重试", domain.ErrConflict)
	}
	if err = domain.ValidatePaperAnalysis(draft); err != nil {
		return w, err
	}
	if draft.DocumentID != w.DocumentID {
		return w, fmt.Errorf("%w：请先 read_paper 成功阅读并冻结本次文档", domain.ErrConflict)
	}
	if draft.Status == "completed" {
		doc, scanErr := scanDocument(tx.QueryRowContext(ctx, "SELECT "+documentColumns+" FROM source_documents WHERE id=? AND owner_id=? AND project_id=?", draft.DocumentID, scope.UserID, scope.ProjectID))
		doc, err = s.hydrateDocument(ctx, scope, doc, scanErr)
		if err != nil {
			return w, err
		}
		if doc.PaperID != w.PaperID {
			return w, domain.ErrInvalid
		}
		for _, field := range draft.Fields {
			if doc.ContentKind == "abstract" && field.Status == "finding" && field.Name != "method" {
				return w, fmt.Errorf("%w：仅摘要阅读时，数据集/指标/代码/复现条件须标为 unverified、inference 或 unknown", domain.ErrInvalid)
			}
			for _, id := range field.EvidenceIDs {
				e, err := scanEvidence(tx.QueryRowContext(ctx, "SELECT "+evidenceColumns+" FROM evidence WHERE id=? AND owner_id=? AND project_id=?", id, scope.UserID, scope.ProjectID))
				if err != nil {
					return w, err
				}
				if err = domain.ValidateEvidence(doc, e); err != nil {
					return w, err
				}
			}
		}
	}
	// 重试可直接复用已读文档提交，执行身份不能依赖再次 read_paper 才更新。
	if w.AttemptID == "" {
		w.Attempts++
		w.AttemptID = randomID()
	}
	w.Revision++
	w.AnalysisRevision++
	w.State = draft.Status
	w.Analysis = &draft
	data, _ := json.Marshal(draft)
	w.AnalysisHash = hashBytes(data)
	if _, err = tx.ExecContext(ctx, `INSERT INTO research_analysis_versions(work_item_id,revision,content_hash,content_json) VALUES(?,?,?,?)`, w.ID, w.AnalysisRevision, w.AnalysisHash, string(data)); err != nil {
		return w, dbError(err)
	}
	if err = putWorkItem(ctx, tx, &w); err != nil {
		return w, err
	}
	for i := range run.WorkItems {
		if run.WorkItems[i].ID == w.ID {
			run.WorkItems[i] = w
		}
	}
	terminal := true
	for _, item := range run.WorkItems {
		if item.State != "completed" && item.State != "failed" {
			terminal = false
		}
	}
	if terminal {
		run.Phase = "analyses_completed"
		var version string
		if err = tx.QueryRowContext(ctx, `SELECT definition_version FROM turns WHERE id=?`, turnID).Scan(&version); err != nil {
			return w, dbError(err)
		}
		if version == "autoresearch-agent-single-v8" {
			run.Phase = "reporting"
		}
	}
	if err = putResearch(ctx, tx, scope, run); err != nil {
		return w, err
	}
	if err = saveResearchOperation(ctx, tx, scope, "analysis:"+operation, fingerprint, run); err != nil {
		return w, err
	}
	if err = analysisProgressEvent(ctx, tx, scope, run, w); err != nil {
		return w, err
	}
	return w, dbError(tx.Commit())
}

// 与工作项同事务落盘，刷新和 SSE 均可读取同一事实。
func analysisProgressEvent(ctx context.Context, tx *sql.Tx, scope domain.Scope, run domain.ResearchRun, w domain.ResearchWorkItem) error {
	title := "正在阅读：" + w.Title
	if w.State == "completed" {
		title = "已保存逐篇分析：" + w.Title
	}
	if w.State == "failed" {
		title = "论文分析失败：" + w.Title
	}
	summary := "按片段阅读并核对原文依据"
	if w.Analysis != nil {
		summary = w.Analysis.Summary
		if w.State == "failed" {
			summary = w.Analysis.Error
		}
	}
	payload, _ := json.Marshal(map[string]any{"id": "research:" + w.ID, "stage": "paper_analysis", "status": w.State, "title": title, "summary": summary, "run_id": run.ID, "paper_id": w.PaperID})
	_, err := tx.ExecContext(ctx, `INSERT INTO events(owner_id,project_id,session_id,turn_id,attempt,kind,payload_json) VALUES(?,?,?,?,(SELECT attempt FROM turns WHERE id=?),'research.progress',?)`, scope.UserID, scope.ProjectID, run.SessionID, run.TurnID, run.TurnID, string(payload))
	return dbError(err)
}
