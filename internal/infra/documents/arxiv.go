package documents

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/deng-jiehui/auto-research/internal/domain"
)

const maxDownload = 30 << 20

var arxivID = regexp.MustCompile(`^((?:\d{4}\.\d{4,5}|[a-zA-Z.-]+/\d{7}))(v\d+)?$`)

// ArxivSource 复用现有 MCP 元数据来源，在应用基础设施补充公开全文读取。
type ArxivSource struct {
	base       domain.PaperSource
	client     *http.Client
	pdfCommand string
}

func NewArxivSource(base domain.PaperSource, client *http.Client, pdfCommand string) *ArxivSource {
	if client == nil {
		client = &http.Client{Timeout: 90 * time.Second}
	}
	copyClient := *client
	copyClient.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if len(via) >= 5 || !allowedURL(r.URL) {
			return fmt.Errorf("全文重定向超出允许来源")
		}
		return nil
	}
	if pdfCommand == "" {
		pdfCommand = "pdftotext"
	}
	return &ArxivSource{base: base, client: &copyClient, pdfCommand: pdfCommand}
}
func allowedURL(u *url.URL) bool {
	return u.Scheme == "https" && (u.Host == "arxiv.org" || u.Host == "www.arxiv.org" || u.Host == "export.arxiv.org") && u.User == nil
}
func (s *ArxivSource) Search(ctx context.Context, q domain.PaperQuery) ([]domain.Candidate, error) {
	if s.base == nil {
		return nil, domain.ErrUnavailable
	}
	return s.base.Search(ctx, q)
}
func (s *ArxivSource) Resolve(ctx context.Context, r string) (domain.Candidate, error) {
	if s.base == nil {
		return domain.Candidate{}, domain.ErrUnavailable
	}
	return s.base.Resolve(ctx, r)
}
func (s *ArxivSource) FullTextSupported() bool { return true }

func (s *ArxivSource) Read(ctx context.Context, reference, section string) (domain.SourceDocument, error) {
	if section == "" || section == "abstract" {
		if s.base == nil {
			return domain.SourceDocument{}, domain.ErrUnavailable
		}
		return s.base.Read(ctx, reference, "abstract")
	}
	if section != "fulltext" {
		return domain.SourceDocument{}, domain.ErrInvalid
	}
	// 整次获取和解析共享预算，HTML 与 PDF 回退不会各自重置总时限。
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	id := strings.TrimPrefix(strings.TrimSpace(reference), "arxiv:")
	if strings.HasPrefix(id, "https://") {
		u, err := url.Parse(id)
		if err != nil || !allowedURL(u) {
			return domain.SourceDocument{}, domain.ErrInvalid
		}
		id = strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(u.Path, "/abs/"), "/pdf/"), "/html/")
		id = strings.TrimSuffix(id, ".pdf")
	}
	parts := arxivID.FindStringSubmatch(id)
	if len(parts) == 0 {
		return domain.SourceDocument{}, fmt.Errorf("%w：仅支持有效 arXiv 论文身份", domain.ErrInvalid)
	}
	version := parts[2]
	if version == "" {
		version = "unknown"
	}
	data, finalURL, status, err := s.download(ctx, "https://arxiv.org/html/"+id)
	if err == nil {
		text, parseErr := parseHTML(data)
		if parseErr != nil {
			return domain.SourceDocument{}, parseErr
		}
		if tail := arxivID.FindStringSubmatch(strings.TrimPrefix(finalURL.Path, "/html/")); len(tail) > 2 && tail[2] != "" {
			version = tail[2]
		}
		return domain.SourceDocument{Text: text, ContentKind: "html", SourceVersion: version, ParserVersion: "arxiv-html-v1", Locator: fullTextLocator(parts[1], version), Original: data}, nil
	}
	if status != 404 && status != 410 {
		return domain.SourceDocument{}, err
	}
	data, finalURL, _, err = s.download(ctx, "https://arxiv.org/pdf/"+id)
	if err != nil {
		return domain.SourceDocument{}, err
	}
	if tail := arxivID.FindStringSubmatch(strings.TrimSuffix(strings.TrimPrefix(finalURL.Path, "/pdf/"), ".pdf")); len(tail) > 2 && tail[2] != "" {
		version = tail[2]
	}
	text, err := extractPDF(ctx, data, s.pdfCommand)
	if err != nil {
		return domain.SourceDocument{}, err
	}
	return domain.SourceDocument{Text: text, ContentKind: "pdf", SourceVersion: version, ParserVersion: "pdftotext-layout-v1", Locator: fullTextLocator(parts[1], version), Original: data}, nil
}

func fullTextLocator(id, version string) string {
	if version != "unknown" {
		id += version
	}
	return "arxiv:" + id + ":fulltext"
}

func (s *ArxivSource) download(ctx context.Context, address string) ([]byte, *url.URL, int, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, nil, 0, err
	}
	r.Header.Set("User-Agent", "AutoResearch/0.1 (public-paper-reader)")
	response, err := s.client.Do(r)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, nil, 0, err
		}
		return nil, nil, 0, fmt.Errorf("%w：全文请求失败：%v", domain.ErrUnavailable, err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, nil, response.StatusCode, fmt.Errorf("%w：arXiv 全文返回 HTTP %d，请稍后重试", domain.ErrUnavailable, response.StatusCode)
	}
	if response.ContentLength > maxDownload {
		return nil, nil, 200, fmt.Errorf("%w：全文超过 30 MiB", domain.ErrInvalid)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxDownload+1))
	if err != nil {
		return nil, nil, 200, err
	}
	if len(data) > maxDownload {
		return nil, nil, 200, fmt.Errorf("%w：全文超过 30 MiB", domain.ErrInvalid)
	}
	return data, response.Request.URL, 200, nil
}
