package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/deng-jiehui/auto-research/internal/app"
	"github.com/deng-jiehui/auto-research/internal/domain"
	"github.com/deng-jiehui/auto-research/internal/infra/sqlite"
)

func TestDirectionApprovalBindsRevision(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := sqlite.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	project, err := store.CreateProject(ctx, "alice", "方向管理")
	if err != nil {
		t.Fatal(err)
	}
	scope := domain.Scope{UserID: "alice", ProjectID: project.ID}
	service := app.NewDirectionService(store, store)
	proposal := domain.DirectionDraft{Name: "小样本时序评测", Description: "比较统一划分下的预测性能", Questions: []string{"线性基线是否仍有竞争力？"}, Gap: "待验证假设", Feasibility: "单卡可运行"}
	first, err := service.Propose(ctx, scope, proposal, "session-a", "turn-a", "submission-a")
	if err != nil || first.State != "proposed" || !first.EvidenceInsufficient {
		t.Fatalf("初始提案: %+v %v", first, err)
	}
	replay, err := service.Propose(ctx, scope, proposal, "session-a", "turn-a", "submission-a")
	if err != nil || replay.ID != first.ID || replay.Revision != first.Revision {
		t.Fatalf("提案重放: %+v %v", replay, err)
	}
	proposal.Name = "统一划分的少样本预测"
	edited, err := service.Edit(ctx, scope, first.ID, first.Revision, proposal)
	if err != nil || edited.Revision <= first.Revision {
		t.Fatalf("编辑: %+v %v", edited, err)
	}
	if _, err = service.Decide(ctx, scope, first.ID, first.Revision, "accepted"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("旧确认没有失效: %v", err)
	}
	accepted, err := service.Decide(ctx, scope, first.ID, edited.Revision, "accepted")
	if err != nil || accepted.State != "accepted" {
		t.Fatalf("确认: %+v %v", accepted, err)
	}
	if _, err = service.Get(ctx, domain.Scope{UserID: "bob", ProjectID: project.ID}, first.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("跨用户可读: %v", err)
	}
	store.Close()
	store, err = sqlite.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service = app.NewDirectionService(store, store)
	history, err := service.History(ctx, scope, first.ID)
	if err != nil || len(history) != 3 || history[0].Name != first.Name || history[2].State != "accepted" {
		t.Fatalf("重启后历史: %+v %v", history, err)
	}
}

func TestDirectionRejectsForeignPaper(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	first, err := store.CreateProject(ctx, "alice", "项目一")
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateProject(ctx, "alice", "项目二")
	if err != nil {
		t.Fatal(err)
	}
	paper, err := store.ImportPaper(ctx, domain.Scope{UserID: "alice", ProjectID: other.ID}, domain.Paper{SourceKey: "arxiv:2401.00001", Title: "他项目论文"})
	if err != nil {
		t.Fatal(err)
	}
	service := app.NewDirectionService(store, store)
	_, err = service.Propose(ctx, domain.Scope{UserID: "alice", ProjectID: first.ID}, domain.DirectionDraft{Name: "越界方向", Description: "不能引用其他项目", PaperIDs: []string{paper.ID}}, "", "", "foreign")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("跨项目论文未拒绝: %v", err)
	}
}
