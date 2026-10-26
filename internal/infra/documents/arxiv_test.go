package documents

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/deng-jiehui/auto-research/internal/domain"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestArxivHTMLProducesReadableFrozenSections(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://arxiv.org/html/2401.00001v2" {
			t.Fatalf("请求地址=%s", r.URL)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/html"}}, Request: r, Body: io.NopCloser(strings.NewReader(`<html><body><nav>不要收录导航</nav><article><h1>Forecasting</h1><section><h2>方法</h2><p>使用<strong>多变量</strong>数据。</p><script>隐藏脚本</script></section><h2>结果</h2><p>误差降低 10%。</p></article></body></html>`))}, nil
	})}
	s := NewArxivSource(nil, client, "")
	doc, err := s.Read(context.Background(), "arxiv:2401.00001v2", "fulltext")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc.Text, "使用多变量数据。") || strings.Contains(doc.Text, "导航") || strings.Contains(doc.Text, "脚本") {
		t.Fatalf("正文=%q", doc.Text)
	}
	if doc.ContentKind != "html" || doc.SourceVersion != "v2" || len(doc.Original) == 0 {
		t.Fatalf("元数据=%+v", doc)
	}
}

func TestArxivLimitsAndPDFFallbackErrorsRemainExplicit(t *testing.T) {
	for _, tc := range []struct {
		name       string
		htmlStatus int
		body       string
		want       string
	}{
		{"empty", 200, "<html><body><script>x</script></body></html>", "正文"},
		{"rate_limit", 429, "limited", "429"},
		{"missing_parser", 404, "%PDF-1.4\nfixture", "pdftotext"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			count := 0
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				count++
				status := tc.htmlStatus
				if count > 1 {
					status = 200
				}
				return &http.Response{StatusCode: status, Header: make(http.Header), Request: r, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})}
			_, err := NewArxivSource(nil, client, "missing-autoresearch-pdftotext").Read(context.Background(), "arxiv:2401.00001", "fulltext")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("错误=%v", err)
			}
			if tc.htmlStatus == 429 && count != 1 {
				t.Fatal("限流时不应通过 PDF 回退增加来源压力")
			}
		})
	}
	if _, err := NewArxivSource(nil, nil, "").Read(context.Background(), "https://127.0.0.1/private", "fulltext"); err == nil {
		t.Fatal("任意 URL 不应成为下载地址")
	}
}

var _ domain.PaperSource = (*ArxivSource)(nil)

func TestArxivRejectsOversizedDownloadAndForeignRedirect(t *testing.T) {
	for _, scenario := range []string{"oversized", "redirect"} {
		t.Run(scenario, func(t *testing.T) {
			count := 0
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				count++
				if count > 1 {
					t.Fatal("拒绝前不应访问重定向外部地址")
				}
				response := &http.Response{StatusCode: 200, ContentLength: maxDownload + 1, Header: make(http.Header), Request: r, Body: io.NopCloser(strings.NewReader(""))}
				if scenario == "redirect" {
					response.StatusCode = 302
					response.Header.Set("Location", "https://127.0.0.1/private")
				}
				return response, nil
			})}
			if _, err := NewArxivSource(nil, client, "").Read(context.Background(), "arxiv:2401.00001", "fulltext"); err == nil {
				t.Fatal("不可信响应应明确拒绝")
			}
		})
	}
}
