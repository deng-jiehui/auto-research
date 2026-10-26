package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/deng-jiehui/auto-research/internal/domain"
)

func prepareResearchRetry(ctx context.Context, tx *sql.Tx, scope domain.Scope, turn domain.Turn) error {
	run, err := researchForTurn(ctx, tx, scope, turn.ID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if turn.State == domain.TurnCompleted {
		return errors.Join(domain.ErrConflict, domain.ErrResearchFrozen)
	}
	if run.ModelCalls >= run.Spec.ModelCallLimit || run.ToolCalls >= run.Spec.ToolCallLimit {
		return domain.ErrBudgetExhausted
	}
	// 综合阶段重试只继续报告；失败论文仍是有效的覆盖记录，单篇重试由用户另行选择。
	if strings.HasSuffix(turn.DefinitionVersion, "-v8") && (run.Phase == "reporting" || run.Phase == "reported") {
		run.PauseRequested = false
		return putResearch(ctx, tx, scope, run)
	}
	for _, w := range run.WorkItems {
		if w.State == "failed" {
			w.State = "pending"
			w.Revision++
			w.AttemptID = ""
			w.Analysis = nil
			w.AnalysisHash = ""
			if err = putWorkItem(ctx, tx, &w); err != nil {
				return err
			}
		}
	}
	if len(run.WorkItems) > 0 {
		run.Phase = "analyzing"
	}
	run.PauseRequested = false
	return putResearch(ctx, tx, scope, run)
}

func applyResearchControl(ctx context.Context, tx *sql.Tx, scope domain.Scope, turn domain.Turn, action *domain.TurnAction, draft domain.ActionDraft) (domain.Turn, error) {
	if !(strings.HasSuffix(turn.DefinitionVersion, "-v6") || (strings.HasSuffix(turn.DefinitionVersion, "-v7") || strings.HasSuffix(turn.DefinitionVersion, "-v8"))) {
		return turn, domain.ErrConflict
	}
	run, err := researchForTurn(ctx, tx, scope, turn.ID)
	if err != nil {
		return turn, err
	}
	if run.Phase == "cancelled" || turn.CancelRequested {
		return turn, domain.ErrConflict
	}
	switch draft.Kind {
	case "research_pause":
		if (run.Phase != "analyzing" && run.Phase != "reporting") || (turn.State != domain.TurnRunning && turn.State != domain.TurnQueued) || run.PauseRequested {
			return turn, domain.ErrConflict
		}
		run.PauseRequested = true
		_, err = tx.ExecContext(ctx, `UPDATE turns SET revision=revision+1 WHERE id=?`, turn.ID)
	case "research_continue":
		var pending []struct {
			ID      string `json:"id"`
			Payload struct {
				Kind  string `json:"kind"`
				RunID string `json:"run_id"`
			} `json:"payload"`
		}
		if json.Unmarshal(turn.PendingQuestions, &pending) != nil || len(pending) != 1 || pending[0].Payload.Kind != "research_pause" || pending[0].Payload.RunID != run.ID || turn.State != domain.TurnAwaitingInput || !run.PauseRequested {
			return turn, domain.ErrConflict
		}
		action.PayloadJSON, _ = json.Marshal([]any{map[string]any{"interrupt_id": pending[0].ID, "text": "continue"}})
		_, err = tx.ExecContext(ctx, `UPDATE turns SET state='queued',pending_questions_json='[]',revision=revision+1 WHERE id=?`, turn.ID)
	case "research_retry":
		if turn.Active() || turn.State == domain.TurnCancelled {
			return turn, domain.ErrBusy
		}
		var input struct {
			PaperID      string `json:"paper_id"`
			WorkRevision int64  `json:"work_revision"`
		}
		if json.Unmarshal(draft.PayloadJSON, &input) != nil {
			return turn, domain.ErrInvalid
		}
		w, findErr := findWorkItem(run, input.PaperID)
		if findErr != nil {
			return turn, findErr
		}
		if w.State != "failed" || w.Revision != input.WorkRevision {
			return turn, domain.ErrConflict
		}
		if run.ModelCalls >= run.Spec.ModelCallLimit || run.ToolCalls >= run.Spec.ToolCallLimit {
			return turn, domain.ErrBudgetExhausted
		}
		w.State = "pending"
		w.Revision++
		w.AttemptID = ""
		w.Analysis = nil
		w.AnalysisHash = ""
		if err = putWorkItem(ctx, tx, &w); err != nil {
			return turn, err
		}
		run.Phase = "analyzing"
		run.PauseRequested = false
		turn, err = restartConversation(ctx, tx, turn, domain.ActionDraft{Kind: "retry"}, action.ID)
	}
	if err != nil {
		return turn, dbError(err)
	}
	if err = putResearch(ctx, tx, scope, run); err != nil {
		return turn, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO turn_actions(id,turn_id,kind,idempotency_key,request_hash,payload_json,state) VALUES(?,?,?,?,?,?,?)`, action.ID, action.TurnID, action.Kind, action.IdempotencyKey, action.RequestHash, action.PayloadJSON, action.State); err != nil {
		return turn, dbError(err)
	}
	payload, _ := json.Marshal(map[string]string{"run_id": run.ID, "action": draft.Kind})
	if _, err = tx.ExecContext(ctx, `INSERT INTO events(owner_id,project_id,session_id,turn_id,attempt,kind,payload_json) VALUES(?,?,?,?,?,'research.control',?)`, scope.UserID, scope.ProjectID, turn.SessionID, turn.ID, turn.Attempt, string(payload)); err != nil {
		return turn, dbError(err)
	}
	return scanTurn(tx.QueryRowContext(ctx, "SELECT "+turnColumns+" FROM turns WHERE id=?", turn.ID))
}

func (s *Store) SetResearchPause(ctx context.Context, scope domain.Scope, turnID string, paused bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	run, err := researchForTurn(ctx, tx, scope, turnID)
	if err != nil {
		return err
	}
	run.PauseRequested = paused
	if err = putResearch(ctx, tx, scope, run); err != nil {
		return err
	}
	return dbError(tx.Commit())
}
