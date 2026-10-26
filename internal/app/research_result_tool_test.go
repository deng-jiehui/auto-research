package app

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/deng-jiehui/auto-research/internal/domain"
	"github.com/deng-jiehui/auto-research/internal/infra/sqlite"
	"github.com/goharness/goharness/pkg/tool"
)

func TestResearchResultToolOnlyProposesDirection(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	project, err := store.CreateProject(ctx, "alice", "提案工具")
	if err != nil {
		t.Fatal(err)
	}
	scope := domain.Scope{UserID: "alice", ProjectID: project.ID}
	service := NewDirectionService(store, store)
	entry := newResearchResultTool(ResearchGraphDeps{Scope: scope, TurnID: "turn-one", SessionID: "session-one", Directions: service})
	for _, raw := range []string{`{"kind":"report","direction":{"name":"越界类型","description":"尚未开放"}}`, `{"kind":"direction","direction":{"name":"伪造批准","description":"不允许","state":"accepted"}}`} {
		result, invokeErr := entry.Invoke(ctx, tool.CallInput{RawArgs: raw})
		if invokeErr == nil && !result.IsError {
			t.Fatalf("不合法参数被接受: %s", raw)
		}
	}
	raw := `{"kind":"direction","direction":{"name":"时间序列基线研究","description":"比较多变量预测模型","questions":["是否需要复杂模型？"]}}`
	for range 2 {
		result, err := entry.Invoke(ctx, tool.CallInput{RawArgs: raw})
		if err != nil || result.IsError {
			t.Fatalf("提案失败: %+v %v", result, err)
		}
	}
	items, err := service.List(ctx, scope)
	if err != nil || len(items) != 1 || items[0].State != "proposed" {
		t.Fatalf("工具绕过审核或重复保存: %+v %v", items, err)
	}
	readTools := toolMap(NewWorkspaceReadTools(WorkspacePolicy{}, &ProjectResources{Scope: scope, Projects: store, Directions: service}))
	args, _ := json.Marshal(ReadFileArgs{Path: "research://directions/" + items[0].ID + ".json"})
	result, err := readTools["read_file"].Invoke(ctx, tool.CallInput{RawArgs: string(args)})
	if err != nil || result.IsError {
		t.Fatalf("提交后不能复用 read_file: %+v %v", result, err)
	}
}
