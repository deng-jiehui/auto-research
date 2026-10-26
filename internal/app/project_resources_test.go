package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/deng-jiehui/auto-research/internal/domain"
	"github.com/deng-jiehui/auto-research/internal/infra/sqlite"
	"github.com/goharness/goharness/pkg/tool"
)

func TestReadFileSupportsProjectResourceAndKeepsWorkspacePolicy(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	project, err := store.CreateProject(ctx, "alice", "资料读取")
	if err != nil {
		t.Fatal(err)
	}
	scope := domain.Scope{UserID: "alice", ProjectID: project.ID}
	directions := NewDirectionService(store, store)
	proposal, err := directions.Propose(ctx, scope, domain.DirectionDraft{Name: "统一预测评测", Description: "研究原文与方向的联系"}, "", "", "proposal")
	if err != nil {
		t.Fatal(err)
	}
	resources := &ProjectResources{Scope: scope, Projects: store, Directions: directions, Papers: NewPaperService(store)}
	root := t.TempDir()
	writeTestWorkspaceFile(t, root, "notes.txt", "原工作区\n")
	tools := toolMap(NewWorkspaceReadTools(WorkspacePolicy{Root: root}, resources))
	for _, address := range []string{"research://index.json", "research://directions/" + proposal.ID + ".json", "notes.txt"} {
		args, _ := json.Marshal(ReadFileArgs{Path: address})
		result, err := tools["read_file"].Invoke(ctx, tool.CallInput{RawArgs: string(args)})
		if err != nil {
			t.Fatal(err)
		}
		var output ReadFileResult
		decodeToolData(t, result, &output)
		if output.Content == "" || output.SHA256 == "" {
			t.Fatalf("读取无内容或版本: %+v", output)
		}
	}
	listed, err := tools["list_directory"].Invoke(ctx, tool.CallInput{RawArgs: `{"path":"research://directions"}`})
	if err != nil {
		t.Fatal(err)
	}
	var listing ListDirectoryResult
	decodeToolData(t, listed, &listing)
	found := false
	for _, entry := range listing.Entries {
		if entry.Path == "research://directions/"+proposal.ID+".json" {
			found = true
		}
	}
	if !found {
		t.Fatalf("目录没有返回可读取的方向地址: %+v", listing)
	}
	searched, err := tools["search_files"].Invoke(ctx, tool.CallInput{RawArgs: `{"path":"research://directions","query":"统一预测评测","glob":"*.json"}`})
	if err != nil {
		t.Fatal(err)
	}
	var matches SearchFilesResult
	decodeToolData(t, searched, &matches)
	if len(matches.Matches) < 1 {
		t.Fatal("逻辑资料搜索未命中")
	}
	if _, err = resources.Read(ctx, "research://directions/../index.json", ReadFileArgs{}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("逻辑路径穿越: %v", err)
	}
	if _, err = readWorkspaceFile(WorkspacePolicy{Root: root}, ReadFileArgs{Path: "../outside.txt"}); !errors.Is(err, ErrWorkspacePath) {
		t.Fatalf("原边界失效: %v", err)
	}
	if _, err = writeWorkspaceFile(ctx, WorkspacePolicy{Root: root}, WriteFileArgs{Path: "research://index.json", Content: "{}", Create: true}, nil); err == nil {
		t.Fatal("逻辑资料不能用文件写入改写")
	}
	foreign, err := store.CreateProject(ctx, "alice", "其他项目")
	if err != nil {
		t.Fatal(err)
	}
	other := *resources
	other.Scope.ProjectID = foreign.ID
	if _, err = other.Read(ctx, "research://directions/"+proposal.ID+".json", ReadFileArgs{}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("跨项目方向可读: %v", err)
	}
}

func TestResourcePaginationBindsRevision(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	project, err := store.CreateProject(ctx, "alice", "分页")
	if err != nil {
		t.Fatal(err)
	}
	scope := domain.Scope{UserID: "alice", ProjectID: project.ID}
	directions := NewDirectionService(store, store)
	for i := range 21 {
		if _, err = directions.Propose(ctx, scope, domain.DirectionDraft{Name: "方向", Description: "描述"}, "", "", string(rune('a'+i))); err != nil {
			t.Fatal(err)
		}
	}
	resources := &ProjectResources{Scope: scope, Projects: store, Directions: directions}
	first, err := resources.Read(ctx, "research://directions/index.json", ReadFileArgs{})
	if err != nil {
		t.Fatal(err)
	}
	var page struct {
		Next string `json:"next"`
	}
	if err = json.Unmarshal([]byte(first.Content), &page); err != nil || !strings.Contains(page.Next, "revision=") {
		t.Fatalf("后续页未绑定版本: %+v %v", page, err)
	}
	if _, err = resources.Read(ctx, page.Next, ReadFileArgs{}); err != nil {
		t.Fatal(err)
	}
	if _, err = directions.Propose(ctx, scope, domain.DirectionDraft{Name: "新方向", Description: "新描述"}, "", "", "new"); err != nil {
		t.Fatal(err)
	}
	if _, err = resources.Read(ctx, page.Next, ReadFileArgs{}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("变化后的旧页应冲突: %v", err)
	}
}
