package artifacts

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/deng-jiehui/auto-research/internal/domain"
)

func TestArtifactIsolationConcurrentReuseAndCorruption(t *testing.T) {
	store := &Store{Root: t.TempDir()}
	scope := domain.Scope{UserID: "alice", ProjectID: "p1"}
	ctx := context.Background()
	id, err := store.Put(ctx, scope, []byte("冻结正文"))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			again, putErr := store.Put(ctx, scope, []byte("冻结正文"))
			if putErr != nil || again != id {
				t.Errorf("并发复用: %s %v", again, putErr)
			}
		}()
	}
	wg.Wait()
	if _, err = store.Read(ctx, domain.Scope{UserID: "alice", ProjectID: "p2"}, id); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("跨项目产物可读: %v", err)
	}
	if _, err = store.Read(ctx, scope, "../outside"); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("无效身份未拒绝: %v", err)
	}
	path, err := store.path(scope, id)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("损坏正文"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Read(ctx, scope, id); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("未识别正文损坏: %v", err)
	}
}
