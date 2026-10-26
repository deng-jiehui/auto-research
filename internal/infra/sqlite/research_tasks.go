package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/deng-jiehui/auto-research/internal/domain"
)

func (s *Store) CreateResearchTask(ctx context.Context, scope domain.Scope, draft domain.ResearchTaskDraft) (domain.ResearchTaskCreated, error) {
	if draft.OperationID == "" || len(draft.OperationID) > 1024 {
		return domain.ResearchTaskCreated{}, domain.ErrInvalid
	}
	request, _ := json.Marshal(struct {
		Spec                    domain.ResearchSpec
		Session, Turn, Previous string
	}{draft.Spec, draft.OriginSessionID, draft.OriginTurnID, draft.PreviousRunID})
	fingerprint := hashBytes(request)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.ResearchTaskCreated{}, err
	}
	defer tx.Rollback()
	var prior, priorHash string
	err = tx.QueryRowContext(ctx, `SELECT run_id,request_hash FROM research_tasks WHERE project_id=? AND owner_id=? AND operation_id=?`, scope.ProjectID, scope.UserID, draft.OperationID).Scan(&prior, &priorHash)
	if err == nil {
		if priorHash != fingerprint {
			return domain.ResearchTaskCreated{}, domain.ErrConflict
		}
		run, e := researchRow(tx.QueryRowContext(ctx, `SELECT content_json FROM research_runs WHERE id=?`, prior))
		if e != nil {
			return domain.ResearchTaskCreated{}, e
		}
		run, e = hydrateResearch(ctx, tx, run)
		if e != nil {
			return domain.ResearchTaskCreated{}, e
		}
		turn, e := scanTurn(tx.QueryRowContext(ctx, "SELECT "+turnColumns+" FROM turns WHERE id=?", run.TurnID))
		return domain.ResearchTaskCreated{Run: run, Turn: turn, Replayed: true}, e
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return domain.ResearchTaskCreated{}, dbError(err)
	}
	var message struct {
		Role string `json:"role"`
	}
	if json.Unmarshal(draft.MessageJSON, &message) != nil || message.Role != "user" {
		return domain.ResearchTaskCreated{}, domain.ErrInvalid
	}
	// 派生任务沿用已验证的来源标记，来源会话删除后仍可重新研究。
	fromPrevious := false
	if draft.PreviousRunID != "" {
		previous, e := researchRow(tx.QueryRowContext(ctx, `SELECT content_json FROM research_runs WHERE id=? AND project_id=? AND owner_id=?`, draft.PreviousRunID, scope.ProjectID, scope.UserID))
		if e != nil {
			return domain.ResearchTaskCreated{}, e
		}
		if !previous.Detached || previous.OriginSessionID != draft.OriginSessionID || previous.OriginTurnID != draft.OriginTurnID {
			return domain.ResearchTaskCreated{}, domain.ErrInvalid
		}
		fromPrevious = true
	}
	if draft.OriginSessionID != "" && !fromPrevious {
		var found string
		if err = tx.QueryRowContext(ctx, `SELECT s.id FROM sessions s JOIN projects p ON p.id=s.project_id WHERE s.id=? AND p.id=? AND p.owner_id=? AND s.kind='chat'`, draft.OriginSessionID, scope.ProjectID, scope.UserID).Scan(&found); err != nil {
			return domain.ResearchTaskCreated{}, dbError(err)
		}
		if draft.OriginTurnID != "" {
			if err = tx.QueryRowContext(ctx, `SELECT id FROM turns WHERE id=? AND session_id=? AND project_id=? AND owner_id=?`, draft.OriginTurnID, found, scope.ProjectID, scope.UserID).Scan(&found); err != nil {
				return domain.ResearchTaskCreated{}, dbError(err)
			}
		}
	} else if draft.OriginSessionID == "" && draft.OriginTurnID != "" {
		return domain.ResearchTaskCreated{}, domain.ErrInvalid
	}
	for _, id := range draft.Spec.PaperIDs {
		var found string
		if err = tx.QueryRowContext(ctx, `SELECT id FROM papers WHERE id=? AND project_id=? AND owner_id=?`, id, scope.ProjectID, scope.UserID).Scan(&found); err != nil {
			return domain.ResearchTaskCreated{}, dbError(err)
		}
	}
	sessionID := randomID()
	if _, err = tx.ExecContext(ctx, `INSERT INTO sessions(id,project_id,title,kind) SELECT ?,id,?,'research' FROM projects WHERE id=? AND owner_id=?`, sessionID, "研究任务", scope.ProjectID, scope.UserID); err != nil {
		return domain.ResearchTaskCreated{}, dbError(err)
	}
	turn := domain.Turn{ID: randomID(), OwnerID: scope.UserID, ProjectID: scope.ProjectID, SessionID: sessionID, State: domain.TurnQueued, InputText: draft.Spec.Question, IdempotencyKey: draft.OperationID, ContextJSON: draft.ContextJSON, DefinitionVersion: draft.DefinitionVersion, Revision: 1, ExecutionKind: "research"}
	if _, err = tx.ExecContext(ctx, `INSERT INTO turns(id,owner_id,project_id,session_id,state,input_text,idempotency_key,context_json,definition_version,execution_kind) VALUES(?,?,?,?,?,?,?,?,?,'research')`, turn.ID, scope.UserID, scope.ProjectID, sessionID, turn.State, turn.InputText, turn.IdempotencyKey, turn.ContextJSON, turn.DefinitionVersion); err != nil {
		return domain.ResearchTaskCreated{}, dbError(err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO messages(id,owner_id,project_id,session_id,turn_id,attempt,seq,content_json,committed) VALUES(?,?,?,?,?,0,1,?,1)`, randomID(), scope.UserID, scope.ProjectID, sessionID, turn.ID, draft.MessageJSON); err != nil {
		return domain.ResearchTaskCreated{}, dbError(err)
	}
	run, err := createResearch(ctx, tx, scope, turn, draft.Spec, draft.PreviousRunID)
	if err != nil {
		return domain.ResearchTaskCreated{}, err
	}
	run.Detached, run.OriginSessionID, run.OriginTurnID = true, draft.OriginSessionID, draft.OriginTurnID
	if err = putResearch(ctx, tx, scope, run); err != nil {
		return domain.ResearchTaskCreated{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO research_tasks(run_id,owner_id,project_id,origin_session_id,origin_turn_id,operation_id,request_hash) VALUES(?,?,?,?,?,?,?)`, run.ID, scope.UserID, scope.ProjectID, draft.OriginSessionID, draft.OriginTurnID, draft.OperationID, fingerprint); err != nil {
		return domain.ResearchTaskCreated{}, dbError(err)
	}
	if err = tx.Commit(); err != nil {
		return domain.ResearchTaskCreated{}, dbError(err)
	}
	return domain.ResearchTaskCreated{Run: run, Turn: turn}, nil
}

func taskSummary(run domain.ResearchRun) domain.ResearchTaskSummary {
	text := "研究正在独立执行，可在任务页查看进度。"
	switch run.ExecutionState {
	case domain.TurnAwaitingInput:
		text = "等待你在任务页确认计划、审核论文或补充信息。"
		if run.PauseRequested {
			text = "研究已暂停，可在任务页继续。"
		}
	case domain.TurnCancelled:
		text = "研究已取消，已保存的成果仍可查询。"
	case domain.TurnFailed, domain.TurnRecoveryRequired:
		text = "研究执行失败：" + run.ErrorMessage
	case domain.TurnCompleted:
		completed, failed := 0, 0
		parts := []string{}
		for _, w := range run.WorkItems {
			if w.State == "failed" {
				failed++
			}
			if w.State == "completed" {
				completed++
				if w.Analysis != nil && len(parts) < 3 {
					parts = append(parts, w.Analysis.Summary)
				}
			}
		}
		text = fmt.Sprintf("逐篇处理结束：完成 %d 篇，失败 %d 篇。", completed, failed)
		if run.Phase == "cancelled" {
			text = "研究已取消。"
		} else {
			if run.LatestReport != nil && run.LatestReport.MatrixHash == domain.BuildEvidenceMatrix(run).Hash {
				text += "报告已保存。" + run.LatestReport.Summary
			} else {
				text += strings.Join(parts, "；")
			}
		}
	}
	if utf8.RuneCountInString(text) > 500 {
		text = string([]rune(text)[:500]) + "…"
	}
	reportPath := ""
	if run.LatestReport != nil {
		reportPath = run.LatestReport.ResourcePath
	}
	return domain.ResearchTaskSummary{ReportPath: reportPath, ID: run.ID, Question: run.Spec.Question, State: run.ExecutionState, Phase: run.Phase, Summary: text, TaskURL: "/p/" + run.ProjectID + "/research/" + run.ID, ResourcePath: "research://runs/" + run.ID + ".json"}
}

func (s *Store) ResearchTaskSummaries(ctx context.Context, scope domain.Scope, sessionID string) ([]domain.ResearchTaskSummary, error) {
	if _, err := s.GetSession(ctx, scope, sessionID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT r.content_json FROM research_tasks t JOIN research_runs r ON r.id=t.run_id WHERE t.project_id=? AND t.owner_id=? AND t.origin_session_id=? ORDER BY t.rowid DESC LIMIT 100`, scope.ProjectID, scope.UserID, sessionID)
	if err != nil {
		return nil, dbError(err)
	}
	defer rows.Close()
	runs := []domain.ResearchRun{}
	for rows.Next() {
		run, e := researchRow(rows)
		if e != nil {
			return nil, e
		}
		runs = append(runs, run)
	}
	if err = rows.Err(); err != nil {
		return nil, dbError(err)
	}
	rows.Close()
	items := make([]domain.ResearchTaskSummary, 0, len(runs))
	for _, run := range runs {
		run, err = hydrateResearch(ctx, s.db, run)
		if err != nil {
			return nil, err
		}
		items = append(items, taskSummary(run))
	}
	return items, nil
}
