package mcp

import (
	"context"
	"testing"

	mcpext "github.com/goharness/goharness/extensions/mcp"
	sdkclient "github.com/mark3labs/mcp-go/client"
	sdkmcp "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/deng-jiehui/auto-research/internal/domain"
)

func TestMCPSourceCallsDiscoveredToolAndNormalizesResult(t *testing.T) {
	serverValue := server.NewMCPServer("fixture", "1.0.0", server.WithToolCapabilities(true))
	serverValue.AddTool(sdkmcp.NewTool("search", sdkmcp.WithString("query")), func(context.Context, sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
		return sdkmcp.NewToolResultStructured(map[string]any{"papers": []any{map[string]any{
			"id": "2401.00001v2", "title": "Fixture Research", "published": "2024-01-02", "summary": "摘要",
		}}}, ""), nil
	})
	sdk, err := sdkclient.NewInProcessClient(serverValue)
	if err != nil {
		t.Fatal(err)
	}
	client, err := mcpext.NewClient(mcpext.Config{SDKClient: sdk})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	source, err := NewMCPSource(client, MCPSourceConfig{Provider: "arxiv", SearchTool: "search"})
	if err != nil {
		t.Fatal(err)
	}
	items, err := source.Search(context.Background(), domain.PaperQuery{Text: "fixture"})
	if err != nil || len(items) != 1 || items[0].SourceKey != "arxiv:2401.00001" {
		t.Fatalf("items=%+v err=%v", items, err)
	}
}

func TestParseCandidatesAcceptsEmptySearch(t *testing.T) {
	items, err := ParseCandidates("arxiv", []byte(`{"papers":[]}`))
	if err != nil || len(items) != 0 {
		t.Fatalf("无匹配结果应正常返回空列表: items=%v err=%v", items, err)
	}
}

func TestMCPSourceRejectsErrorPayload(t *testing.T) {
	serverValue := server.NewMCPServer("errors", "1", server.WithToolCapabilities(true))
	serverValue.AddTool(sdkmcp.NewTool("search"), func(context.Context, sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
		result := sdkmcp.NewToolResultText(`{"papers":[{"id":"1706.03762","title":"Attention Is All You Need"}]}`)
		result.IsError = true
		return result, nil
	})
	sdk, err := sdkclient.NewInProcessClient(serverValue)
	if err != nil {
		t.Fatal(err)
	}
	client, err := mcpext.NewClient(mcpext.Config{SDKClient: sdk})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	source, err := NewMCPSource(client, MCPSourceConfig{Provider: "arxiv", SearchTool: "search"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = source.Search(context.Background(), domain.PaperQuery{Text: "attention"}); err == nil {
		t.Fatal("MCP 的错误结果不能作为成功论文返回")
	}
}
