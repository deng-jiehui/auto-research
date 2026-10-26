package mcp

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/deng-jiehui/auto-research/internal/config"
	"github.com/deng-jiehui/auto-research/internal/domain"
	mcpext "github.com/goharness/goharness/extensions/mcp"
)

// OpenSource 在启动时完成协议握手和工具发现，返回唯一连接的关闭函数。
func OpenSource(ctx context.Context, cfg config.AcademicConfig, dataDir string) (domain.PaperSource, func(), error) {
	cfg = cfg.Effective()
	if cfg.Mode == "fixture" {
		return NewFixtureSource(), func() {}, nil
	}
	if cfg.Mode != "arxiv_mcp" {
		return nil, nil, fmt.Errorf("不支持的学术来源模式 %q", cfg.Mode)
	}
	storage, err := filepath.Abs(filepath.Join(dataDir, "arxiv-mcp"))
	if err != nil {
		return nil, nil, err
	}
	client, err := mcpext.NewClient(mcpext.Config{
		Transport: mcpext.TransportStdio, Command: cfg.Command,
		Args:       []string{"--from", "arxiv-mcp-server==0.8.2", "arxiv-mcp-server", "--storage-path", storage},
		ClientName: "autoresearch", ClientVersion: "0.3.0",
		Policy: mcpext.Policy{Strict: true, AllowedCommands: []string{cfg.Command}, AllowedTools: []string{"search_papers", "get_abstract"}},
	})
	if err != nil {
		return nil, nil, fmt.Errorf("启动 arXiv MCP: %w", err)
	}
	closeSource := func() { _ = client.Close() }
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	initCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	available, err := client.Tools(initCtx)
	if err != nil {
		closeSource()
		return nil, nil, fmt.Errorf("连接 arXiv MCP: %w", err)
	}
	found := map[string]bool{}
	for _, item := range available {
		found[item.Spec().Name] = true
	}
	for _, name := range []string{"search_papers", "get_abstract"} {
		if !found[name] {
			closeSource()
			return nil, nil, fmt.Errorf("arXiv MCP 缺少工具 %s", name)
		}
	}
	source, err := NewArxivSource(client, timeout)
	if err != nil {
		closeSource()
		return nil, nil, err
	}
	return source, closeSource, nil
}
