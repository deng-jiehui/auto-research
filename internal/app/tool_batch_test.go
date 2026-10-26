package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deng-jiehui/auto-research/internal/domain"
	"github.com/deng-jiehui/auto-research/internal/infra/mcp"
	"github.com/deng-jiehui/auto-research/internal/infra/sqlite"
	"github.com/goharness/goharness/pkg/llm"
	"github.com/goharness/goharness/pkg/schema"
)

func TestSingleAgentReadBatchUsesTurnBudget(t *testing.T) {
	for _, count := range []int{8, 32, 33} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "research.txt"), []byte("研究资料"), 0600); err != nil {
				t.Fatal(err)
			}
			deps := testGraphDeps(nil)
			deps.AgentMode = "single"
			deps.WorkspacePolicy = WorkspacePolicy{Root: root}
			deps.Model = llm.NewHandlerModel(func(_ context.Context, req llm.Request, _ llm.Options) ([]schema.Message, error) {
				if req.Messages[len(req.Messages)-1].Role != schema.RoleTool {
					blocks := make([]schema.ContentBlock, 0, count)
					for i := 0; i < count; i++ {
						blocks = append(blocks, schema.NewFunctionToolCallBlock(fmt.Sprintf("read-%d", i), "read_file", `{"path":"research.txt"}`))
					}
					return []schema.Message{schema.NewMessage(schema.RoleAssistant, blocks...)}, nil
				}
				seen := map[string]bool{}
				for _, message := range req.Messages {
					for _, result := range message.FunctionToolResults() {
						var read ReadFileResult
						data := schema.NewMessage(schema.RoleTool, result.ContentBlocks...).JoinedText("")
						if result.IsError || seen[result.CallID] || json.Unmarshal([]byte(data), &read) != nil || read.Content != "研究资料" {
							t.Fatalf("读取结果缺失、重复或配对错误: %+v", result)
						}
						seen[result.CallID] = true
					}
				}
				for i := 0; i < count; i++ {
					if !seen[fmt.Sprintf("read-%d", i)] {
						t.Fatalf("缺少第 %d 个读取结果", i)
					}
				}
				return []schema.Message{schema.AssistantMessage("已读取全部资料")}, nil
			})
			compiled, err := BuildResearchGraph(deps)
			if err != nil {
				t.Fatal(err)
			}
			output, err := compiled.Invoke(t.Context(), []schema.Message{schema.UserMessage("读取研究资料")})
			if count == 33 {
				if err == nil || !strings.Contains(err.Error(), "工具调用次数超过上限") {
					t.Fatalf("超出轮次预算应拒绝执行: %v", err)
				}
				return
			}
			if err != nil || len(output) != 1 || output[0].JoinedText("") != "已读取全部资料" {
				t.Fatalf("预算内的读取批次未完成: output=%v err=%v", output, err)
			}
		})
	}
}

func TestDetachedResearchAcceptsReadBatchAfterPlanApproval(t *testing.T) {
	ctx := t.Context()
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service := analysisService(store, mcp.NewFixtureSource())
	project, err := store.CreateProject(ctx, "alice", "大批次读取")
	if err != nil {
		t.Fatal(err)
	}
	scope := domain.Scope{UserID: "alice", ProjectID: project.ID}
	created, err := service.StartResearch(ctx, scope, StartResearchInput{ResearchSpec: domain.ResearchSpec{Question: "比较预测基线"}, IdempotencyKey: "batch-research"})
	if err != nil {
		t.Fatal(err)
	}
	deps := ResearchGraphDeps{Scope: scope, TurnID: created.Turn.ID, Research: service.ResearchService(), Academic: service.academic, DefinitionVersion: "autoresearch-agent-single-v8"}
	fixture := newSingleFixtureModel(deps)
	readBatchSent := false
	readBatchVerified := false
	service.model = llm.NewHandlerModel(func(ctx context.Context, req llm.Request, _ llm.Options) ([]schema.Message, error) {
		run, err := store.GetResearch(ctx, scope, created.Run.ID)
		if err != nil {
			return nil, err
		}
		if run.ApprovedPlanRevision > 0 && !readBatchSent {
			readBatchSent = true
			blocks := make([]schema.ContentBlock, 0, 8)
			for i := 0; i < 8; i++ {
				args, _ := json.Marshal(ReadFileArgs{Path: "research://runs/" + run.ID + ".json", StartLine: i + 1, EndLine: i + 1})
				blocks = append(blocks, schema.NewFunctionToolCallBlock(fmt.Sprintf("research-read-%d", i), "read_file", string(args)))
			}
			return []schema.Message{schema.NewMessage(schema.RoleAssistant, blocks...)}, nil
		}
		if readBatchSent && !readBatchVerified {
			seen := map[string]bool{}
			for _, message := range req.Messages {
				for _, result := range message.FunctionToolResults() {
					if !strings.HasPrefix(result.CallID, "research-read-") {
						continue
					}
					var read ReadFileResult
					data := schema.NewMessage(schema.RoleTool, result.ContentBlocks...).JoinedText("")
					if result.IsError || seen[result.CallID] || json.Unmarshal([]byte(data), &read) != nil || read.Path != "research://runs/"+run.ID+".json" || read.Content == "" {
						t.Fatalf("任务读取结果错误: %+v", result)
					}
					seen[result.CallID] = true
				}
			}
			if len(seen) != 8 {
				t.Fatalf("任务读取结果数=%d，期望 8", len(seen))
			}
			readBatchVerified = true
		}
		response, err := fixture.Invoke(ctx, req)
		return response.Messages, err
	})
	if _, err = service.RunQueued(ctx); err != nil {
		t.Fatal(err)
	}
	turn, err := store.GetTurn(ctx, scope, created.Turn.SessionID, created.Turn.ID)
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.GetResearch(ctx, scope, created.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	var pending []struct {
		ID string `json:"id"`
	}
	if err = json.Unmarshal(turn.PendingQuestions, &pending); err != nil || len(pending) != 1 {
		t.Fatalf("未进入计划审核: state=%s err=%v", turn.State, err)
	}
	_, err = service.Resume(ctx, scope, turn.SessionID, turn.ID, ResumeInput{Revision: turn.Revision, IdempotencyKey: "approve-plan", Answers: []AnswerInput{{InterruptID: pending[0].ID, ResearchPlan: &domain.ResearchPlanReview{RunID: run.ID, PlanRevision: run.PlanRevision, PlanHash: run.PlanHash, Decision: "approve", Plan: run.Plan}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.RunQueued(ctx); err != nil {
		t.Fatal(err)
	}
	run, err = store.GetResearch(ctx, scope, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !readBatchVerified || run.ExecutionState != domain.TurnAwaitingInput || run.Phase != "candidate_review" || len(run.Candidates) == 0 {
		t.Fatalf("计划确认后的读取批次未继续到候选审核: state=%s phase=%s candidates=%d", run.ExecutionState, run.Phase, len(run.Candidates))
	}
}
