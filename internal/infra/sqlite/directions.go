package sqlite

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/deng-jiehui/auto-research/internal/domain"
)

func decodeDirection(text string, err error) (domain.Direction, error) {
	var item domain.Direction
	if err != nil {
		return item, dbError(err)
	}
	err = json.Unmarshal([]byte(text), &item)
	return item, err
}
func (s *Store) SaveDirection(ctx context.Context, scope domain.Scope, item domain.Direction, revision int64, key string) (domain.Direction, error) {
	if item.ProjectID != scope.ProjectID || scope.UserID == "" {
		return domain.Direction{}, domain.ErrInvalid
	}
	request, err := json.Marshal(item)
	if err != nil {
		return domain.Direction{}, err
	}
	digest := sha256.Sum256(request)
	hash := hex.EncodeToString(digest[:])
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Direction{}, dbError(err)
	}
	defer tx.Rollback()
	if key != "" {
		var saved, savedHash string
		err = tx.QueryRowContext(ctx, `SELECT content_json,request_hash FROM direction_submissions WHERE owner_id=? AND project_id=? AND submission_key=?`, scope.UserID, scope.ProjectID, key).Scan(&saved, &savedHash)
		if err == nil {
			if hash != savedHash {
				return domain.Direction{}, domain.ErrConflict
			}
			return decodeDirection(saved, nil)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return domain.Direction{}, dbError(err)
		}
	}
	var exists int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM projects WHERE id=? AND owner_id=?`, scope.ProjectID, scope.UserID).Scan(&exists); err != nil {
		return domain.Direction{}, dbError(err)
	}
	if exists == 0 {
		return domain.Direction{}, domain.ErrNotFound
	}
	item.Revision = revision + 1
	if revision == 0 {
		item.ID = rand.Text()
	}
	encoded, err := json.Marshal(item)
	if err != nil {
		return domain.Direction{}, err
	}
	if revision == 0 {
		_, err = tx.ExecContext(ctx, `INSERT INTO directions(id,owner_id,project_id,revision,state,content_json) VALUES(?,?,?,?,?,?)`, item.ID, scope.UserID, scope.ProjectID, item.Revision, item.State, string(encoded))
	} else {
		var result sql.Result
		result, err = tx.ExecContext(ctx, `UPDATE directions SET revision=?,state=?,content_json=? WHERE id=? AND owner_id=? AND project_id=? AND revision=?`, item.Revision, item.State, string(encoded), item.ID, scope.UserID, scope.ProjectID, revision)
		if err == nil {
			count, countErr := result.RowsAffected()
			if countErr != nil {
				return domain.Direction{}, dbError(countErr)
			}
			if count == 0 {
				return domain.Direction{}, domain.ErrConflict
			}
		}
	}
	if err != nil {
		return domain.Direction{}, dbError(err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO direction_revisions(direction_id,revision,content_json) VALUES(?,?,?)`, item.ID, item.Revision, string(encoded)); err != nil {
		return domain.Direction{}, dbError(err)
	}
	if key != "" {
		if _, err = tx.ExecContext(ctx, `INSERT INTO direction_submissions(owner_id,project_id,submission_key,request_hash,direction_id,content_json) VALUES(?,?,?,?,?,?)`, scope.UserID, scope.ProjectID, key, hash, item.ID, string(encoded)); err != nil {
			return domain.Direction{}, dbError(err)
		}
	}
	if err = tx.Commit(); err != nil {
		return domain.Direction{}, dbError(err)
	}
	return item, nil
}
func (s *Store) GetDirection(ctx context.Context, scope domain.Scope, id string) (domain.Direction, error) {
	var text string
	err := s.db.QueryRowContext(ctx, `SELECT content_json FROM directions WHERE id=? AND project_id=? AND owner_id=?`, id, scope.ProjectID, scope.UserID).Scan(&text)
	return decodeDirection(text, err)
}
func (s *Store) ListDirections(ctx context.Context, scope domain.Scope) ([]domain.Direction, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT content_json FROM directions WHERE project_id=? AND owner_id=? ORDER BY rowid DESC`, scope.ProjectID, scope.UserID)
	if err != nil {
		return nil, dbError(err)
	}
	defer rows.Close()
	items := make([]domain.Direction, 0)
	for rows.Next() {
		var text string
		if err = rows.Scan(&text); err != nil {
			return nil, dbError(err)
		}
		item, err := decodeDirection(text, nil)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, dbError(rows.Err())
}
func (s *Store) DirectionHistory(ctx context.Context, scope domain.Scope, id string) ([]domain.Direction, error) {
	if _, err := s.GetDirection(ctx, scope, id); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT content_json FROM direction_revisions WHERE direction_id=? ORDER BY revision`, id)
	if err != nil {
		return nil, dbError(err)
	}
	defer rows.Close()
	items := make([]domain.Direction, 0)
	for rows.Next() {
		var text string
		if err = rows.Scan(&text); err != nil {
			return nil, dbError(err)
		}
		item, err := decodeDirection(text, nil)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, dbError(rows.Err())
}
