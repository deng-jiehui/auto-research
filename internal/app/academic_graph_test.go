package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/deng-jiehui/auto-research/internal/domain"
	"github.com/deng-jiehui/auto-research/internal/infra/mcp"
	"github.com/deng-jiehui/auto-research/internal/infra/sqlite"
	"github.com/goharness/goharness/pkg/graph"
	"github.com/goharness/goharness/pkg/llm"
	"github.com/goharness/goharness/pkg/schema"
	"github.com/goharness/goharness/pkg/state"
	"github.com/goharness/goharness/pkg/stream"
	"github.com/goharness/goharness/pkg/tool"
)

// 检索结论已有完整工具上下文，不能再由无工具上下文的模型改写。
func TestAcademicGraphPreservesToolBasedReply(t *testing.T) {
	for _, tc := range []struct {
		name, query, reply string
		sourceUnavailable  bool
		save               bool
	}{
		{name: "success", query: "fixture", reply: "已通过论文工具检索到 [Fixture Research](https://arxiv.org/abs/2401.00001)。仅阅读摘要，没有归档。"},
		{name: "empty", query: "no-such-paper", reply: "本次检索没有匹配结果，尚未归档论文。"},
		{name: "unavailable", query: "fixture", reply: "论文来源暂时不可用，本次检索失败，没有取得候选论文。", sourceUnavailable: true},
		{name: "compact_note", query: "fixture", reply: "已检索到 [Fixture Research](https://arxiv.org/abs/2401.00001)。\n" + strings.Repeat("摘要中的研究结论。", 510), save: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var notes []domain.Note
			deps := testGraphDeps(&notes)
			var source domain.PaperSource = mcp.NewFixtureSource()
			if tc.sourceUnavailable {
				source = academicUnavailableSource{PaperSource: source}
			}
			deps.Academic = AcademicToolsDeps{Academic: NewAcademicService(source)}
			searchCalls, composeCalls := 0, 0
			deps.Model = llm.NewHandlerModel(func(_ context.Context, req llm.Request, _ llm.Options) ([]schema.Message, error) {
				if len(req.Functions) == 0 {
					if strings.Contains(req.Messages[0].JoinedText(""), "业务路由器") {
						decision, _ := json.Marshal(GoalDecision{Route: "academic", Save: tc.save})
						return []schema.Message{schema.AssistantMessage(string(decision))}, nil
					}
					if strings.Contains(req.Messages[0].JoinedText(""), "项目笔记压缩器") {
						return []schema.Message{schema.AssistantMessage("已检索 Fixture Research；下一步阅读摘要。")}, nil
					}
					composeCalls++
					return []schema.Message{schema.AssistantMessage("我不能进行实时联网检索，以下根据已有知识推荐其他论文。")}, nil
				}
				last := req.Messages[len(req.Messages)-1]
				if last.Role != schema.RoleTool {
					args, _ := json.Marshal(AcademicQueryArgs{Text: tc.query, Limit: 1})
					return []schema.Message{schema.NewMessage(schema.RoleAssistant, schema.NewFunctionToolCallBlock("search-proof", "search_papers", string(args)))}, nil
				}
				results := last.FunctionToolResults()
				if len(results) != 1 || results[0].Name != "search_papers" || results[0].IsError != tc.sourceUnavailable {
					t.Fatalf("未取得实际检索结果: %+v", results)
				}
				searchCalls++
				return []schema.Message{schema.AssistantMessage(tc.reply)}, nil
			})
			compiled, err := BuildResearchGraph(deps)
			if err != nil {
				t.Fatal(err)
			}
			reader, err := compiled.StreamEvents(context.Background(), []schema.Message{schema.UserMessage("请帮我检索相关论文")}, stream.Config{})
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			var output []schema.Message
			publicReply := false
			want := tc.reply
			if tc.save {
				want += "\n\n项目笔记已保存，可在项目笔记面板查看。"
			}
			for {
				event, err := reader.Recv()
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				if event.Meta.NodeID == "compose_reply" && event.Message != nil && event.Message.Snapshot != nil {
					publicReply = event.Message.Snapshot.JoinedText("") == want
				}
				if event.Result != nil {
					output = *event.Result
				}
			}
			if len(output) != 1 || output[0].JoinedText("") != want {
				t.Fatalf("检索答复被二次改写: %+v", output)
			}
			if searchCalls != 1 || composeCalls != 0 || !publicReply {
				t.Fatalf("必须保留真实工具结果并发布最终答复: search=%d compose=%d publicReply=%v", searchCalls, composeCalls, publicReply)
			}
			if tc.save && (len(notes) != 1 || notes[0].Text != "已检索 Fixture Research；下一步阅读摘要。") {
				t.Fatalf("笔记压缩应只影响已保存笔记: %+v", notes)
			}
		})
	}
}

type academicUnavailableSource struct {
	domain.PaperSource
}

func (academicUnavailableSource) Search(context.Context, domain.PaperQuery) ([]domain.Candidate, error) {
	return nil, domain.ErrUnavailable
}

func TestAcademicGraphSearchSaveReadAndResume(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	project, err := store.CreateProject(ctx, "local-user", "论文研究")
	if err != nil {
		t.Fatal(err)
	}
	scope := domain.Scope{UserID: "local-user", ProjectID: project.ID}
	papers, evidence := NewPaperService(store), NewEvidenceService(store)
	var saved []domain.Note
	deps := testGraphDeps(&saved)
	deps.Scope, deps.Project = scope, project
	deps.Academic = AcademicToolsDeps{Scope: scope, Academic: NewAcademicService(mcp.NewFixtureSource()), Papers: papers, Evidence: evidence}
	deps.Model = llm.NewHandlerModel(func(_ context.Context, req llm.Request, _ llm.Options) ([]schema.Message, error) {
		if len(req.Functions) == 0 {
			if strings.Contains(req.Messages[0].JoinedText(""), "业务路由器") {
				return []schema.Message{schema.AssistantMessage(`{"route":"academic","scope":"","save":true}`)}, nil
			}
			return []schema.Message{schema.AssistantMessage("已读取摘要并归档论文；详见项目论文库。")}, nil
		}
		last := req.Messages[len(req.Messages)-1]
		call := func(name, args string) ([]schema.Message, error) {
			return []schema.Message{schema.NewMessage(schema.RoleAssistant, schema.NewFunctionToolCallBlock("call-"+name, name, args))}, nil
		}
		if last.Role != schema.RoleTool {
			return call("search_papers", `{"text":"fixture","limit":1}`)
		}
		result := last.FunctionToolResults()[0]
		if result.IsError {
			t.Fatalf("工具失败: %+v", result)
		}
		switch result.Name {
		case "search_papers":
			return call("save_paper", `{"reference":"arxiv:2401.00001"}`)
		case "save_paper":
			return call("ask_user", `{"question":"优先阅读哪个部分？","choices":["摘要","研究方法"]}`)
		case "ask_user":
			for _, message := range req.Messages {
				for _, r := range message.FunctionToolResults() {
					if r.Name == "save_paper" {
						var paper domain.Paper
						if err := json.Unmarshal([]byte(schema.NewMessage(schema.RoleTool, r.ContentBlocks...).JoinedText("")), &paper); err != nil {
							t.Fatal(err)
						}
						args, _ := json.Marshal(ReadPaperArgs{PaperID: paper.ID, Section: "abstract"})
						return call("read_paper", string(args))
					}
				}
			}
		case "read_paper":
			return []schema.Message{schema.AssistantMessage("Fixture Research 的摘要讨论可重复研究工作流。[论文](https://arxiv.org/abs/2401.00001)")}, nil
		}
		t.Fatalf("未预期工具结果: %s", result.Name)
		return nil, nil
	})
	compiled, err := BuildResearchGraph(deps)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := state.NewMemoryStore()
	_, err = compiled.Invoke(ctx, []schema.Message{schema.UserMessage("搜索 fixture 论文，归档、阅读并保存项目笔记")}, graph.WithCheckpoint(checkpoint, "academic-resume"), graph.WithDurability(graph.DurabilitySync))
	if !errors.Is(err, graph.ErrInterrupted) {
		t.Fatalf("应在 ask_user 等待用户: %v", err)
	}
	items, err := papers.List(ctx, scope, domain.PaperQuery{})
	if err != nil || len(items) != 1 {
		t.Fatalf("中断前归档失败: %v %v", items, err)
	}
	pending, err := compiled.PendingInterrupts(ctx, graph.WithCheckpoint(checkpoint, "academic-resume"))
	if err != nil || len(pending) != 1 {
		t.Fatalf("中断缺失: %v %v", pending, err)
	}
	output, err := compiled.Resume(ctx, graph.WithCheckpoint(checkpoint, "academic-resume"), graph.WithResume(pending[0].ID, "摘要"), graph.WithDurability(graph.DurabilitySync))
	if err != nil || len(output) != 1 {
		t.Fatalf("恢复论文流程失败: %v %v", output, err)
	}
	docs, err := evidence.ListDocuments(ctx, scope, items[0].ID)
	if err != nil || len(docs) != 1 || docs[0].SHA256 == "" || docs[0].Locator != "abstract" {
		t.Fatalf("来源归档失败: %+v %v", docs, err)
	}
	items, err = papers.List(ctx, scope, domain.PaperQuery{})
	if err != nil || len(items) != 1 || len(saved) != 1 {
		t.Fatalf("恢复后应保持论文幂等并保存笔记: papers=%v notes=%v err=%v", items, saved, err)
	}
	if _, err = papers.Get(ctx, domain.Scope{UserID: "another-user", ProjectID: project.ID}, items[0].ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("跨用户读取未隔离: %v", err)
	}
	other, err := store.CreateProject(ctx, "local-user", "另一个项目")
	if err != nil {
		t.Fatal(err)
	}
	foreign := deps.Academic
	foreign.Scope = domain.Scope{UserID: "local-user", ProjectID: other.ID}
	result, err := NewAcademicTools(foreign)[3].Invoke(ctx, tool.CallInput{Args: map[string]any{"paper_id": items[0].ID}})
	if err != nil || !result.IsError {
		t.Fatalf("read_paper 不能跨项目读取: result=%+v err=%v", result, err)
	}
}
