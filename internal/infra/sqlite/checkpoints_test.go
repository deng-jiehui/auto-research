package sqlite

import (
	"context"
	"errors"
	"testing"

	"github.com/deng-jiehui/auto-research/internal/domain"
	"github.com/goharness/goharness/pkg/schema"
	"github.com/goharness/goharness/pkg/state"
)

func TestCheckpointStorePersistsAcrossReopenAndRejectsStaleSave(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, "alice", "检查点项目")
	if err != nil {
		t.Fatal(err)
	}
	scope := domain.Scope{UserID: "alice", ProjectID: project.ID}
	session, err := store.CreateSession(ctx, scope, "会话")
	if err != nil {
		t.Fatal(err)
	}
	turn, err := store.AdmitTurn(ctx, scope, session.ID, domain.TurnDraft{InputText: "目标", IdempotencyKey: "cp-1", DefinitionVersion: "v1", ContextJSON: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	checkpoints := NewCheckpointStore(store, scope, session.ID, turn.Turn.ID)
	first, err := checkpoints.Save(ctx, state.Checkpoint{ConversationID: "autoresearch:" + turn.Turn.ID, Messages: []schema.Message{schema.UserMessage("目标")}, Values: map[string]any{"step": 1}, Status: state.StatusInterrupted})
	if err != nil {
		t.Fatal(err)
	}
	stale := first.Clone()
	second, err := checkpoints.Save(ctx, first)
	if err != nil || second.Version != 2 {
		t.Fatalf("第二版检查点=%+v err=%v", second, err)
	}
	if _, err = checkpoints.Save(ctx, stale); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("旧检查点未冲突: %v", err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	loaded, found, err := NewCheckpointStore(store, scope, session.ID, turn.Turn.ID).Load(ctx, "autoresearch:"+turn.Turn.ID)
	if err != nil || !found || loaded.Version != 2 || loaded.Values["step"] != float64(1) {
		t.Fatalf("重启后检查点=%+v found=%v err=%v", loaded, found, err)
	}
}

func TestCheckpointStoreSeparatesNestedAgentCheckpoints(t *testing.T) {
	store, ctx, scope, session, turn := runtimeFixture(t)
	checkpoints := NewCheckpointStore(store, scope, session.ID, turn.ID)
	rootKey := "autoresearch:" + turn.ID
	childKey := rootKey + "/run-1/task-3@0/search-child#0"
	root, err := checkpoints.Save(ctx, state.Checkpoint{ConversationID: rootKey, Values: map[string]any{"kind": "root"}})
	if err != nil {
		t.Fatal(err)
	}
	child, err := checkpoints.Save(ctx, state.Checkpoint{ConversationID: childKey, Values: map[string]any{"kind": "child"}})
	if err != nil {
		t.Fatalf("无法保存子 Agent 检查点: %v", err)
	}
	stale := child.Clone()
	child, err = checkpoints.Save(ctx, child)
	if err != nil || child.Version != 2 {
		t.Fatalf("子检查点独立版本=%d err=%v", child.Version, err)
	}
	if _, err = checkpoints.Save(ctx, stale); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("子检查点旧版本未冲突: %v", err)
	}
	reopened := NewCheckpointStore(store, scope, session.ID, turn.ID)
	loaded, found, err := reopened.Load(ctx, childKey)
	if err != nil || !found || loaded.ID != child.ID || loaded.Values["kind"] != "child" {
		t.Fatalf("重建适配器后无法读取子检查点: %+v found=%v err=%v", loaded, found, err)
	}
	items, err := reopened.List(ctx, childKey, state.ListOptions{})
	if err != nil || len(items) != 2 || items[0].ID != child.ID {
		t.Fatalf("子检查点历史=%+v err=%v", items, err)
	}
	if _, found, err = reopened.Get(ctx, rootKey, child.ID); err != nil || found {
		t.Fatalf("父命名空间读到了子检查点: found=%v err=%v", found, err)
	}
	if err = reopened.Delete(ctx, childKey); err != nil {
		t.Fatal(err)
	}
	if _, found, err = reopened.Load(ctx, childKey); err != nil || found {
		t.Fatalf("子检查点删除失败: found=%v err=%v", found, err)
	}
	loaded, found, err = reopened.Load(ctx, rootKey)
	if err != nil || !found || loaded.ID != root.ID || loaded.Version != 1 {
		t.Fatalf("子检查点影响了根检查点: %+v found=%v err=%v", loaded, found, err)
	}
	for _, key := range []string{rootKey + "other/child", "autoresearch:other/child", rootKey + "/", rootKey + "/../other", rootKey + "/child//task"} {
		if _, _, err = reopened.Load(ctx, key); !errors.Is(err, state.ErrInvalidCheckpoint) {
			t.Fatalf("越界或空命名空间没有被拒绝: key=%q err=%v", key, err)
		}
		if _, err = reopened.Save(ctx, state.Checkpoint{ConversationID: key}); !errors.Is(err, state.ErrInvalidCheckpoint) {
			t.Fatalf("保存越界或空命名空间没有被拒绝: key=%q err=%v", key, err)
		}
	}
}
