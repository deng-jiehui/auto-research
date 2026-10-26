package documents

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// 仅在显式开启时访问公开论文；不会使用模型、MCP 配置或用户密钥。
func TestArxivFullTextLive(t *testing.T) {
	if os.Getenv("AUTORESEARCH_DOCUMENT_LIVE") != "1" {
		t.Skip("设置 AUTORESEARCH_DOCUMENT_LIVE=1 验证公开全文")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	reference := os.Getenv("AUTORESEARCH_DOCUMENT_REFERENCE")
	if reference == "" {
		reference = "arxiv:2303.08774v6"
	}
	doc, err := NewArxivSource(nil, nil, os.Getenv("AUTORESEARCH_PDFTOTEXT")).Read(ctx, reference, "fulltext")
	if err != nil {
		t.Fatal(err)
	}
	if len([]rune(doc.Text)) < 1000 || len(doc.Original) == 0 {
		t.Fatalf("正文不完整: kind=%s runes=%d", doc.ContentKind, len([]rune(doc.Text)))
	}
	t.Logf("真实全文 kind=%s version=%s runes=%d raw_bytes=%d", doc.ContentKind, doc.SourceVersion, len([]rune(doc.Text)), len(doc.Original))
}

func TestPDFExtractsActualPageBoundaries(t *testing.T) {
	command := os.Getenv("AUTORESEARCH_PDFTOTEXT")
	if command == "" {
		t.Skip("设置 AUTORESEARCH_PDFTOTEXT 指向真实 Poppler pdftotext 验证分页")
	}
	var pdf strings.Builder
	pdf.WriteString("%PDF-1.4\n")
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R 4 0 R] /Count 2 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 5 0 R >> >> /Contents 6 0 R >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 5 0 R >> >> /Contents 7 0 R >>",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	for _, content := range []string{"First page contains the reproducible linear baseline.", "Second page contains evaluation metrics and limitations."} {
		stream := "BT /F1 12 Tf 50 700 Td (" + content + ") Tj ET\n"
		objects = append(objects, fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(stream), stream))
	}
	offsets := []int{0}
	for i, obj := range objects {
		offsets = append(offsets, pdf.Len())
		fmt.Fprintf(&pdf, "%d 0 obj\n%s\nendobj\n", i+1, obj)
	}
	xref := pdf.Len()
	fmt.Fprintf(&pdf, "xref\n0 %d\n0000000000 65535 f \n", len(offsets))
	for _, offset := range offsets[1:] {
		fmt.Fprintf(&pdf, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&pdf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets), xref)
	text, err := extractPDF(context.Background(), []byte(pdf.String()), command)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "# 第 1 页") || !strings.Contains(text, "# 第 2 页") || !strings.Contains(text, "linear baseline") {
		t.Fatalf("分页或正文错误: %q", text)
	}
}

func TestArxivPDFLive(t *testing.T) {
	command := os.Getenv("AUTORESEARCH_PDFTOTEXT")
	if os.Getenv("AUTORESEARCH_DOCUMENT_LIVE") != "1" || command == "" {
		t.Skip("显式开启公开下载并配置 pdftotext 后验证真实 PDF")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	source := NewArxivSource(nil, nil, command)
	data, _, _, err := source.download(ctx, "https://arxiv.org/pdf/1706.03762v7")
	if err != nil {
		t.Fatal(err)
	}
	text, err := extractPDF(ctx, data, command)
	if err != nil || !strings.Contains(text, "Attention Is All You Need") || !strings.Contains(text, "# 第 2 页") {
		t.Fatalf("真实 PDF 解析失败: %v", err)
	}
	t.Logf("真实 PDF bytes=%d runes=%d pages=%d", len(data), len([]rune(text)), strings.Count(text, "# 第 "))
}
