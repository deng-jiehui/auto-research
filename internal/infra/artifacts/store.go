package artifacts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/deng-jiehui/auto-research/internal/domain"
)

// Store 按可信项目与内容哈希保存不可变产物，不接受外部文件路径。
type Store struct{ Root string }

func (s *Store) path(scope domain.Scope, id string) (string, error) {
	if scope.UserID == "" || scope.ProjectID == "" || len(id) != 64 {
		return "", domain.ErrInvalid
	}
	if _, err := hex.DecodeString(id); err != nil || strings.ToLower(id) != id {
		return "", domain.ErrInvalid
	}
	owner := sha256.Sum256([]byte(scope.UserID + "\x00" + scope.ProjectID))
	root, err := filepath.Abs(s.Root)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, hex.EncodeToString(owner[:]))
	// 逐级拒绝符号链接，避免外置正文越过产物根目录。
	for current := dir; ; current = filepath.Dir(current) {
		info, statErr := os.Lstat(current)
		if statErr == nil && info.Mode()&os.ModeSymlink != 0 {
			return "", domain.ErrForbidden
		}
		if statErr != nil && !os.IsNotExist(statErr) {
			return "", statErr
		}
		if current == filepath.Dir(current) {
			break
		}
	}
	path := filepath.Join(dir, id)
	if info, err := os.Lstat(path); err == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
		return "", domain.ErrForbidden
	}
	return path, nil
}

func (s *Store) Put(ctx context.Context, scope domain.Scope, data []byte) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if len(data) > 32<<20 {
		return "", fmt.Errorf("%w：产物超过 32 MiB", domain.ErrInvalid)
	}
	hash := sha256.Sum256(data)
	id := hex.EncodeToString(hash[:])
	path, err := s.path(scope, id)
	if err != nil {
		return "", err
	}
	if existing, readErr := s.Read(ctx, scope, id); readErr == nil && string(existing) == string(data) {
		return id, nil
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".artifact-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(temp.Name())
	if _, err = temp.Write(data); err != nil {
		temp.Close()
		return "", err
	}
	if err = temp.Sync(); err != nil {
		temp.Close()
		return "", err
	}
	if err = temp.Close(); err != nil {
		return "", err
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	if err = os.Rename(temp.Name(), path); err != nil {
		if _, readErr := s.Read(ctx, scope, id); readErr != nil {
			return "", err
		}
	}
	return id, nil
}

func (s *Store) Read(ctx context.Context, scope domain.Scope, id string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, err := s.path(scope, id)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if info.Size() > 32<<20 {
		return nil, domain.ErrInvalid
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(data)
	if hex.EncodeToString(hash[:]) != id {
		return nil, fmt.Errorf("%w：产物校验和不匹配", domain.ErrInvalid)
	}
	return data, nil
}
