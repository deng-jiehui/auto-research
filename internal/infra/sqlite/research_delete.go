package sqlite

import (
	"context"
	"database/sql"

	"github.com/deng-jiehui/auto-research/internal/domain"
)

// DeleteResearch 删除已结束的调研及其执行数据。独立任务的内部 session
// 会一起删除；普通会话中的研究只删除研究记录，避免误删用户聊天。
func (s *Store) DeleteResearch(ctx context.Context, scope domain.Scope, id string) error {
	if id == "" {
		return domain.ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return dbError(err)
	}
	defer tx.Rollback()
	var sessionID, turnID, kind, state string
	err = tx.QueryRowContext(ctx, `
		SELECT r.session_id,r.turn_id,s.kind,t.state
		FROM research_runs r
		JOIN sessions s ON s.id=r.session_id
		JOIN turns t ON t.id=r.turn_id
		WHERE r.id=? AND r.project_id=? AND r.owner_id=?`, id, scope.ProjectID, scope.UserID).
		Scan(&sessionID, &turnID, &kind, &state)
	if err != nil {
		if err == sql.ErrNoRows {
			return domain.ErrNotFound
		}
		return dbError(err)
	}
	if state == domain.TurnQueued || state == domain.TurnRunning || state == domain.TurnAwaitingInput || state == domain.TurnRecoveryRequired {
		return domain.ErrBusy
	}
	// 研究操作账本没有 run_id 外键，结果 JSON 中的任务 ID 是其归属依据。
	if _, err = tx.ExecContext(ctx, `DELETE FROM research_operations WHERE owner_id=? AND project_id=? AND json_extract(result_json,'$.id')=?`, scope.UserID, scope.ProjectID, id); err != nil {
		return dbError(err)
	}
	if kind == "research" {
		// checkpoint_heads 没有外键，先清理指向本轮 checkpoint 的头指针，
		// 再由内部 session 的级联删除 checkpoint 明细。
		if _, err = tx.ExecContext(ctx, `DELETE FROM checkpoint_heads WHERE checkpoint_id IN (SELECT id FROM checkpoints WHERE turn_id=?)`, turnID); err != nil {
			return dbError(err)
		}
		result, execErr := tx.ExecContext(ctx, `DELETE FROM sessions WHERE id=? AND project_id=?`, sessionID, scope.ProjectID)
		if execErr != nil {
			return dbError(execErr)
		}
		if count, countErr := result.RowsAffected(); countErr != nil {
			return dbError(countErr)
		} else if count == 0 {
			return domain.ErrNotFound
		}
	} else {
		result, execErr := tx.ExecContext(ctx, `DELETE FROM research_runs WHERE id=? AND project_id=? AND owner_id=?`, id, scope.ProjectID, scope.UserID)
		if execErr != nil {
			return dbError(execErr)
		}
		if count, countErr := result.RowsAffected(); countErr != nil {
			return dbError(countErr)
		} else if count == 0 {
			return domain.ErrNotFound
		}
	}
	return dbError(tx.Commit())
}
