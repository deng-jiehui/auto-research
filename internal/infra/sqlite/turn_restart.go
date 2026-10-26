package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/deng-jiehui/auto-research/internal/domain"
)

// restartConversation 在同一事务内替换原消息、失效后续对话并重新排队。
// 论文、笔记和工作区文件是独立产物，不随对话回退而撤销。
func restartConversation(ctx context.Context, tx *sql.Tx, turn domain.Turn, draft domain.ActionDraft, actionID string) (domain.Turn, error) {
	var active int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM turns WHERE session_id=? AND project_id=? AND owner_id=? AND state IN ('queued','running','awaiting_input','recovery_required')`, turn.SessionID, turn.ProjectID, turn.OwnerID).Scan(&active); err != nil {
		return domain.Turn{}, dbError(err)
	}
	if active != 0 {
		return domain.Turn{}, domain.ErrBusy
	}
	var messageID, content string
	var seq int64
	if err := tx.QueryRowContext(ctx, `SELECT id,seq,content_json FROM messages WHERE turn_id=? AND session_id=? AND project_id=? AND owner_id=? ORDER BY seq LIMIT 1`, turn.ID, turn.SessionID, turn.ProjectID, turn.OwnerID).Scan(&messageID, &seq, &content); err != nil {
		return domain.Turn{}, dbError(err)
	}
	var message struct {
		Role string `json:"role"`
	}
	if json.Unmarshal([]byte(content), &message) != nil || message.Role != "user" {
		return domain.Turn{}, fmt.Errorf("%w：原用户消息不可读取", domain.ErrInvalid)
	}
	if draft.Kind == "edit" {
		if draft.Replacement == nil || draft.Replacement.Text == "" || !json.Valid(draft.Replacement.ContextJSON) || !json.Valid(draft.Replacement.MessageJSON) {
			return domain.Turn{}, domain.ErrInvalid
		}
		turn.InputText, turn.ContextJSON = draft.Replacement.Text, draft.Replacement.ContextJSON
		if _, err := tx.ExecContext(ctx, `UPDATE messages SET content_json=? WHERE id=?`, draft.Replacement.MessageJSON, messageID); err != nil {
			return domain.Turn{}, dbError(err)
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE messages SET superseded=1 WHERE session_id=? AND project_id=? AND owner_id=? AND seq>?`, turn.SessionID, turn.ProjectID, turn.OwnerID, seq); err != nil {
		return domain.Turn{}, dbError(err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE turns SET state='superseded',revision=revision+1,updated_at=CURRENT_TIMESTAMP WHERE session_id=? AND project_id=? AND owner_id=? AND rowid>(SELECT rowid FROM turns WHERE id=?)`, turn.SessionID, turn.ProjectID, turn.OwnerID, turn.ID); err != nil {
		return domain.Turn{}, dbError(err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE turns SET state='queued',input_text=?,context_json=?,cancel_requested=0,pending_questions_json='[]',error_code='',error_message='',canonical_message_ids_json='[]',progress_json='{}',progress_checkpoint_id='',progress_checkpoint_version=0,revision=revision+1,updated_at=CURRENT_TIMESTAMP WHERE id=?`, turn.InputText, turn.ContextJSON, turn.ID); err != nil {
		return domain.Turn{}, dbError(err)
	}
	payload, _ := json.Marshal(map[string]string{"action_id": actionID, "kind": draft.Kind})
	if _, err := tx.ExecContext(ctx, `INSERT INTO events(owner_id,project_id,session_id,turn_id,attempt,kind,payload_json) VALUES (?,?,?,?,?,'turn.restarted',?)`, turn.OwnerID, turn.ProjectID, turn.SessionID, turn.ID, turn.Attempt, payload); err != nil {
		return domain.Turn{}, dbError(err)
	}
	return scanTurn(tx.QueryRowContext(ctx, "SELECT "+turnColumns+" FROM turns WHERE id=?", turn.ID))
}
