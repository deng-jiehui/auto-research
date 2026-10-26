package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deng-jiehui/auto-research/internal/domain"
	"github.com/deng-jiehui/auto-research/internal/infra/mcp"
	"github.com/deng-jiehui/auto-research/internal/infra/sqlite"
	"github.com/goharness/goharness/pkg/graph"
	"github.com/goharness/goharness/pkg/llm"
	"github.com/goharness/goharness/pkg/runnable"
	"github.com/goharness/goharness/pkg/schema"
	"github.com/goharness/goharness/pkg/state"
	"github.com/goharness/goharness/pkg/stream"
)

func TestSingleAgentKeepsOneContextAcrossToolLoop(t *testing.T) {
	deps := testGraphDeps(nil)
	deps.AgentMode = "single"
	deps.Academic = AcademicToolsDeps{Academic: NewAcademicService(mcp.NewFixtureSource())}
	var calls int
	var sawToolResult bool
	deps.Model = llm.NewHandlerModel(func(_ context.Context, request llm.Request, _ llm.Options) ([]schema.Message, error) {
		calls++
		if len(request.Messages) > 2 {
			sawToolResult = request.Messages[len(request.Messages)-1].Role == schema.RoleTool
		}
		if calls == 1 {
			args, _ := json.Marshal(AcademicQueryArgs{Text: "fixture", Limit: 1})
			return []schema.Message{schema.NewMessage(schema.RoleAssistant, schema.NewFunctionToolCallBlock("search-1", "search_papers", string(args)))}, nil
		}
		return []schema.Message{schema.AssistantMessage("已通过同一个研究 Agent 读取论文工具结果并回答。")}, nil
	})
	compiled, err := BuildResearchGraph(deps)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := compiled.StreamEvents(context.Background(), []schema.Message{schema.UserMessage("检索 fixture 论文")}, stream.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	var output []schema.Message
	for {
		event, recvErr := reader.Recv()
		if errors.Is(recvErr, io.EOF) {
			break
		}
		if recvErr != nil {
			t.Fatal(recvErr)
		}
		if event.Result != nil {
			output = *event.Result
		}
	}
	if len(output) != 1 || output[0].JoinedText("") != "已通过同一个研究 Agent 读取论文工具结果并回答。" {
		t.Fatalf("单 Agent 最终输出=%v", output)
	}
	if calls != 2 || !sawToolResult {
		t.Fatalf("模型上下文没有连续传递: calls=%d sawToolResult=%v", calls, sawToolResult)
	}
}

func TestMultiAgentSearchPublishesArchivableCandidates(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	project, err := store.CreateProject(ctx, "local-user", "子任务检索")
	if err != nil {
		t.Fatal(err)
	}
	scope := domain.Scope{UserID: "local-user", ProjectID: project.ID}
	session, err := store.CreateSession(ctx, scope, "独立检索")
	if err != nil {
		t.Fatal(err)
	}
	model := llm.NewHandlerModel(func(_ context.Context, req llm.Request, _ llm.Options) ([]schema.Message, error) {
		if req.Messages[len(req.Messages)-1].Role == schema.RoleTool {
			return []schema.Message{schema.AssistantMessage("已返回检索结果。")}, nil
		}
		for _, fn := range req.Functions {
			if fn.Name == "run_subagent" {
				return []schema.Message{schema.NewMessage(schema.RoleAssistant, schema.NewFunctionToolCallBlock("delegate-search", "run_subagent", `{"task":"搜索 fixture 论文"}`))}, nil
			}
		}
		return []schema.Message{schema.NewMessage(schema.RoleAssistant, schema.NewFunctionToolCallBlock("child-search", "search_papers", `{"text":"fixture","limit":2}`))}, nil
	})
	service := NewTurnService(store, store, store, model)
	service.SetCheckpointFactory(func(scope domain.Scope, sessionID, turnID string) state.Store {
		return sqlite.NewCheckpointStore(store, scope, sessionID, turnID)
	})
	if err = service.SetAgentMode("multi"); err != nil {
		t.Fatal(err)
	}
	service.SetAcademicTools(AcademicToolsDeps{Academic: NewAcademicService(mcp.NewFixtureSource())})
	if _, err = service.Send(ctx, scope, session.ID, SendInput{Text: "委派论文检索", IdempotencyKey: "child-search"}); err != nil {
		t.Fatal(err)
	}
	if _, err = service.RunQueued(ctx); err != nil {
		t.Fatal(err)
	}
	var after int64
	for {
		events, listErr := service.Events(ctx, scope, session.ID, after, 100)
		if listErr != nil {
			t.Fatal(listErr)
		}
		if len(events) == 0 {
			break
		}
		for _, event := range events {
			after = event.ID
			if event.Kind != "academic.tool" {
				continue
			}
			var toolEvent WorkspaceToolEvent
			if json.Unmarshal(event.PayloadJSON, &toolEvent) == nil && toolEvent.Tool == "search_papers" && len(toolEvent.Papers) == 1 && toolEvent.Papers[0].SourceKey == "arxiv:2401.00001" {
				return
			}
		}
	}
	t.Fatal("子 Agent 的真实检索结果没有返回可一键归档的论文卡片")
}

func TestSingleAgentCanSaveProjectNoteThroughTool(t *testing.T) {
	var saved domain.Note
	deps := testGraphDeps(nil)
	deps.AgentMode = "single"
	deps.SaveNote = func(_ context.Context, note domain.Note) (domain.Note, error) {
		note.ID = "note-single"
		saved = note
		return note, nil
	}
	var calls int
	deps.Model = llm.NewHandlerModel(func(_ context.Context, request llm.Request, _ llm.Options) ([]schema.Message, error) {
		calls++
		if calls == 1 {
			return []schema.Message{schema.NewMessage(schema.RoleAssistant, schema.NewFunctionToolCallBlock("save-1", "save_project_note", `{"text":"单 Agent 保存的研究笔记"}`))}, nil
		}
		return []schema.Message{schema.AssistantMessage("项目笔记已保存。")}, nil
	})
	compiled, err := BuildResearchGraph(deps)
	if err != nil {
		t.Fatal(err)
	}
	output, err := compiled.Invoke(context.Background(), []schema.Message{schema.UserMessage("保存项目笔记")})
	if err != nil || len(output) != 1 {
		t.Fatalf("保存笔记失败: output=%v err=%v", output, err)
	}
	if saved.ID != "note-single" || !strings.Contains(saved.Text, "单 Agent") {
		t.Fatalf("单 Agent 未通过工具保存笔记: %+v", saved)
	}
}

func TestMultiAgentModeAddsIsolatedSubagentTool(t *testing.T) {
	deps := testGraphDeps(nil)
	deps.AgentMode = "multi"
	var parentCalls, childCalls int
	deps.Model = llm.NewHandlerModel(func(_ context.Context, request llm.Request, _ llm.Options) ([]schema.Message, error) {
		hasDelegate := false
		for _, definition := range request.Functions {
			if definition.Name == "run_subagent" {
				hasDelegate = true
				break
			}
		}
		if hasDelegate {
			parentCalls++
			if parentCalls == 1 {
				return []schema.Message{schema.NewMessage(schema.RoleAssistant, schema.NewFunctionToolCallBlock("delegate-1", "run_subagent", `{"task":"独立检查研究目标"}`))}, nil
			}
			return []schema.Message{schema.AssistantMessage("主 Agent 已综合子 Agent 结果。")}, nil
		}
		childCalls++
		for _, message := range request.Messages {
			if strings.Contains(message.JoinedText(""), "主会话私有信息") {
				t.Error("子 Agent 不应自动继承主会话聊天历史")
			}
		}
		return []schema.Message{schema.AssistantMessage("子 Agent 独立完成检查。")}, nil
	})
	compiled, err := BuildResearchGraph(deps)
	if err != nil {
		t.Fatal(err)
	}
	output, err := compiled.Invoke(context.Background(), []schema.Message{schema.UserMessage("主会话私有信息；请委派一个独立检查")})
	if err != nil || len(output) != 1 {
		t.Fatalf("子 Agent 模式失败: output=%v err=%v", output, err)
	}
	if output[0].JoinedText("") != "主 Agent 已综合子 Agent 结果。" || parentCalls != 2 || childCalls != 1 {
		t.Fatalf("主子 Agent 调用关系异常: output=%v parent=%d child=%d", output, parentCalls, childCalls)
	}
}

func TestSingleAgentPreservesConversationPrefixAcrossTurns(t *testing.T) {
	deps := testGraphDeps(nil)
	deps.AgentMode = "single"
	var requests []llm.Request
	deps.Model = llm.NewHandlerModel(func(_ context.Context, req llm.Request, _ llm.Options) ([]schema.Message, error) {
		requests = append(requests, req.Clone())
		return []schema.Message{schema.AssistantMessage("先建立基线。")}, nil
	})
	compiled, err := BuildResearchGraph(deps)
	if err != nil {
		t.Fatal(err)
	}
	first := schema.UserMessage("如何开始研究？")
	output, err := compiled.Invoke(context.Background(), []schema.Message{first})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = compiled.Invoke(context.Background(), []schema.Message{first, output[0], schema.UserMessage("接下来呢？")}); err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(requests[0].Messages)
	after, _ := json.Marshal(requests[1].Messages[:len(requests[0].Messages)])
	if string(before) != string(after) {
		t.Fatal("追加用户消息后改写了已有消息前缀，无法复用前缀缓存")
	}
}

func TestSingleAgentSavesPaperWithinTrustedProjectScope(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	project, err := store.CreateProject(ctx, "local-user", "论文归档")
	if err != nil {
		t.Fatal(err)
	}
	scope := domain.Scope{UserID: "local-user", ProjectID: project.ID}
	deps := testGraphDeps(nil)
	deps.AgentMode, deps.Scope, deps.Project = "single", scope, project
	papers := NewPaperService(store)
	// 工具作用域必须来自图依赖，不能依赖调用者重复填写。
	deps.Academic = AcademicToolsDeps{Academic: NewAcademicService(mcp.NewFixtureSource()), Papers: papers}
	deps.Model = llm.NewHandlerModel(func(_ context.Context, req llm.Request, _ llm.Options) ([]schema.Message, error) {
		last := req.Messages[len(req.Messages)-1]
		if last.Role == schema.RoleTool {
			for _, result := range last.FunctionToolResults() {
				if result.IsError {
					t.Errorf("归档工具失败：%v", result)
				}
			}
			return []schema.Message{schema.AssistantMessage("归档完成。")}, nil
		}
		return []schema.Message{schema.NewMessage(schema.RoleAssistant, schema.NewFunctionToolCallBlock("save-scope", "save_paper", `{"reference":"2401.00001"}`))}, nil
	})
	compiled, err := BuildResearchGraph(deps)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = compiled.Invoke(ctx, []schema.Message{schema.UserMessage("归档论文")}); err != nil {
		t.Fatal(err)
	}
	items, err := papers.List(ctx, scope, domain.PaperQuery{})
	if err != nil || len(items) != 1 || items[0].ProjectID != project.ID {
		t.Fatalf("论文未保存到当前项目：%v err=%v", items, err)
	}
}

func TestSingleAgentCanRepairOversizedNote(t *testing.T) {
	deps := testGraphDeps(nil)
	deps.AgentMode = "single"
	saves, calls := 0, 0
	deps.SaveNote = func(_ context.Context, note domain.Note) (domain.Note, error) {
		saves++
		return note, nil
	}
	deps.Model = llm.NewHandlerModel(func(_ context.Context, req llm.Request, _ llm.Options) ([]schema.Message, error) {
		calls++
		if calls == 3 {
			return []schema.Message{schema.AssistantMessage("压缩后已保存。")}, nil
		}
		text := strings.Repeat("长", 4001)
		if calls == 2 {
			results := req.Messages[len(req.Messages)-1].FunctionToolResults()
			if len(results) != 1 || !results[0].IsError {
				t.Error("应把长度校验错误交回同一个 Agent 修正")
			}
			text = "研究关注点：构建基线；下一步：记录可重复实验。"
		}
		args, _ := json.Marshal(saveProjectNoteArgs{Text: text})
		return []schema.Message{schema.NewMessage(schema.RoleAssistant, schema.NewFunctionToolCallBlock("note-repair", "save_project_note", string(args)))}, nil
	})
	compiled, err := BuildResearchGraph(deps)
	if err != nil {
		t.Fatal(err)
	}
	output, err := compiled.Invoke(context.Background(), []schema.Message{schema.UserMessage("保存项目笔记")})
	if err != nil || len(output) != 1 || saves != 1 {
		t.Fatalf("笔记长度错误无法自行修复：output=%v saves=%d err=%v", output, saves, err)
	}
}

func TestAgentModesResumeConfirmedWrite(t *testing.T) {
	for _, mode := range []string{"single", "multi"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			deps := testGraphDeps(nil)
			deps.AgentMode = mode
			deps.WorkspacePolicy = WorkspacePolicy{Root: root}
			deps.Model = llm.NewHandlerModel(func(_ context.Context, req llm.Request, _ llm.Options) ([]schema.Message, error) {
				if req.Messages[len(req.Messages)-1].Role == schema.RoleTool {
					return []schema.Message{schema.AssistantMessage("写入完成。")}, nil
				}
				for _, fn := range req.Functions {
					if fn.Name == "run_subagent" {
						return []schema.Message{schema.NewMessage(schema.RoleAssistant, schema.NewFunctionToolCallBlock("delegate-write", "run_subagent", `{"task":"创建 experiment.txt，内容为 confirmed"}`))}, nil
					}
				}
				return []schema.Message{schema.NewMessage(schema.RoleAssistant, schema.NewFunctionToolCallBlock("write-confirm", "write_file", `{"path":"experiment.txt","content":"confirmed","create":true}`))}, nil
			})
			compiled, err := BuildResearchGraph(deps)
			if err != nil {
				t.Fatal(err)
			}
			checkpoints := state.NewMemoryStore()
			_, err = compiled.Invoke(context.Background(), []schema.Message{schema.UserMessage("创建实验说明文件")}, graph.WithCheckpoint(checkpoints, "confirmed-write"), graph.WithDurability(graph.DurabilitySync))
			if !errors.Is(err, graph.ErrInterrupted) {
				t.Fatalf("写入前未中断：%v", err)
			}
			if _, err = os.Stat(filepath.Join(root, "experiment.txt")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("确认前已产生文件：%v", err)
			}
			// 重建图后，子 Agent 的中断也必须能经父图恢复。
			compiled, err = BuildResearchGraph(deps)
			if err != nil {
				t.Fatal(err)
			}
			pending, err := compiled.PendingInterrupts(context.Background(), graph.WithCheckpoint(checkpoints, "confirmed-write"))
			if err != nil || len(pending) != 1 {
				t.Fatalf("找不到写入确认：%v err=%v", pending, err)
			}
			output, err := compiled.Resume(context.Background(), graph.WithCheckpoint(checkpoints, "confirmed-write"), graph.WithResume(pending[0].ID, true))
			if err != nil || len(output) != 1 {
				t.Fatalf("写入确认后恢复失败：output=%v err=%v", output, err)
			}
			data, err := os.ReadFile(filepath.Join(root, "experiment.txt"))
			if err != nil || string(data) != "confirmed" {
				t.Fatalf("恢复后写入内容错误：%q err=%v", data, err)
			}
		})
	}
}

func TestOldAgentDefinitionResumesWithOriginalTools(t *testing.T) {
	for _, entry := range []struct{ mode, version string }{{"single", "v1"}, {"multi", "v1"}, {"single", "v3"}, {"multi", "v3"}} {
		mode := entry.mode
		t.Run(mode+entry.version, func(t *testing.T) {
			deps := testGraphDeps(nil)
			deps.AgentMode = mode
			deps.DefinitionVersion = "autoresearch-agent-" + mode + "-" + entry.version
			deps.Research = NewResearchService(nil)
			deps.Model = llm.NewHandlerModel(func(_ context.Context, req llm.Request, _ llm.Options) ([]schema.Message, error) {
				for _, fn := range req.Functions {
					if fn.Name == "ask_user" {
						data, _ := json.Marshal(fn)
						if strings.Contains(string(data), "expected_revision") {
							t.Error("旧定义泄漏计划审核参数")
						}
					}
					if entry.version == "v1" && (fn.Name == "save_evidence" || fn.Name == "search_project_evidence") {
						t.Errorf("旧定义泄漏新工具: %s", fn.Name)
					}
					if entry.version == "v1" && fn.Name == "read_paper" {
						data, _ := json.Marshal(fn)
						if strings.Contains(string(data), "block_id") {
							t.Error("旧阅读 schema 被修改")
						}
					}
				}
				if req.Messages[len(req.Messages)-1].Role == schema.RoleTool {
					return []schema.Message{schema.AssistantMessage("已按原检查点恢复。")}, nil
				}
				return []schema.Message{schema.NewMessage(schema.RoleAssistant, schema.NewFunctionToolCallBlock("old-ask", "ask_user", `{"question":"优先研究什么？","choices":["基线","评测"]}`))}, nil
			})
			compiled, err := BuildResearchGraph(deps)
			if err != nil {
				t.Fatal(err)
			}
			checkpoints := state.NewMemoryStore()
			opts := []runnable.Option{graph.WithCheckpoint(checkpoints, "old-definition"), graph.WithDurability(graph.DurabilitySync)}
			if _, err = compiled.Invoke(context.Background(), []schema.Message{schema.UserMessage("先帮我确定研究重点")}, opts...); !errors.Is(err, graph.ErrInterrupted) {
				t.Fatalf("旧图未中断: %v", err)
			}
			// 模拟升级后依赖增加 Documents，但持久 Turn 仍选择旧定义。
			deps.Academic.Documents = &DocumentService{}
			compiled, err = BuildResearchGraph(deps)
			if err != nil {
				t.Fatal(err)
			}
			pending, err := compiled.PendingInterrupts(context.Background(), opts...)
			if err != nil || len(pending) != 1 {
				t.Fatalf("旧中断丢失: %v", err)
			}
			output, err := compiled.Resume(context.Background(), append(opts, graph.WithResume(pending[0].ID, "基线"))...)
			if err != nil || len(output) != 1 || output[0].JoinedText("") != "已按原检查点恢复。" {
				t.Fatalf("旧图恢复失败: %v %v", output, err)
			}
		})
	}
}

func TestVersionTwoResumesWithoutDirectionAndResourceTools(t *testing.T) {
	deps := testGraphDeps(nil)
	deps.AgentMode, deps.DefinitionVersion = "single", "autoresearch-agent-single-v2"
	deps.Academic.Documents = &DocumentService{}
	deps.Model = llm.NewHandlerModel(func(_ context.Context, req llm.Request, _ llm.Options) ([]schema.Message, error) {
		for _, fn := range req.Functions {
			data, _ := json.Marshal(fn)
			if fn.Name == "submit_research_result" || (fn.Name == "read_file" && strings.Contains(string(data), "research://")) {
				t.Error("v2 定义不应混入 v3 方向和资料 schema")
			}
		}
		if req.Messages[len(req.Messages)-1].Role == schema.RoleTool {
			return []schema.Message{schema.AssistantMessage("旧全文会话恢复成功。")}, nil
		}
		return []schema.Message{schema.NewMessage(schema.RoleAssistant, schema.NewFunctionToolCallBlock("v2-ask", "ask_user", `{"question":"研究哪个基线？","choices":["线性","Transformer"]}`))}, nil
	})
	compiled, err := BuildResearchGraph(deps)
	if err != nil {
		t.Fatal(err)
	}
	checkpoints := state.NewMemoryStore()
	opts := []runnable.Option{graph.WithCheckpoint(checkpoints, "version-two"), graph.WithDurability(graph.DurabilitySync)}
	if _, err = compiled.Invoke(context.Background(), []schema.Message{schema.UserMessage("帮我评测论文")}, opts...); !errors.Is(err, graph.ErrInterrupted) {
		t.Fatalf("v2 未中断: %v", err)
	}
	deps.Resources, deps.Directions = &ProjectResources{}, &DirectionService{}
	compiled, err = BuildResearchGraph(deps)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := compiled.PendingInterrupts(context.Background(), opts...)
	if err != nil || len(pending) != 1 {
		t.Fatalf("v2 旧中断丢失: %v", err)
	}
	output, err := compiled.Resume(context.Background(), append(opts, graph.WithResume(pending[0].ID, "线性"))...)
	if err != nil || len(output) != 1 || output[0].JoinedText("") != "旧全文会话恢复成功。" {
		t.Fatalf("v2 恢复失败: %v %v", output, err)
	}
}
