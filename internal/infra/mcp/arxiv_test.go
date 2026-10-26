package mcp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/deng-jiehui/auto-research/internal/domain"
	mcpext "github.com/goharness/goharness/extensions/mcp"
	sdkclient "github.com/mark3labs/mcp-go/client"
	sdkmcp "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func testArxivSource(t *testing.T, handler func(context.Context, sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error)) *ArxivSource {
	t.Helper()
	serverValue := server.NewMCPServer("arxiv-protocol", "0.8.2", server.WithToolCapabilities(true))
	serverValue.AddTool(sdkmcp.NewTool("search_papers"), handler)
	serverValue.AddTool(sdkmcp.NewTool("get_abstract"), handler)
	sdk, err := sdkclient.NewInProcessClient(serverValue)
	if err != nil {
		t.Fatal(err)
	}
	client, err := mcpext.NewClient(mcpext.Config{SDKClient: sdk})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	source, err := NewArxivSource(client, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return source
}

func TestArxivSourceUsesActualSchemaAndCachesAbstract(t *testing.T) {
	calls := 0
	source := testArxivSource(t, func(_ context.Context, req sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
		calls++
		args := req.GetArguments()
		if req.Params.Name == "search_papers" {
			if args["query"] != "transformer" || args["max_results"] != float64(1) || args["abstract_mode"] != "full" || args["date_from"] != "2017-01-01" || args["date_to"] != "2017-12-31" {
				t.Errorf("搜索参数未匹配真实 schema: %v", args)
			}
			return sdkmcp.NewToolResultStructured(map[string]any{"papers": []any{map[string]any{"id": "1706.03762", "title": "Attention Is All You Need", "published": "2017-06-12", "authors": []string{"Ashish Vaswani"}, "abstract": "Attention based models.", "url": "https://arxiv.org/pdf/1706.03762"}}}, "额外说明不是 JSON"), nil
		}
		t.Error("完整摘要已由搜索返回，不应重复远程读取")
		return sdkmcp.NewToolResultError("unexpected"), nil
	})
	items, err := source.Search(context.Background(), domain.PaperQuery{Text: "transformer", Limit: 1, YearFrom: 2017, YearTo: 2017})
	if err != nil || len(items) != 1 {
		t.Fatalf("搜索失败: %v %v", items, err)
	}
	doc, err := source.Read(context.Background(), items[0].SourceKey, "abstract")
	if err != nil || doc.Text != "Attention based models." || calls != 1 {
		t.Fatalf("摘要读取失败: %v %v calls=%d", doc, err, calls)
	}
	if _, err := source.Read(context.Background(), items[0].SourceKey, "full"); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("全文请求不能伪装成摘要: %v", err)
	}
}

func TestArxivSourceClassifiesRateLimit(t *testing.T) {
	calls := 0
	source := testArxivSource(t, func(context.Context, sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
		calls++
		return sdkmcp.NewToolResultText(`{"status":"rate_limited","message":"retry after 60 seconds"}`), nil
	})
	if _, err := source.Search(context.Background(), domain.PaperQuery{Text: "transformer", Limit: 1}); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("限流不能当作空搜索结果: %v", err)
	}
	if _, err := source.Search(context.Background(), domain.PaperQuery{Text: "transformer", Limit: 1}); !errors.Is(err, domain.ErrUnavailable) || calls != 1 {
		t.Fatalf("冷却期间不应再次访问上游: calls=%d err=%v", calls, err)
	}
}

func TestArxivSourceResolvesActualMetadataResponse(t *testing.T) {
	source := testArxivSource(t, func(_ context.Context, req sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
		if req.Params.Name != "get_abstract" || req.GetArguments()["paper_id"] != "1706.03762" {
			t.Errorf("元数据参数错误: %+v", req)
		}
		return sdkmcp.NewToolResultText(`{"status":"success","paper_id":"1706.03762","title":"Attention Is All You Need","authors":["Ashish Vaswani"],"published":"2017-06-12","abstract":"Attention based models.","pdf_url":"https://arxiv.org/pdf/1706.03762"}`), nil
	})
	item, err := source.Resolve(context.Background(), "https://arxiv.org/abs/1706.03762v1")
	if err != nil || item.Year != 2017 || item.SourceKey != "arxiv:1706.03762" {
		t.Fatalf("元数据解析错误: %v %v", item, err)
	}
}
