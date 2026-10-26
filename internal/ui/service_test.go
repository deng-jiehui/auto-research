package ui

import (
	"errors"
	"testing"

	"github.com/deng-jiehui/auto-research/internal/app"
	"github.com/deng-jiehui/auto-research/internal/domain"
	"github.com/deng-jiehui/auto-research/internal/infra/mcp"
	"github.com/deng-jiehui/auto-research/internal/infra/sqlite"
	"github.com/goharness/goharness/pkg/state"
)

func uiTestRuntime(store *sqlite.Store) *app.TurnService {
	academic := app.NewAcademicService(mcp.NewFixtureSource())
	papers := app.NewPaperService(store)
	documents := app.NewDocumentService(store, papers, academic)
	turns := app.NewTurnService(store, store, store)
	research := app.NewResearchService(store)
	turns.SetResearchService(research)
	turns.SetAcademicTools(app.AcademicToolsDeps{Academic: academic, Papers: papers, Evidence: app.NewEvidenceService(store), Documents: documents})
	turns.SetResearchResources(nil, &app.ProjectResources{Projects: store, Research: research, Papers: papers, Documents: documents})
	turns.SetCheckpointFactory(func(scope domain.Scope, sid, tid string) state.Store {
		return sqlite.NewCheckpointStore(store, scope, sid, tid)
	})
	return turns
}

func TestResearchUIUsesBoundTargetAndDurableAnswers(t *testing.T) {
	ctx := t.Context()
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	project, err := store.CreateProject(ctx, "alice", "UI 协议")
	if err != nil {
		t.Fatal(err)
	}
	scope := domain.Scope{UserID: "alice", ProjectID: project.ID}
	turns := uiTestRuntime(store)
	created, err := turns.StartResearch(ctx, scope, app.StartResearchInput{ResearchSpec: domain.ResearchSpec{Question: "比较单卡预测基线"}, IdempotencyKey: "create"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = turns.RunQueued(ctx); err != nil {
		t.Fatal(err)
	}
	_, err = store.SaveNote(ctx, scope, domain.Note{Text: "不应自动进入研究的共享笔记", ToolKey: "unrelated"})
	if err != nil {
		t.Fatal(err)
	}
	screen := NewService(turns)
	target := Target{Kind: "research", ID: created.Run.ID}
	view, err := screen.Snapshot(ctx, scope, target, 100)
	if err != nil {
		t.Fatal(err)
	}
	if view.UI.Interaction == nil || view.UI.Interaction.Kind != "research_plan" || view.Research == nil || view.Research.ID != target.ID || len(view.Notes) != 0 {
		t.Fatalf("研究目标投影或隔离错误: %+v", view)
	}
	prompt := view.UI.Interaction
	input := app.ResumeInput{Revision: view.Turn.Revision, IdempotencyKey: "approve", Answers: []app.AnswerInput{{InterruptID: prompt.ID, ResearchPlan: &domain.ResearchPlanReview{RunID: prompt.RunID, PlanRevision: prompt.Revision, PlanHash: prompt.Hash, Decision: "approve", Plan: *prompt.Plan}}}}
	if _, err = screen.Respond(ctx, scope, target, "another-turn", input); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("接受了其他任务轮次: %v", err)
	}
	foreign := domain.Scope{UserID: "alice", ProjectID: "another-project"}
	if _, err = screen.Snapshot(ctx, foreign, target, 100); !errors.Is(err, domain.ErrNotFound) && !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("跨项目读取未拒绝: %v", err)
	}
	if _, err = screen.Respond(ctx, foreign, target, view.Turn.ID, input); !errors.Is(err, domain.ErrNotFound) && !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("跨项目回答未拒绝: %v", err)
	}
	result, err := screen.Respond(ctx, scope, target, view.Turn.ID, input)
	if err != nil || result.Replayed {
		t.Fatalf("回答失败: %+v %v", result, err)
	}
	replayed, err := screen.Respond(ctx, scope, target, view.Turn.ID, input)
	if err != nil || !replayed.Replayed || replayed.ActionID != result.ActionID {
		t.Fatalf("回答重放失败: %+v %v", replayed, err)
	}
	turns = uiTestRuntime(store)
	screen = NewService(turns)
	if _, err = turns.RunQueued(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := screen.Snapshot(ctx, scope, target, 100)
	if err != nil || after.UI.Interaction == nil || after.UI.Interaction.Kind != "research_candidates" {
		t.Fatalf("重建后未继续原任务: %+v %v", after.UI, err)
	}
	events, err := screen.Events(ctx, scope, target, 0, 100)
	if err != nil || len(events) == 0 {
		t.Fatalf("缺少研究事件: %+v %v", events, err)
	}
	for _, event := range events {
		if event.SessionID != created.Run.SessionID {
			t.Fatalf("事件混入其他会话: %+v", event)
		}
	}
	last := events[len(events)-1].ID
	if remaining, err := screen.Events(ctx, scope, target, last, 100); err != nil || len(remaining) != 0 {
		t.Fatalf("事件游标重复读取: %+v %v", remaining, err)
	}
	if _, err = screen.Act(ctx, scope, target, after.Turn.ID, "cancel", app.ResearchControlInput{TurnActionInput: app.TurnActionInput{Revision: after.Turn.Revision, IdempotencyKey: "cancel"}}); err != nil {
		t.Fatal(err)
	}
	final, err := screen.Snapshot(ctx, scope, target, 100)
	if err != nil || final.Turn.State != domain.TurnCancelled || final.UI.Interaction != nil {
		t.Fatalf("取消后仍显示交互: %+v %v", final.UI, err)
	}
}
