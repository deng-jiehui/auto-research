package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/deng-jiehui/auto-research/internal/domain"
	"github.com/deng-jiehui/auto-research/internal/infra/mcp"
	"github.com/deng-jiehui/auto-research/internal/infra/sqlite"
	"github.com/goharness/goharness/pkg/llm"
	"github.com/goharness/goharness/pkg/schema"
)

func conversationReport(t *testing.T) (*sqlite.Store, *TurnService, domain.Scope, domain.ResearchReport) {
	t.Helper()
	s, err := sqlite.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	service := analysisService(s, mcp.NewFixtureSource())
	scope, _, run := approveAnalysisInputs(t, service, s, "autoresearch-agent-single-v8")
	if _, err = service.RunQueued(t.Context()); err != nil {
		t.Fatal(err)
	}
	run, err = s.GetResearch(t.Context(), scope, run.ID)
	if err != nil || run.LatestReport == nil {
		t.Fatalf("报告准备失败: %v", err)
	}
	report, err := s.GetResearchReport(t.Context(), scope, run.LatestReport.ID)
	if err != nil {
		t.Fatal(err)
	}
	return s, service, scope, report
}

func nextConversationReport(t *testing.T, s *sqlite.Store, scope domain.Scope, report domain.ResearchReport) domain.ResearchReport {
	t.Helper()
	ctx := t.Context()
	run, err := s.GetResearch(ctx, scope, report.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetTurnState(ctx, scope, run.TurnID, domain.TurnQueued, "", ""); err != nil {
		t.Fatal(err)
	}
	draft := report.Draft
	draft.Title = "新版本报告"
	draft.Summary = "新增实验建议，仍需验证。"
	next, err := s.SaveResearchReport(ctx, scope, run.TurnID, "report-v2", report.Revision, draft)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetTurnState(ctx, scope, run.TurnID, domain.TurnCompleted, "", ""); err != nil {
		t.Fatal(err)
	}
	return next
}

func TestReportConversationKeepsOldVersionUntilExplicitSwitch(t *testing.T) {
	s, service, scope, old := conversationReport(t)
	ctx := t.Context()
	session, _ := s.CreateSession(ctx, scope, "版本隔离")
	input := reportMessage(t, "v1", "解释这份报告", map[string]string{"run_id": old.RunID, "report_id": old.ID})
	first, err := service.Send(ctx, scope, session.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.RunQueued(ctx); err != nil {
		t.Fatal(err)
	}
	nextReport := nextConversationReport(t, s, scope, old)
	if nextReport.Revision != 2 {
		t.Fatal("未生成第二版报告")
	}
	continued, err := service.Send(ctx, scope, session.ID, reportMessage(t, "continue-v1", "继续解释", nil))
	if err != nil {
		t.Fatal(err)
	}
	assertReportBinding(t, continued.Turn, old.ID, 1)
	if _, err = service.RunQueued(ctx); err != nil {
		t.Fatal(err)
	}
	switched, err := service.Send(ctx, scope, session.ID, reportMessage(t, "switch", "改为讨论新版", map[string]string{"run_id": old.RunID, "report_id": nextReport.ID}))
	if err != nil {
		t.Fatal(err)
	}
	assertReportBinding(t, switched.Turn, nextReport.ID, 2)
	replay, err := service.Send(ctx, scope, session.ID, input)
	if err != nil || !replay.Replayed {
		t.Fatalf("旧请求重放失败: %v", err)
	}
	assertReportBinding(t, replay.Turn, first.Turn.ReportContext.ReportID, 1)
	if _, err = service.Send(ctx, scope, session.ID, reportMessage(t, "v1", "解释这份报告", map[string]string{"run_id": old.RunID, "report_id": nextReport.ID})); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("同一消息身份换成新版未冲突: %v", err)
	}
}

func TestReportConversationGraphReadsOnDemandAndReturnsCitation(t *testing.T) {
	for _, mode := range []string{"single", "multi"} {
		t.Run(mode, func(t *testing.T) {
			s, service, scope, report := conversationReport(t)
			ctx := t.Context()
			session, _ := s.CreateSession(ctx, scope, "按需阅读")
			reads := []string{}
			var evidence domain.Evidence
			service.model = llm.NewHandlerModel(func(_ context.Context, req llm.Request, _ llm.Options) ([]schema.Message, error) {
				if strings.Contains(req.Messages[0].JoinedText(""), report.Markdown) || strings.Contains(req.Messages[0].JoinedText(""), "报告尚未开放") {
					return nil, errors.New("系统提示注入了完整报告或能力说明过时")
				}
				found := 0
				for _, message := range req.Messages {
					if strings.HasPrefix(message.JoinedText(""), "当前消息附带的报告引用") {
						found++
					}
				}
				if found != 1 {
					return nil, fmt.Errorf("报告绑定丢失或重复注入: %d", found)
				}
				last := req.Messages[len(req.Messages)-1]
				path := "research://index.json"
				if last.Role == schema.RoleTool {
					results := last.FunctionToolResults()
					if len(results) != 1 || results[0].IsError {
						return nil, errors.New("真实文件工具读取失败")
					}
					var read ReadFileResult
					if err := json.Unmarshal([]byte(schema.NewMessage(schema.RoleTool, results[0].ContentBlocks...).JoinedText("")), &read); err != nil {
						return nil, err
					}
					reads = append(reads, read.Path)
					switch read.Path {
					case "research://index.json":
						if !strings.Contains(read.Content, "research://runs/index.json") {
							return nil, errors.New("资料索引缺少任务入口")
						}
						path = "research://runs/index.json"
					case "research://runs/index.json":
						if !strings.Contains(read.Content, report.RunID) {
							return nil, errors.New("未找到已有任务")
						}
						path = "research://runs/" + report.RunID + ".json"
					case "research://runs/" + report.RunID + ".json":
						if !strings.Contains(read.Content, report.ID) {
							return nil, errors.New("任务未指向已保存报告")
						}
						path = "research://runs/" + report.RunID + "/reports.json"
					case "research://runs/" + report.RunID + "/reports.json":
						if !strings.Contains(read.Content, report.ResourcePath) {
							return nil, errors.New("报告目录缺少正文入口")
						}
						path = report.ResourcePath
					case report.ResourcePath:
						if !strings.Contains(read.Content, "方法比较") {
							return nil, errors.New("未读取实际报告")
						}
						path = "research://reports/" + report.ID + "/references.json"
					case "research://reports/" + report.ID + "/references.json":
						var page struct {
							Items []domain.Evidence `json:"items"`
						}
						if err := json.Unmarshal([]byte(read.Content), &page); err != nil || len(page.Items) == 0 {
							return nil, errors.New("未读到报告真实证据")
						}
						evidence = page.Items[0]
						path = "research://documents/" + evidence.DocumentID + "/blocks/" + evidence.BlockID + ".txt"
					default:
						if !strings.Contains(read.Content, evidence.Quote) {
							return nil, errors.New("冻结原文不包含引用片段")
						}
						return []schema.Message{schema.AssistantMessage("报告的原文依据：" + evidence.Quote + "\n\n[查看原文](/p/" + scope.ProjectID + "/papers?paper=" + evidence.PaperID + "&evidence=" + evidence.ID + ")\n\n该结论之外的实验效果仍未知，需要验证。")}, nil
					}
				}
				raw, _ := json.Marshal(ReadFileArgs{Path: path})
				return []schema.Message{schema.NewMessage(schema.RoleAssistant, schema.NewFunctionToolCallBlock(newID(), "read_file", string(raw)))}, nil
			})
			for i := 0; i < 2; i++ {
				var selected any
				if i == 0 {
					selected = map[string]string{"run_id": report.RunID, "report_id": report.ID}
				}
				in := reportMessage(t, fmt.Sprint(i), "为什么有这个结论？", selected)
				in.AgentMode = mode
				if _, err := service.Send(ctx, scope, session.ID, in); err != nil {
					t.Fatal(err)
				}
				if _, err := service.RunQueued(ctx); err != nil {
					t.Fatal(err)
				}
			}
			if len(reads) != 14 {
				t.Fatalf("未通过现有工具按需读取: %v", reads)
			}
			snapshot, err := service.Snapshot(ctx, scope, session.ID, 100)
			if err != nil {
				t.Fatal(err)
			}
			last := decodeCommittedMessages(snapshot.Messages)
			if !strings.Contains(last[len(last)-1].JoinedText(""), report.References[0].ID) {
				t.Fatal("回答未保存真实引用")
			}
			runs, err := s.ListResearch(ctx, scope)
			if err != nil || len(runs) != 1 {
				t.Fatal("报告追问重复启动研究", err)
			}
		})
	}
}

func reportMessage(t *testing.T, key, text string, selection any) SendInput {
	t.Helper()
	value := map[string]any{"text": text, "idempotency_key": key}
	if selection != nil {
		value["report_context"] = selection
	}
	raw, _ := json.Marshal(value)
	var in SendInput
	if err := json.Unmarshal(raw, &in); err != nil {
		t.Fatal(err)
	}
	return in
}

func assertReportBinding(t *testing.T, turn domain.Turn, id string, revision int64) {
	t.Helper()
	var context struct {
		Report *struct {
			ReportID string `json:"report_id"`
			Revision int64  `json:"revision"`
		} `json:"report_context"`
	}
	if err := json.Unmarshal(turn.ContextJSON, &context); err != nil {
		t.Fatal(err)
	}
	if id == "" {
		if context.Report != nil {
			t.Fatal("移除引用后仍继承报告", string(turn.ContextJSON))
		}
		return
	}
	if context.Report == nil || context.Report.ReportID != id || context.Report.Revision != revision {
		t.Fatalf("轮次未冻结选中的报告版本: %s", turn.ContextJSON)
	}
	data, _ := json.Marshal(turn)
	var projected map[string]json.RawMessage
	_ = json.Unmarshal(data, &projected)
	if len(projected["report_context"]) == 0 {
		t.Fatal("快照未向用户展示报告绑定")
	}
}

func TestReportConversationInheritsClearsAndPreservesRetry(t *testing.T) {
	s, service, scope, report := conversationReport(t)
	ctx := t.Context()
	session, _ := s.CreateSession(ctx, scope, "报告讨论")
	selected := map[string]any{"run_id": report.RunID, "report_id": report.ID}
	first, err := service.Send(ctx, scope, session.ID, reportMessage(t, "first", "单卡可以怎么做？", selected))
	if err != nil {
		t.Fatal(err)
	}
	assertReportBinding(t, first.Turn, report.ID, 1)
	if _, err = service.RunQueued(ctx); err != nil {
		t.Fatal(err)
	}
	// 重建服务后，多轮追问仍从数据库继承，不依赖页面 URL 或进程内状态。
	service = analysisService(s, mcp.NewFixtureSource())
	next, err := service.Send(ctx, scope, session.ID, reportMessage(t, "second", "依据是哪篇？", nil))
	if err != nil {
		t.Fatal(err)
	}
	assertReportBinding(t, next.Turn, report.ID, 1)
	if _, err = service.RunQueued(ctx); err != nil {
		t.Fatal(err)
	}
	current, _ := s.GetTurn(ctx, scope, session.ID, next.Turn.ID)
	edit, err := service.EditMessage(ctx, scope, session.ID, next.Turn.ID, EditMessageInput{Text: "给我引用原文", TurnActionInput: TurnActionInput{Revision: current.Revision, IdempotencyKey: "edit"}})
	if err != nil {
		t.Fatal(err)
	}
	assertReportBinding(t, edit.Turn, report.ID, 1)
	if _, err = service.RunQueued(ctx); err != nil {
		t.Fatal(err)
	}
	current, _ = s.GetTurn(ctx, scope, session.ID, next.Turn.ID)
	retry, err := service.Retry(ctx, scope, session.ID, next.Turn.ID, TurnActionInput{Revision: current.Revision, IdempotencyKey: "retry"})
	if err != nil {
		t.Fatal(err)
	}
	assertReportBinding(t, retry.Turn, report.ID, 1)
	if _, err = service.RunQueued(ctx); err != nil {
		t.Fatal(err)
	}
	cleared, err := service.Send(ctx, scope, session.ID, reportMessage(t, "clear", "我们换个话题", map[string]bool{"clear": true}))
	if err != nil {
		t.Fatal(err)
	}
	assertReportBinding(t, cleared.Turn, "", 0)
	if _, err = service.RunQueued(ctx); err != nil {
		t.Fatal(err)
	}
	next, err = service.Send(ctx, scope, session.ID, reportMessage(t, "after-clear", "你好", nil))
	if err != nil {
		t.Fatal(err)
	}
	assertReportBinding(t, next.Turn, "", 0)
	// 原消息重放不能因为当前选择已移除而变成不同版本。
	replay, err := service.Send(ctx, scope, session.ID, reportMessage(t, "first", "单卡可以怎么做？", selected))
	if err != nil || !replay.Replayed || replay.Turn.ID != first.Turn.ID {
		t.Fatalf("引用轮次重放失败: %+v %v", replay, err)
	}
	assertReportBinding(t, replay.Turn, report.ID, 1)
}

func TestReportConversationRejectsForeignAndConflictingSelections(t *testing.T) {
	s, service, scope, report := conversationReport(t)
	ctx := t.Context()
	project, _ := s.CreateProject(ctx, "alice", "另一个项目")
	foreignScope := domain.Scope{UserID: "alice", ProjectID: project.ID}
	session, _ := s.CreateSession(ctx, foreignScope, "跨项目")
	selected := map[string]any{"run_id": report.RunID, "report_id": report.ID}
	if _, err := service.Send(ctx, foreignScope, session.ID, reportMessage(t, "foreign", "分析报告", selected)); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("跨项目引用未拒绝: %v", err)
	}
	session, _ = s.CreateSession(ctx, scope, "校验")
	for _, selection := range []any{
		map[string]string{"run_id": "wrong-run", "report_id": report.ID},
		map[string]string{"report_id": report.ID},
		map[string]any{"clear": true, "run_id": report.RunID, "report_id": report.ID},
		map[string]any{},
	} {
		if _, err := service.Send(ctx, scope, session.ID, reportMessage(t, "bad", "分析报告", selection)); err == nil {
			t.Fatalf("不合法的报告选择被接受: %+v", selection)
		}
	}
	first, err := service.Send(ctx, scope, session.ID, reportMessage(t, "same-key", "分析报告", selected))
	if err != nil {
		t.Fatal(err)
	}
	assertReportBinding(t, first.Turn, report.ID, 1)
	if _, err = service.Send(ctx, scope, session.ID, reportMessage(t, "same-key", "分析报告", map[string]bool{"clear": true})); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("同一执行身份改变引用未冲突: %v", err)
	}
}

func TestReportConversationUserMessagesKeepTheirReportIdentity(t *testing.T) {
	s, service, scope, report := conversationReport(t)
	ctx := t.Context()
	session, _ := s.CreateSession(ctx, scope, "消息引用")
	created, err := service.Send(ctx, scope, session.ID, reportMessage(t, "message", "这个方法有什么限制？", map[string]string{"run_id": report.RunID, "report_id": report.ID}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.RunQueued(ctx); err != nil {
		t.Fatal(err)
	}
	check := func() {
		messages, err := s.ListMessages(ctx, scope, session.ID, 100)
		if err != nil {
			t.Fatal(err)
		}
		user := decodeCommittedMessages(messages)[0]
		data, _ := json.Marshal(user.Extra["report_context"])
		var reference struct {
			ReportID string `json:"report_id"`
			Revision int64  `json:"revision"`
		}
		if json.Unmarshal(data, &reference) != nil || reference.ReportID != report.ID || reference.Revision != 1 {
			t.Fatalf("历史消息丢失其报告引用: %s", data)
		}
	}
	check()
	current, _ := s.GetTurn(ctx, scope, session.ID, created.Turn.ID)
	if _, err = service.EditMessage(ctx, scope, session.ID, current.ID, EditMessageInput{Text: "具体限制呢？", TurnActionInput: TurnActionInput{Revision: current.Revision, IdempotencyKey: "edit"}}); err != nil {
		t.Fatal(err)
	}
	check()
}

func TestReportConversationReadsFrozenReportResources(t *testing.T) {
	ctx := t.Context()
	s, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	source := &partialPaperSource{PaperSource: mcp.NewFixtureSource(), store: s}
	source.unavailable.Store(true)
	service := analysisService(s, source)
	scope, researchTurn, run := approveAnalysisInputs(t, service, s, "autoresearch-agent-single-v8")
	source.scope, source.turnID = scope, researchTurn.ID
	if _, err = service.RunQueued(ctx); err != nil {
		t.Fatal(err)
	}
	run, _ = s.GetResearch(ctx, scope, run.ID)
	report, err := s.GetResearchReport(ctx, scope, run.LatestReport.ID)
	if err != nil {
		t.Fatal(err)
	}
	turn, _ := s.GetTurn(ctx, scope, run.SessionID, run.TurnID)
	_, err = service.ControlResearch(ctx, scope, run.ID, "retry", ResearchControlInput{TurnActionInput: TurnActionInput{Revision: turn.Revision, IdempotencyKey: "change-analysis"}, PaperID: run.WorkItems[1].PaperID, WorkRevision: run.WorkItems[1].Revision})
	if err != nil {
		t.Fatal(err)
	}
	current, _ := service.ResearchService().Matrix(ctx, scope, run.ID)
	if current.Hash == report.MatrixHash {
		t.Fatal("测试未改变当前矩阵")
	}
	resources := &ProjectResources{Scope: scope, Projects: s, Research: service.ResearchService()}
	entries, err := resources.catalog(ctx, "research://reports/"+report.ID)
	if err != nil || len(entries) != 3 {
		t.Fatalf("报告目录未提供正文、矩阵和引用入口: %+v %v", entries, err)
	}
	for _, name := range []string{"matrix.json", "references.json"} {
		path := "research://reports/" + report.ID + "/" + name
		value, err := resources.Read(ctx, path, ReadFileArgs{})
		if err != nil {
			t.Fatalf("历史报告快照不可读: %s %v", path, err)
		}
		var page struct {
			ReportID string            `json:"report_id"`
			Hash     string            `json:"matrix_hash"`
			Items    []json.RawMessage `json:"items"`
		}
		if json.Unmarshal([]byte(value.Content), &page) != nil || page.ReportID != report.ID || page.Hash != report.MatrixHash || len(page.Items) == 0 {
			t.Fatalf("读到了当前矩阵或丢失引用: %s", value.Content)
		}
		if name == "references.json" && !strings.Contains(value.Content, report.References[0].Quote) {
			t.Fatal("报告证据快照被替换")
		}
		if name == "references.json" {
			var reference struct {
				CitationURL string `json:"citation_url"`
				SourcePath  string `json:"source_path"`
			}
			if json.Unmarshal(page.Items[0], &reference) != nil || reference.CitationURL != "/p/"+scope.ProjectID+"/papers?paper="+report.References[0].PaperID+"&evidence="+report.References[0].ID || !strings.HasPrefix(reference.SourcePath, "research://documents/") {
				t.Fatalf("工具没有提供可核验的原文入口: %+v", reference)
			}
		}
		foreign, _ := s.CreateProject(ctx, "bob", "未授权")
		copy := *resources
		copy.Scope = domain.Scope{UserID: "bob", ProjectID: foreign.ID}
		if _, err = copy.Read(ctx, path, ReadFileArgs{}); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("跨项目读取报告快照未拒绝: %v", err)
		}
	}
}
