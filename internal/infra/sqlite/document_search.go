package sqlite

import (
	"context"
	"strings"

	"github.com/deng-jiehui/auto-research/internal/domain"
)

func (s *Store) SearchDocumentBlocks(ctx context.Context, scope domain.Scope, query, paperID string, limit int) ([]domain.DocumentHit, error) {
	terms := strings.Fields(query)
	for i, t := range terms {
		terms[i] = `"` + strings.ReplaceAll(t, `"`, `""`) + `"`
	}
	fts := strings.Join(terms, " AND ")
	columns := `b.id,b.document_id,b.paper_id,b.title,b.text,b.start_rune,b.end_rune,d.locator,d.content_kind`
	base := ` FROM document_blocks b JOIN source_documents d ON d.id=b.document_id WHERE b.project_id=? AND b.owner_id=? AND (?='' OR b.paper_id=?)`
	args := []any{scope.ProjectID, scope.UserID, paperID, paperID}
	read := func(sql string, args []any) ([]domain.DocumentHit, error) {
		rows, err := s.db.QueryContext(ctx, sql, args...)
		if err != nil {
			return nil, dbError(err)
		}
		defer rows.Close()
		items := make([]domain.DocumentHit, 0)
		for rows.Next() {
			var h domain.DocumentHit
			if err = rows.Scan(&h.ID, &h.DocumentID, &h.PaperID, &h.Title, &h.Text, &h.StartRune, &h.EndRune, &h.Locator, &h.ContentKind); err != nil {
				return nil, err
			}
			items = append(items, h)
		}
		return items, rows.Err()
	}
	hits, err := read("SELECT "+columns+base+` AND b.rowid IN (SELECT rowid FROM document_blocks_fts WHERE document_blocks_fts MATCH ?) ORDER BY b.rowid DESC LIMIT ?`, append(append([]any{}, args...), fts, limit))
	if err != nil {
		return nil, err
	}
	if len(hits) >= limit {
		return hits, nil
	}
	// 中文短语补充字面检索；不把 SQL 通配符当成查询语义。
	escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(query)
	fallback, err := read("SELECT "+columns+base+` AND b.text LIKE ? ESCAPE '\' ORDER BY b.rowid DESC LIMIT ?`, append(append([]any{}, args...), "%"+escaped+"%", limit))
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, h := range hits {
		seen[h.DocumentID+":"+h.ID] = true
	}
	for _, h := range fallback {
		key := h.DocumentID + ":" + h.ID
		if !seen[key] && len(hits) < limit {
			hits = append(hits, h)
			seen[key] = true
		}
	}
	return hits, nil
}

func (s *Store) SaveDocumentFetch(ctx context.Context, scope domain.Scope, f domain.DocumentFetch) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO document_fetches(paper_id,owner_id,project_id,state,document_id,error) SELECT id,owner_id,project_id,?,?,? FROM papers WHERE id=? AND project_id=? AND owner_id=? ON CONFLICT(paper_id) DO UPDATE SET state=excluded.state,document_id=excluded.document_id,error=excluded.error,updated_at=CURRENT_TIMESTAMP`, f.State, f.DocumentID, f.Error, f.PaperID, scope.ProjectID, scope.UserID)
	return dbError(err)
}
func (s *Store) GetDocumentFetch(ctx context.Context, scope domain.Scope, paperID string) (domain.DocumentFetch, error) {
	var f domain.DocumentFetch
	err := s.db.QueryRowContext(ctx, `SELECT paper_id,state,document_id,error,updated_at FROM document_fetches WHERE paper_id=? AND project_id=? AND owner_id=?`, paperID, scope.ProjectID, scope.UserID).Scan(&f.PaperID, &f.State, &f.DocumentID, &f.Error, &f.UpdatedAt)
	return f, dbError(err)
}
