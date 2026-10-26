package sqlite

import (
	"context"

	"github.com/deng-jiehui/auto-research/internal/domain"
)

// prepareDocuments 为升级前的冻结文本补索引；不会改写正文或旧引用。
func (s *Store) prepareDocuments(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT d.id,d.owner_id,d.project_id FROM source_documents d WHERE NOT EXISTS (SELECT 1 FROM document_blocks b WHERE b.document_id=d.id)`)
	if err != nil {
		return err
	}
	type missingDocument struct {
		id    string
		scope domain.Scope
	}
	var missing []missingDocument
	for rows.Next() {
		var item missingDocument
		if err = rows.Scan(&item.id, &item.scope.UserID, &item.scope.ProjectID); err != nil {
			rows.Close()
			return err
		}
		missing = append(missing, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, item := range missing {
		doc, err := s.GetDocument(ctx, item.scope, item.id)
		if err != nil {
			return err
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		for _, block := range domain.DocumentBlocks(doc) {
			_, err = tx.ExecContext(ctx, `INSERT INTO document_blocks(id,document_id,owner_id,project_id,paper_id,title,text,start_rune,end_rune) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(document_id,id) DO NOTHING`, block.ID, doc.ID, item.scope.UserID, item.scope.ProjectID, doc.PaperID, block.Title, block.Text, block.StartRune, block.EndRune)
			if err != nil {
				tx.Rollback()
				return err
			}
		}
		if err = tx.Commit(); err != nil {
			return err
		}
	}
	// 本地服务启动时没有旧下载 worker，可将残留状态转成可重试失败。
	_, err = s.db.ExecContext(ctx, `UPDATE document_fetches SET state='failed',error='上次全文获取因服务退出而中断，请重试',updated_at=CURRENT_TIMESTAMP WHERE state='running'`)
	return err
}
