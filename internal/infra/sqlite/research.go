package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/deng-jiehui/auto-research/internal/domain"
)

type researchQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func researchRow(row scanner) (domain.ResearchRun, error) {
	var text string
	var run domain.ResearchRun
	if err := row.Scan(&text); err != nil {
		return run, dbError(err)
	}
	err := json.Unmarshal([]byte(text), &run)
	return run, err
}
func researchForTurn(ctx context.Context, q researchQuery, scope domain.Scope, turnID string) (domain.ResearchRun, error) {
	run, err := researchRow(q.QueryRowContext(ctx, `SELECT content_json FROM research_runs WHERE turn_id=? AND project_id=? AND owner_id=?`, turnID, scope.ProjectID, scope.UserID))
	if err != nil {
		return run, err
	}
	return hydrateResearch(ctx, q, run)
}
func (s *Store) ResearchForTurn(ctx context.Context, scope domain.Scope, turnID string) (domain.ResearchRun, error) {
	return researchForTurn(ctx, s.db, scope, turnID)
}
func (s *Store) GetResearch(ctx context.Context, scope domain.Scope, id string) (domain.ResearchRun, error) {
	run, err := researchRow(s.db.QueryRowContext(ctx, `SELECT content_json FROM research_runs WHERE id=? AND project_id=? AND owner_id=?`, id, scope.ProjectID, scope.UserID))
	if err != nil {
		return run, err
	}
	return hydrateResearch(ctx, s.db, run)
}
func (s *Store) ListResearch(ctx context.Context, scope domain.Scope) ([]domain.ResearchRun, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT content_json FROM research_runs WHERE project_id=? AND owner_id=? ORDER BY rowid DESC LIMIT 100`, scope.ProjectID, scope.UserID)
	if err != nil {
		return nil, dbError(err)
	}
	defer rows.Close()
	items := make([]domain.ResearchRun, 0)
	for rows.Next() {
		run, err := researchRow(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, run)
	}
	if err = rows.Err(); err != nil {
		return nil, dbError(err)
	}
	rows.Close()
	for i := range items {
		items[i], err = hydrateResearch(ctx, s.db, items[i])
		if err != nil {
			return nil, err
		}
	}
	return items, nil
}
func putResearch(ctx context.Context, tx *sql.Tx, scope domain.Scope, run domain.ResearchRun) error {
	data, err := json.Marshal(run)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE research_runs SET content_json=? WHERE id=? AND project_id=? AND owner_id=?`, string(data), run.ID, scope.ProjectID, scope.UserID)
	return dbError(err)
}

// 登记与 Turn 共用事务；先查幂等，之后才冻结当前项目/方向，重发不受资料变化影响。
func createResearch(ctx context.Context, tx *sql.Tx, scope domain.Scope, turn domain.Turn, spec domain.ResearchSpec, parents ...string) (domain.ResearchRun, error) {
	request, _ := json.Marshal(spec)
	project, err := scanProject(tx.QueryRowContext(ctx, "SELECT "+projectColumns+" FROM projects WHERE id=? AND owner_id=?", scope.ProjectID, scope.UserID))
	if err != nil {
		return domain.ResearchRun{}, err
	}
	if spec.Question == "" {
		spec.Question = turn.InputText
	}
	if strings.TrimSpace(spec.Question) == "" || utf8.RuneCountInString(spec.Question) > 4000 {
		return domain.ResearchRun{}, domain.ErrInvalid
	}
	if spec.Settings.CandidateLimit == 0 {
		spec.Settings = project.Settings
	}
	spec.Settings, err = domain.NormalizeSettings(spec.Settings)
	if err != nil {
		return domain.ResearchRun{}, err
	}
	if spec.ModelCallLimit == 0 {
		spec.ModelCallLimit = 24
		if strings.HasSuffix(turn.DefinitionVersion, "-v6") || (strings.HasSuffix(turn.DefinitionVersion, "-v7") || strings.HasSuffix(turn.DefinitionVersion, "-v8")) {
			spec.ModelCallLimit = 64
		}
	}
	if spec.ToolCallLimit == 0 {
		spec.ToolCallLimit = 64
		if strings.HasSuffix(turn.DefinitionVersion, "-v6") || (strings.HasSuffix(turn.DefinitionVersion, "-v7") || strings.HasSuffix(turn.DefinitionVersion, "-v8")) {
			spec.ToolCallLimit = 128
		}
	}
	if spec.ModelCallLimit < 1 || spec.ModelCallLimit > 100 || spec.ToolCallLimit < 1 || spec.ToolCallLimit > 300 {
		return domain.ResearchRun{}, domain.ErrInvalid
	}
	run := domain.ResearchRun{ID: randomID(), ProjectID: scope.ProjectID, SessionID: turn.SessionID, TurnID: turn.ID, Phase: "planning", Spec: spec, ProjectSnapshot: project}
	if len(parents) > 0 && parents[0] != "" {
		var parent string
		if err = tx.QueryRowContext(ctx, `SELECT id FROM research_runs WHERE id=? AND project_id=? AND owner_id=?`, parents[0], scope.ProjectID, scope.UserID).Scan(&parent); err != nil {
			return run, dbError(err)
		}
		run.PreviousRunID = parent
	}
	if spec.DirectionID != "" {
		var raw string
		if err = tx.QueryRowContext(ctx, `SELECT content_json FROM directions WHERE id=? AND project_id=? AND owner_id=?`, spec.DirectionID, scope.ProjectID, scope.UserID).Scan(&raw); err != nil {
			return run, dbError(err)
		}
		var d domain.Direction
		if err = json.Unmarshal([]byte(raw), &d); err != nil {
			return run, err
		}
		if d.State != "accepted" || d.Revision != spec.DirectionRevision {
			return run, domain.ErrConflict
		}
		run.DirectionSnapshot = &d
	} else if spec.DirectionRevision != 0 {
		return run, domain.ErrInvalid
	}
	frozen, _ := json.Marshal(run.Spec)
	run.SpecHash = hashBytes(frozen)
	run, err = hydrateResearch(ctx, tx, run)
	if err != nil {
		return run, err
	}
	if run.ModelCalls > run.Spec.ModelCallLimit || run.ToolCalls > run.Spec.ToolCallLimit {
		return run, domain.ErrBudgetExhausted
	}
	data, _ := json.Marshal(run)
	_, err = tx.ExecContext(ctx, `INSERT INTO research_runs(id,owner_id,project_id,session_id,turn_id,start_hash,content_json) VALUES(?,?,?,?,?,?,?)`, run.ID, scope.UserID, scope.ProjectID, turn.SessionID, turn.ID, hashBytes(request), string(data))
	return run, dbError(err)
}

func (s *Store) SaveResearchPlan(ctx context.Context, scope domain.Scope, turnID, operationID string, expected int64, plan domain.ResearchPlan) (domain.ResearchRun, error) {
	if operationID == "" || len(operationID) > 1024 {
		return domain.ResearchRun{}, domain.ErrInvalid
	}
	request, _ := json.Marshal(struct {
		TurnID   string
		Expected int64
		Plan     domain.ResearchPlan
	}{turnID, expected, plan})
	fingerprint := hashBytes(request)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.ResearchRun{}, dbError(err)
	}
	defer tx.Rollback()
	var prior, priorHash string
	err = tx.QueryRowContext(ctx, `SELECT request_hash,result_json FROM research_operations WHERE owner_id=? AND project_id=? AND operation_id=?`, scope.UserID, scope.ProjectID, operationID).Scan(&priorHash, &prior)
	if err == nil {
		if priorHash != fingerprint {
			return domain.ResearchRun{}, domain.ErrConflict
		}
		var saved domain.ResearchRun
		err = json.Unmarshal([]byte(prior), &saved)
		return saved, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return domain.ResearchRun{}, dbError(err)
	}
	if err = domain.ValidateResearchPlan(plan); err != nil {
		return domain.ResearchRun{}, err
	}
	turn, err := scanTurn(tx.QueryRowContext(ctx, "SELECT "+turnColumns+" FROM turns WHERE id=? AND owner_id=? AND project_id=?", turnID, scope.UserID, scope.ProjectID))
	if err != nil {
		return domain.ResearchRun{}, err
	}
	if turn.State != domain.TurnQueued && turn.State != domain.TurnRunning {
		return domain.ResearchRun{}, domain.ErrConflict
	}
	run, err := researchForTurn(ctx, tx, scope, turnID)
	if errors.Is(err, domain.ErrNotFound) {
		if expected != 0 {
			return run, domain.ErrConflict
		}
		run, err = createResearch(ctx, tx, scope, turn, domain.ResearchSpec{Question: turn.InputText})
		if err == nil {
			_, err = tx.ExecContext(ctx, `UPDATE research_runs SET start_hash='' WHERE id=?`, run.ID)
		}
	}
	if err != nil {
		return run, err
	}
	if run.PlanRevision != expected || run.Phase == "cancelled" {
		return run, domain.ErrConflict
	}
	if len(run.WorkItems) > 0 {
		return run, errors.Join(domain.ErrConflict, domain.ErrResearchFrozen)
	}
	run.Plan = plan
	run.PlanRevision++
	run.ApprovedPlanRevision = 0
	run.Phase = "planning"
	data, _ := json.Marshal(plan)
	run.PlanHash = hashBytes(data)
	if run.CandidateRevision > 0 {
		run.Candidates, run.CandidateReview = nil, nil
		run.CandidateSearches, run.ApprovedCandidateRevision = 0, 0
		run.CandidateRound++
		if err = putCandidateVersion(ctx, tx, scope, &run); err != nil {
			return run, err
		}
	}
	if err = putResearch(ctx, tx, scope, run); err != nil {
		return run, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO research_plan_versions(run_id,revision,plan_json) VALUES(?,?,?)`, run.ID, run.PlanRevision, string(data)); err != nil {
		return run, dbError(err)
	}
	result, _ := json.Marshal(run)
	if _, err = tx.ExecContext(ctx, `INSERT INTO research_operations(owner_id,project_id,operation_id,request_hash,result_json) VALUES(?,?,?,?,?)`, scope.UserID, scope.ProjectID, operationID, fingerprint, string(result)); err != nil {
		return run, dbError(err)
	}
	return run, dbError(tx.Commit())
}

func applyResearchReview(ctx context.Context, tx *sql.Tx, scope domain.Scope, turn domain.Turn, draft domain.ActionDraft) error {
	var answers []struct {
		InterruptID        string                          `json:"interrupt_id"`
		ResearchPlan       *domain.ResearchPlanReview      `json:"research_plan,omitempty"`
		ResearchCandidates *domain.ResearchCandidateReview `json:"research_candidates,omitempty"`
	}
	if err := json.Unmarshal(draft.PayloadJSON, &answers); err != nil {
		return domain.ErrInvalid
	}
	var pending []struct {
		ID      string `json:"id"`
		Payload struct {
			Kind     string `json:"kind"`
			RunID    string `json:"run_id"`
			Revision int64  `json:"revision"`
			Hash     string `json:"hash"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(turn.PendingQuestions, &pending); err != nil {
		return domain.ErrInvalid
	}
	needsReview := false
	for _, p := range pending {
		if p.Payload.Kind == "research_plan" || p.Payload.Kind == "research_candidates" {
			needsReview = true
		}
	}
	if !needsReview {
		for _, a := range answers {
			if a.ResearchPlan != nil || a.ResearchCandidates != nil {
				return domain.ErrInvalid
			}
		}
		return nil
	}
	if turn.State != domain.TurnAwaitingInput || len(answers) != 1 || len(pending) != 1 || answers[0].InterruptID != pending[0].ID {
		return domain.ErrConflict
	}
	if pending[0].Payload.Kind == "research_candidates" {
		if answers[0].ResearchCandidates == nil || answers[0].ResearchPlan != nil {
			return domain.ErrInvalid
		}
		p := pending[0].Payload
		return applyCandidateReview(ctx, tx, scope, turn, *answers[0].ResearchCandidates, p.RunID, p.Revision, p.Hash)
	}
	if answers[0].ResearchPlan == nil || answers[0].ResearchCandidates != nil {
		return domain.ErrInvalid
	}
	review := answers[0].ResearchPlan
	p := pending[0].Payload
	run, err := researchForTurn(ctx, tx, scope, turn.ID)
	if err != nil {
		return err
	}
	if review.RunID != run.ID || p.RunID != run.ID || review.PlanRevision != run.PlanRevision || p.Revision != run.PlanRevision || review.PlanHash != run.PlanHash || p.Hash != run.PlanHash || run.ApprovedPlanRevision != 0 {
		return domain.ErrConflict
	}
	if review.Decision == "cancel" {
		run.Phase = "cancelled"
	} else if review.Decision == "approve" {
		if err = domain.ValidateResearchPlan(review.Plan); err != nil {
			return err
		}
		data, _ := json.Marshal(review.Plan)
		if hashBytes(data) != run.PlanHash {
			run.PlanRevision++
			run.Plan = review.Plan
			run.PlanHash = hashBytes(data)
			if _, err = tx.ExecContext(ctx, `INSERT INTO research_plan_versions(run_id,revision,plan_json) VALUES(?,?,?)`, run.ID, run.PlanRevision, string(data)); err != nil {
				return dbError(err)
			}
		}
		run.ApprovedPlanRevision = run.PlanRevision
		run.Phase = "searching"
	} else {
		return domain.ErrInvalid
	}
	return putResearch(ctx, tx, scope, run)
}

// 调用账本属于 Turn，首次提交计划前的模型和工具也会被纳入预算。
func hydrateResearch(ctx context.Context, q researchQuery, run domain.ResearchRun) (domain.ResearchRun, error) {
	err := q.QueryRowContext(ctx, `SELECT COALESCE(SUM(kind='model'),0),COALESCE(SUM(kind='tool'),0),COALESCE(SUM(input_tokens),0),COALESCE(SUM(output_tokens),0),COALESCE(SUM(kind='model' AND usage_recorded=0),0) FROM research_attempts WHERE turn_id=?`, run.TurnID).Scan(&run.ModelCalls, &run.ToolCalls, &run.InputTokens, &run.OutputTokens, &run.UnknownUsage)
	if err != nil {
		return run, dbError(err)
	}
	err = q.QueryRowContext(ctx, `SELECT state,attempt,error_message,cancel_requested FROM turns WHERE id=?`, run.TurnID).Scan(&run.ExecutionState, &run.ExecutionAttempt, &run.ErrorMessage, &run.CancelRequested)
	if err != nil {
		return run, dbError(err)
	}
	var items string
	err = q.QueryRowContext(ctx, `SELECT COALESCE(json_group_array(json(content_json)),'[]') FROM (SELECT content_json FROM research_work_items WHERE run_id=? ORDER BY rowid)`, run.ID).Scan(&items)
	if err == nil {
		// 明细表是唯一当前状态；复用旧切片会让 JSON 省略的字段残留旧失败内容。
		run.WorkItems = nil
		err = json.Unmarshal([]byte(items), &run.WorkItems)
	}
	return run, dbError(err)
}
func (s *Store) ReserveResearchCall(ctx context.Context, scope domain.Scope, turnID, kind string) (string, error) {
	if kind != "model" && kind != "tool" {
		return "", domain.ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", dbError(err)
	}
	defer tx.Rollback()
	var allowed int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM turns WHERE id=? AND project_id=? AND owner_id=?`, turnID, scope.ProjectID, scope.UserID).Scan(&allowed); err != nil {
		return "", dbError(err)
	}
	if allowed == 0 {
		return "", domain.ErrNotFound
	}
	run, err := researchForTurn(ctx, tx, scope, turnID)
	if err == nil {

		if (kind == "model" && run.ModelCalls >= run.Spec.ModelCallLimit) || (kind == "tool" && run.ToolCalls >= run.Spec.ToolCallLimit) {
			return "", domain.ErrBudgetExhausted
		}
	} else if !errors.Is(err, domain.ErrNotFound) {
		return "", err
	}
	id := randomID()
	_, err = tx.ExecContext(ctx, `INSERT INTO research_attempts(id,turn_id,kind) VALUES(?,?,?)`, id, turnID, kind)
	if err != nil {
		return "", dbError(err)
	}
	return id, dbError(tx.Commit())
}
func (s *Store) RecordResearchUsage(ctx context.Context, scope domain.Scope, attemptID string, input, output int64, known bool) error {
	if !known {
		return nil
	}
	if input < 0 || output < 0 {
		return domain.ErrInvalid
	}
	_, err := s.db.ExecContext(ctx, `UPDATE research_attempts SET input_tokens=?,output_tokens=?,usage_recorded=1 WHERE id=? AND kind='model' AND usage_recorded=0 AND turn_id IN (SELECT id FROM turns WHERE project_id=? AND owner_id=?)`, input, output, attemptID, scope.ProjectID, scope.UserID)
	return dbError(err)
}
