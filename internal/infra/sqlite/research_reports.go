package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/deng-jiehui/auto-research/internal/domain"
)

func reportRow(row scanner) (domain.ResearchReport, error) {
	var raw string
	var r domain.ResearchReport
	if err := row.Scan(&raw); err != nil {
		return r, dbError(err)
	}
	err := json.Unmarshal([]byte(raw), &r)
	return r, err
}

func (s *Store) reportBody(ctx context.Context, scope domain.Scope, r domain.ResearchReport, err error) (domain.ResearchReport, error) {
	if err != nil {
		return r, err
	}
	data, err := s.artifacts.Read(ctx, scope, r.ArtifactID)
	r.Markdown = string(data)
	return r, err
}

func reportRequest(turn, operation string, expected int64, d domain.ReportDraft) (string, error) {
	if operation == "" || len(operation) > 1024 || expected < 0 {
		return "", domain.ErrInvalid
	}
	data, _ := json.Marshal(struct {
		Turn     string
		Expected int64
		Draft    domain.ReportDraft
	}{turn, expected, d})
	return hashBytes(data), nil
}

func reportReplay(ctx context.Context, q researchQuery, scope domain.Scope, turn, operation, fingerprint string) (domain.ResearchReport, error) {
	var prior, runTurn, raw string
	err := q.QueryRowContext(ctx, `SELECT o.request_hash,o.turn_id,r.content_json FROM research_report_operations o JOIN research_reports r ON r.id=o.report_id WHERE o.owner_id=? AND o.project_id=? AND o.operation_id=?`, scope.UserID, scope.ProjectID, operation).Scan(&prior, &runTurn, &raw)
	if err != nil {
		return domain.ResearchReport{}, dbError(err)
	}
	if prior != fingerprint || runTurn != turn {
		return domain.ResearchReport{}, domain.ErrConflict
	}
	var r domain.ResearchReport
	err = json.Unmarshal([]byte(raw), &r)
	return r, err
}

func (s *Store) ResearchReportReplay(ctx context.Context, scope domain.Scope, turn, operation string, expected int64, d domain.ReportDraft) (domain.ResearchReport, error) {
	f, err := reportRequest(turn, operation, expected, d)
	if err != nil {
		return domain.ResearchReport{}, err
	}
	r, err := reportReplay(ctx, s.db, scope, turn, operation, f)
	return s.reportBody(ctx, scope, r, err)
}

func (s *Store) GetResearchReport(ctx context.Context, scope domain.Scope, id string) (domain.ResearchReport, error) {
	r, err := reportRow(s.db.QueryRowContext(ctx, `SELECT content_json FROM research_reports WHERE id=? AND owner_id=? AND project_id=?`, id, scope.UserID, scope.ProjectID))
	return s.reportBody(ctx, scope, r, err)
}

func (s *Store) ListResearchReports(ctx context.Context, scope domain.Scope, runID string) ([]domain.ResearchReport, error) {
	if _, err := s.GetResearch(ctx, scope, runID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT content_json FROM research_reports WHERE run_id=? AND owner_id=? AND project_id=? ORDER BY revision DESC LIMIT 100`, runID, scope.UserID, scope.ProjectID)
	if err != nil {
		return nil, dbError(err)
	}
	defer rows.Close()
	items := []domain.ResearchReport{}
	for rows.Next() {
		r, e := reportRow(rows)
		if e != nil {
			return nil, e
		}
		items = append(items, r)
	}
	return items, dbError(rows.Err())
}

func (s *Store) SaveResearchReport(ctx context.Context, scope domain.Scope, turn, operation string, expected int64, d domain.ReportDraft) (domain.ResearchReport, error) {
	f, err := reportRequest(turn, operation, expected, d)
	if err != nil {
		return domain.ResearchReport{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.ResearchReport{}, dbError(err)
	}
	defer tx.Rollback()
	prior, err := reportReplay(ctx, tx, scope, turn, operation, f)
	if err == nil {
		return s.reportBody(ctx, scope, prior, nil)
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return prior, err
	}
	run, err := researchForTurn(ctx, tx, scope, turn)
	if err != nil {
		return prior, err
	}
	if run.CancelRequested || run.Phase == "cancelled" || (run.ExecutionState != domain.TurnQueued && run.ExecutionState != domain.TurnRunning) || run.ApprovedCandidateRevision == 0 || run.ApprovedCandidateRevision != run.CandidateRevision || len(run.WorkItems) == 0 {
		return prior, domain.ErrConflict
	}
	for _, w := range run.WorkItems {
		if w.State != "completed" && w.State != "failed" {
			return prior, fmt.Errorf("%w：仍有论文尚未处理", domain.ErrConflict)
		}
	}
	matrix := domain.BuildEvidenceMatrix(run)
	if matrix.Hash != d.MatrixHash {
		return prior, fmt.Errorf("%w：矩阵已变化，请重新读取 matrix.json", domain.ErrConflict)
	}
	if err = domain.ValidateReportDraft(d); err != nil {
		return prior, err
	}
	var revision int64
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(revision),0) FROM research_reports WHERE run_id=?`, run.ID).Scan(&revision); err != nil {
		return prior, dbError(err)
	}
	if expected != revision {
		return prior, domain.ErrConflict
	}
	references, err := s.validateReportClaims(ctx, tx, scope, run, d)
	if err != nil {
		return prior, err
	}
	id := randomID()
	r := domain.ResearchReport{ReportReference: domain.ReportReference{ID: id, Revision: revision + 1, MatrixHash: matrix.Hash, Summary: d.Summary, ResourcePath: "research://reports/" + id + "/report.md"}, RunID: run.ID, ProjectID: scope.ProjectID, Title: d.Title, CreatedAt: time.Now().UTC(), Draft: d, Matrix: matrix, References: references}
	markdown := domain.RenderResearchReport(run, r)
	r.ArtifactID, err = s.artifacts.Put(ctx, scope, []byte(markdown))
	if err != nil {
		return prior, err
	}
	data, _ := json.Marshal(r)
	if _, err = tx.ExecContext(ctx, `INSERT INTO research_reports(id,owner_id,project_id,run_id,revision,content_json) VALUES(?,?,?,?,?,?)`, r.ID, scope.UserID, scope.ProjectID, run.ID, r.Revision, string(data)); err != nil {
		return prior, dbError(err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO research_report_operations(owner_id,project_id,operation_id,turn_id,request_hash,report_id) VALUES(?,?,?,?,?,?)`, scope.UserID, scope.ProjectID, operation, turn, f, r.ID); err != nil {
		return prior, dbError(err)
	}
	run.LatestReport = &r.ReportReference
	run.Phase = "reported"
	if err = putResearch(ctx, tx, scope, run); err != nil {
		return prior, err
	}
	payload, _ := json.Marshal(r.ReportReference)
	if _, err = tx.ExecContext(ctx, `INSERT INTO events(owner_id,project_id,session_id,turn_id,attempt,kind,payload_json) VALUES(?,?,?,?,(SELECT attempt FROM turns WHERE id=?),'research.report',?)`, scope.UserID, scope.ProjectID, run.SessionID, turn, turn, string(payload)); err != nil {
		return prior, dbError(err)
	}
	if err = tx.Commit(); err != nil {
		return prior, dbError(err)
	}
	r.Markdown = markdown
	return r, nil
}

func (s *Store) validateReportClaims(ctx context.Context, tx *sql.Tx, scope domain.Scope, run domain.ResearchRun, d domain.ReportDraft) ([]domain.Evidence, error) {
	items := map[string]domain.ResearchWorkItem{}
	for _, w := range run.WorkItems {
		items[w.ID] = w
	}
	refs := []domain.Evidence{}
	seen := map[string]bool{}
	for _, claim := range d.Claims {
		allowed := map[string]domain.ResearchWorkItem{}
		for _, ref := range claim.AnalysisRefs {
			w, ok := items[ref.WorkItemID]
			if !ok || w.AnalysisRevision != ref.AnalysisRevision || w.Analysis == nil {
				return nil, fmt.Errorf("%w：结论引用的分析不属于本任务当前版本", domain.ErrConflict)
			}
			if w.State == "failed" && claim.Status == "finding" {
				return nil, domain.ErrInvalid
			}
			for _, field := range w.Analysis.Fields {
				for _, id := range field.EvidenceIDs {
					allowed[id] = w
				}
			}
		}
		for _, id := range claim.EvidenceIDs {
			w, ok := allowed[id]
			if !ok {
				return nil, fmt.Errorf("%w：证据未被所引用分析使用", domain.ErrInvalid)
			}
			e, err := scanEvidence(tx.QueryRowContext(ctx, "SELECT "+evidenceColumns+" FROM evidence WHERE id=? AND owner_id=? AND project_id=?", id, scope.UserID, scope.ProjectID))
			if err != nil {
				return nil, err
			}
			if e.DocumentID != w.DocumentID || e.PaperID != w.PaperID {
				return nil, domain.ErrInvalid
			}
			doc, e2 := scanDocument(tx.QueryRowContext(ctx, "SELECT "+documentColumns+" FROM source_documents WHERE id=? AND owner_id=? AND project_id=?", w.DocumentID, scope.UserID, scope.ProjectID))
			doc, e2 = s.hydrateDocument(ctx, scope, doc, e2)
			if e2 != nil {
				return nil, e2
			}
			if err = domain.ValidateEvidence(doc, e); err != nil {
				return nil, err
			}
			if !seen[id] {
				refs = append(refs, e)
				seen[id] = true
			}
		}
	}
	return refs, nil
}
