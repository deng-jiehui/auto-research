package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/deng-jiehui/auto-research/internal/domain"
	mcpext "github.com/goharness/goharness/extensions/mcp"
)

// ArxivSource 适配 arxiv-mcp-server 0.8.2 的真实工具协议。
// 本阶段读取的是完整摘要；不把摘要冒充论文全文。
type ArxivSource struct {
	base       *MCPSource
	timeout    time.Duration
	gate       chan struct{}
	mu         sync.Mutex
	cache      map[string]domain.Candidate
	retryAfter time.Time
}

func NewArxivSource(client *mcpext.Client, timeout time.Duration) (*ArxivSource, error) {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	base, err := NewMCPSource(client, MCPSourceConfig{Provider: "arxiv", SearchTool: "search_papers"})
	if err != nil {
		return nil, err
	}
	return &ArxivSource{base: base, timeout: timeout, gate: make(chan struct{}, 1), cache: make(map[string]domain.Candidate)}, nil
}

func (s *ArxivSource) call(ctx context.Context, name string, args map[string]any) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	// 上游建议串行访问 arXiv；排队等待也受同一超时约束。
	select {
	case s.gate <- struct{}{}:
		defer func() { <-s.gate }()
	case <-ctx.Done():
		return nil, fmt.Errorf("%w: 等待 arXiv MCP 超时: %v", domain.ErrUnavailable, ctx.Err())
	}
	s.mu.Lock()
	blocked := time.Now().Before(s.retryAfter)
	s.mu.Unlock()
	if blocked {
		return nil, fmt.Errorf("%w: arXiv 限流冷却中，请稍后重试", domain.ErrUnavailable)
	}
	data, err := s.base.call(ctx, name, args)
	if err != nil {
		return nil, fmt.Errorf("%w: arXiv MCP 调用失败: %v", domain.ErrUnavailable, err)
	}
	var status struct {
		Status            string  `json:"status"`
		Message           string  `json:"message"`
		RetryAfterSeconds float64 `json:"retry_after_seconds"`
	}
	if err := json.Unmarshal(data, &status); err != nil {
		return nil, fmt.Errorf("%w: arXiv MCP 返回非 JSON 数据", domain.ErrUnavailable)
	}
	if status.Status != "" && status.Status != "success" {
		if status.Status == "rate_limited" {
			delay := time.Duration(status.RetryAfterSeconds * float64(time.Second))
			if delay <= 0 {
				delay = time.Minute
			}
			if delay > 10*time.Minute {
				delay = 10 * time.Minute
			}
			s.mu.Lock()
			s.retryAfter = time.Now().Add(delay)
			s.mu.Unlock()
		}
		return nil, fmt.Errorf("%w: arXiv MCP 状态 %s: %s", domain.ErrUnavailable, status.Status, status.Message)
	}
	return data, nil
}

func (s *ArxivSource) Search(ctx context.Context, query domain.PaperQuery) ([]domain.Candidate, error) {
	if query.Limit <= 0 {
		query.Limit = 5
	}
	args := map[string]any{"query": query.Text, "max_results": query.Limit, "abstract_mode": "full"}
	if query.YearFrom > 0 {
		args["date_from"] = fmt.Sprintf("%04d-01-01", query.YearFrom)
	}
	if query.YearTo > 0 {
		args["date_to"] = fmt.Sprintf("%04d-12-31", query.YearTo)
	}
	data, err := s.call(ctx, "search_papers", args)
	if err != nil {
		return nil, err
	}
	items, err := ParseCandidates("arxiv", data)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", domain.ErrUnavailable, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.cache) > 200 {
		clear(s.cache)
	}
	for _, item := range items {
		s.cache[item.SourceKey] = item
	}
	return items, nil
}

func (s *ArxivSource) Resolve(ctx context.Context, reference string) (domain.Candidate, error) {
	key, err := domain.NormalizePaperKey(reference)
	if err != nil || !strings.HasPrefix(key, "arxiv:") {
		return domain.Candidate{}, fmt.Errorf("%w: 此来源仅支持 arXiv 编号或链接", domain.ErrInvalid)
	}
	s.mu.Lock()
	cached, ok := s.cache[key]
	s.mu.Unlock()
	if ok {
		return cached, nil
	}
	data, err := s.call(ctx, "get_abstract", map[string]any{"paper_id": strings.TrimPrefix(key, "arxiv:")})
	if err != nil {
		return domain.Candidate{}, err
	}
	var item map[string]any
	if err := json.Unmarshal(data, &item); err != nil {
		return domain.Candidate{}, err
	}
	item["id"] = item["paper_id"]
	item["url"] = "https://arxiv.org/abs/" + strings.TrimPrefix(key, "arxiv:")
	wrapped, err := json.Marshal(map[string]any{"papers": []any{item}})
	if err != nil {
		return domain.Candidate{}, err
	}
	items, err := ParseCandidates("arxiv", wrapped)
	if err != nil {
		return domain.Candidate{}, fmt.Errorf("%w: %v", domain.ErrUnavailable, err)
	}
	if len(items) != 1 || items[0].SourceKey != key {
		return domain.Candidate{}, fmt.Errorf("%w: arXiv 返回身份不匹配", domain.ErrUnavailable)
	}
	s.mu.Lock()
	if len(s.cache) > 200 {
		clear(s.cache)
	}
	s.cache[key] = items[0]
	s.mu.Unlock()
	return items[0], nil
}

func (s *ArxivSource) Read(ctx context.Context, reference, section string) (domain.SourceDocument, error) {
	if section != "" && section != "abstract" {
		return domain.SourceDocument{}, fmt.Errorf("%w: 当前只支持 abstract 摘要读取，尚未接入全文下载", domain.ErrInvalid)
	}
	item, err := s.Resolve(ctx, reference)
	if err != nil {
		return domain.SourceDocument{}, err
	}
	if strings.TrimSpace(item.Abstract) == "" {
		return domain.SourceDocument{}, fmt.Errorf("%w: 来源未提供摘要", domain.ErrUnavailable)
	}
	return domain.SourceDocument{Text: item.Abstract, Locator: "arxiv:" + item.ArxivID + ":abstract"}, nil
}
