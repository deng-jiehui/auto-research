package app

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/deng-jiehui/auto-research/internal/config"
	"github.com/deng-jiehui/auto-research/internal/domain"
	"github.com/deng-jiehui/auto-research/internal/infra/mcp"
	"github.com/deng-jiehui/auto-research/internal/infra/sqlite"
	"github.com/goharness/goharness/pkg/llm"
	"github.com/goharness/goharness/pkg/schema"
)

// 此测试显式启用才访问真实 arXiv，不使用用户模型密钥或已有数据库。
func TestArxivLiveGraph(t *testing.T) {
	runArxivLiveGraph(t, false)
}

func TestArxivLiveKeywordGraph(t *testing.T) {
	if os.Getenv("AUTORESEARCH_ARXIV_KEYWORD_LIVE") != "1" {
		t.Skip("设置 AUTORESEARCH_ARXIV_KEYWORD_LIVE=1 检查上游关键词搜索")
	}
	runArxivLiveGraph(t, true)
}

func runArxivLiveGraph(t *testing.T, search bool) {
	t.Helper()
	if os.Getenv("AUTORESEARCH_ARXIV_LIVE") != "1" {
		t.Skip("设置 AUTORESEARCH_ARXIV_LIVE=1 执行真实 MCP 联调")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	dataDir := t.TempDir()
	source, closeSource, err := mcp.OpenSource(ctx, config.AcademicConfig{Mode: "arxiv_mcp", TimeoutSeconds: 60}, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer closeSource()
	resolved, err := source.Resolve(ctx, "1706.03762")
	if err != nil || resolved.Title != "Attention Is All You Need" || resolved.Year != 2017 {
		t.Fatalf("真实 get_abstract 失败: %+v %v", resolved, err)
	}
	t.Logf("真实 get_abstract: %s (%d)，摘要 %d 字符", resolved.Title, resolved.Year, len([]rune(resolved.Abstract)))
	source = &liveLoggedSource{PaperSource: source, t: t}
	store, err := sqlite.Open(ctx, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	project, err := store.CreateProject(ctx, "local-user", "arXiv MCP 联调")
	if err != nil {
		t.Fatal(err)
	}
	scope := domain.Scope{UserID: "local-user", ProjectID: project.ID}
	papers, evidence := NewPaperService(store), NewEvidenceService(store)
	deps := ResearchGraphDeps{Scope: scope, Project: project, SessionID: "live-session", TurnID: "live-turn", Academic: AcademicToolsDeps{Academic: NewAcademicService(source), Papers: papers, Evidence: evidence}}
	// 脚本模型只固定工具选择，工具传输、来源、Graph 和持久化全部真实执行。
	deps.Model = llm.NewHandlerModel(func(_ context.Context, req llm.Request, _ llm.Options) ([]schema.Message, error) {
		if len(req.Functions) == 0 {
			if strings.Contains(req.Messages[0].JoinedText(""), "业务路由器") {
				return []schema.Message{schema.AssistantMessage(`{"route":"academic","scope":"transformer","save":false}`)}, nil
			}
			return []schema.Message{schema.AssistantMessage("已检索并归档 [Attention Is All You Need](https://arxiv.org/abs/1706.03762)，读取范围为摘要。")}, nil
		}
		call := func(name, args string) ([]schema.Message, error) {
			return []schema.Message{schema.NewMessage(schema.RoleAssistant, schema.NewFunctionToolCallBlock("live-"+name, name, args))}, nil
		}
		last := req.Messages[len(req.Messages)-1]
		if last.Role != schema.RoleTool {
			if !search {
				return call("save_paper", `{"reference":"arxiv:1706.03762"}`)
			}
			// 验收目标是指定论文，使用标题短语，避免宽泛关键词的相关性排序影响断言。
			return call("search_papers", `{"text":"ti:\"Attention Is All You Need\"","limit":1}`)
		}
		r := last.FunctionToolResults()[0]
		text := schema.NewMessage(schema.RoleTool, r.ContentBlocks...).JoinedText("")
		if r.IsError {
			t.Fatalf("真实工具 %s 失败: %s", r.Name, text)
		}
		switch r.Name {
		case "search_papers":
			var items []domain.Candidate
			if err := json.Unmarshal([]byte(text), &items); err != nil {
				t.Fatal(err)
			}
			if len(items) != 1 || items[0].SourceKey != "arxiv:1706.03762" {
				t.Fatalf("真实搜索返回异常: %s", text)
			}
			return call("save_paper", `{"reference":"arxiv:1706.03762"}`)
		case "save_paper":
			var paper domain.Paper
			if err := json.Unmarshal([]byte(text), &paper); err != nil {
				t.Fatal(err)
			}
			args, _ := json.Marshal(ReadPaperArgs{PaperID: paper.ID, Section: "abstract"})
			return call("read_paper", string(args))
		case "read_paper":
			return []schema.Message{schema.AssistantMessage("Attention Is All You Need 提出了 Transformer。依据摘要，模型完全基于注意力机制。[来源](https://arxiv.org/abs/1706.03762)，仅阅读摘要。")}, nil
		}
		t.Fatalf("未知工具 %s", r.Name)
		return nil, nil
	})
	compiled, err := BuildResearchGraph(deps)
	if err != nil {
		t.Fatal(err)
	}
	output, err := compiled.Invoke(ctx, []schema.Message{schema.UserMessage("检索 Attention Is All You Need，归档并读取摘要")})
	if err != nil || len(output) != 1 {
		t.Fatalf("真实 Graph 失败: %v %v", output, err)
	}
	items, err := papers.List(ctx, scope, domain.PaperQuery{})
	if err != nil || len(items) != 1 {
		t.Fatalf("论文归档失败: %v %v", items, err)
	}
	docs, err := evidence.ListDocuments(ctx, scope, items[0].ID)
	if err != nil || len(docs) != 1 || docs[0].SHA256 == "" || !strings.Contains(docs[0].Text, "attention") {
		t.Fatalf("摘要归档失败: %v %v", docs, err)
	}
	t.Logf("真实 Graph 归档/读取通过（关键词搜索=%v）: source_key=%s，document_sha256=%s，locator=%s", search, items[0].SourceKey, docs[0].SHA256, docs[0].Locator)
}

type liveLoggedSource struct {
	domain.PaperSource
	t *testing.T
}

func (s *liveLoggedSource) Search(ctx context.Context, q domain.PaperQuery) ([]domain.Candidate, error) {
	items, err := s.PaperSource.Search(ctx, q)
	if err != nil {
		s.t.Logf("真实搜索诊断: %v", err)
	}
	return items, err
}
