package sqlite

import (
	"errors"
	"testing"

	"github.com/deng-jiehui/auto-research/internal/domain"
	"github.com/goharness/goharness/pkg/state"
)

func TestIndependentResearchAtomicCreationAndScope(t *testing.T) {
	s, ctx, scope, session, chat := runtimeFixture(t)
	draft := domain.ResearchTaskDraft{Spec: domain.ResearchSpec{Question: "独立研究"}, OriginSessionID: session.ID, OperationID: "independent", ContextJSON: []byte(`{}`), MessageJSON: []byte(`{"role":"user","content_blocks":[{"type":"text","text":{"text":"独立研究"}}]}`), DefinitionVersion: "autoresearch-agent-single-v7"}
	if _, err := s.db.ExecContext(ctx, `CREATE TRIGGER fail_task BEFORE INSERT ON research_tasks BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateResearchTask(ctx, scope, draft); err == nil {
		t.Fatal("映射故障未传播")
	}
	var n int
	for _, table := range []string{"sessions", "turns", "research_runs", "research_tasks", "messages"} {
		query := "SELECT count(*) FROM " + table
		if table == "sessions" {
			query += " WHERE kind='research'"
		} else if table == "turns" {
			query += " WHERE execution_kind='research'"
		} else if table == "messages" {
			query += " WHERE turn_id IN (SELECT id FROM turns WHERE execution_kind='research')"
		}
		if err := s.db.QueryRowContext(ctx, query).Scan(&n); err != nil || n != 0 {
			t.Fatalf("事务回滚留下 %s=%d: %v", table, n, err)
		}
	}
	_, _ = s.db.ExecContext(ctx, `DROP TRIGGER fail_task`)
	created, err := s.CreateResearchTask(ctx, scope, draft)
	if err != nil {
		t.Fatal(err)
	}
	foreign := domain.Scope{UserID: "other", ProjectID: scope.ProjectID}
	if _, err = s.GetResearch(ctx, foreign, created.Run.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("跨用户读取任务: %v", err)
	}
	if _, err = s.ResearchTaskSummaries(ctx, foreign, session.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("跨用户读取摘要: %v", err)
	}
	otherProject, _ := s.CreateProject(ctx, scope.UserID, "其他项目")
	otherScope := domain.Scope{UserID: scope.UserID, ProjectID: otherProject.ID}
	if _, err = s.CreateResearchTask(ctx, otherScope, draft); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("接受跨项目来源: %v", err)
	}
	if _, err = s.ResearchTaskSummaries(ctx, otherScope, session.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("跨项目读取摘要: %v", err)
	}
	claimed, found, err := s.ClaimQueuedKind(ctx, "research")
	if err != nil || !found || claimed.ID != created.Turn.ID {
		t.Fatalf("研究队列领错任务: %+v %v", claimed, err)
	}
	chatClaim, found, err := s.ClaimQueuedKind(ctx, "chat")
	if err != nil || !found || chatClaim.ID != chat.ID {
		t.Fatalf("普通队列领错任务: %+v %v", chatClaim, err)
	}
	// 重试必须能找到初始用户消息，而不能把 Agent 的最终回复当成用户输入。
	if err = s.SetTurnState(ctx, scope, claimed.ID, domain.TurnFailed, "test", "模拟失败"); err != nil {
		t.Fatal(err)
	}
	turn, _ := s.GetTurn(ctx, scope, claimed.SessionID, claimed.ID)
	if _, err = s.ApplyAction(ctx, scope, turn.SessionID, turn.ID, domain.ActionDraft{Kind: "retry", Revision: turn.Revision, IdempotencyKey: "retry", RequestHash: "retry", PayloadJSON: []byte(`{}`)}); err != nil {
		t.Fatalf("独立任务不能重试: %v", err)
	}
}

func TestDeleteResearchTaskCascadesInternalSessionAndPreservesPaper(t *testing.T) {
	s, ctx, scope, source, _ := runtimeFixture(t)
	paper, err := s.ImportPaper(ctx, scope, domain.Paper{Title: "保留论文", SourceKey: "paper:keep"})
	if err != nil {
		t.Fatal(err)
	}
	draft := domain.ResearchTaskDraft{
		Spec:              domain.ResearchSpec{Question: "删除已结束调研", PaperIDs: []string{paper.ID}},
		OriginSessionID:   source.ID,
		OperationID:       "delete-research",
		ContextJSON:       []byte(`{}`),
		MessageJSON:       []byte(`{"role":"user","content_blocks":[]}`),
		DefinitionVersion: "autoresearch-agent-single-v8",
	}
	created, err := s.CreateResearchTask(ctx, scope, draft)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveResearchPlan(ctx, scope, created.Turn.ID, "delete-plan", 0, domain.ResearchPlan{Summary: "待删除计划", Queries: []string{"baseline"}}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReserveResearchCall(ctx, scope, created.Turn.ID, "model"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AppendEvent(ctx, scope, domain.SessionEvent{SessionID: created.Run.SessionID, TurnID: created.Turn.ID, Kind: "test", PayloadJSON: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	checkpoints := NewCheckpointStore(s, scope, created.Run.SessionID, created.Turn.ID)
	for _, suffix := range []string{"", "/child"} {
		if _, err = checkpoints.Save(ctx, state.Checkpoint{ConversationID: "autoresearch:" + created.Turn.ID + suffix, Values: map[string]any{"test": true}}); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.SetTurnState(ctx, scope, created.Turn.ID, domain.TurnCompleted, "", ""); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteResearch(ctx, scope, created.Run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetResearch(ctx, scope, created.Run.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("删除后仍可读取调研: %v", err)
	}
	if _, err = s.GetSession(ctx, scope, created.Run.SessionID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("删除后内部调研会话仍存在: %v", err)
	}
	if _, err = s.GetPaper(ctx, scope, paper.ID); err != nil {
		t.Fatalf("删除调研误删论文库资料: %v", err)
	}
	if _, err = s.GetSession(ctx, scope, source.ID); err != nil {
		t.Fatalf("删除调研误删来源会话: %v", err)
	}
	for _, table := range []string{"research_runs", "research_tasks", "research_operations", "research_plan_versions", "research_attempts", "checkpoints", "checkpoint_heads", "events", "messages"} {
		var count int
		if err = s.db.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("删除后留下 %s=%d: %v", table, count, err)
		}
	}
}

func TestDeleteResearchRejectsActiveTaskAndForeignScope(t *testing.T) {
	s, ctx, scope, source, _ := runtimeFixture(t)
	draft := domain.ResearchTaskDraft{
		Spec:              domain.ResearchSpec{Question: "不能删除执行中调研"},
		OriginSessionID:   source.ID,
		OperationID:       "delete-active-research",
		ContextJSON:       []byte(`{}`),
		MessageJSON:       []byte(`{"role":"user","content_blocks":[]}`),
		DefinitionVersion: "autoresearch-agent-single-v8",
	}
	created, err := s.CreateResearchTask(ctx, scope, draft)
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{domain.TurnQueued, domain.TurnRunning, domain.TurnAwaitingInput, domain.TurnRecoveryRequired} {
		if err = s.SetTurnState(ctx, scope, created.Turn.ID, status, "", ""); err != nil {
			t.Fatal(err)
		}
		if err = s.DeleteResearch(ctx, scope, created.Run.ID); !errors.Is(err, domain.ErrBusy) {
			t.Fatalf("%s 任务删除错误=%v, want ErrBusy", status, err)
		}
	}
	if _, err = s.GetResearch(ctx, scope, created.Run.ID); err != nil {
		t.Fatalf("拒绝删除后任务不存在: %v", err)
	}
	foreign := domain.Scope{UserID: "other", ProjectID: scope.ProjectID}
	if err = s.DeleteResearch(ctx, foreign, created.Run.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("跨用户删除错误=%v, want ErrNotFound", err)
	}
}

func TestDeleteLegacyResearchPreservesChatAndCheckpoint(t *testing.T) {
	s, ctx, scope, session, turn := runtimeFixture(t)
	run, err := s.SaveResearchPlan(ctx, scope, turn.ID, "legacy-plan", 0, domain.ResearchPlan{Summary: "旧会话调研", Queries: []string{"baseline"}})
	if err != nil {
		t.Fatal(err)
	}
	checkpoints := NewCheckpointStore(s, scope, session.ID, turn.ID)
	key := "autoresearch:" + turn.ID
	if _, err = checkpoints.Save(ctx, state.Checkpoint{ConversationID: key, Values: map[string]any{"test": true}}); err != nil {
		t.Fatal(err)
	}
	if err = s.SetTurnState(ctx, scope, turn.ID, domain.TurnFailed, "test", "失败任务"); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteResearch(ctx, scope, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetResearch(ctx, scope, run.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("旧调研未删除: %v", err)
	}
	if _, err = s.GetTurn(ctx, scope, session.ID, turn.ID); err != nil {
		t.Fatalf("误删旧会话执行记录: %v", err)
	}
	if _, found, loadErr := checkpoints.Load(ctx, key); loadErr != nil || !found {
		t.Fatalf("误删旧会话 checkpoint: found=%v, err=%v", found, loadErr)
	}
}
