package documents

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/deng-jiehui/auto-research/internal/domain"
)

type limitedOutput struct{ bytes.Buffer }

func (b *limitedOutput) Write(data []byte) (int, error) {
	if b.Len()+len(data) > 32<<20 {
		return 0, fmt.Errorf("PDF 提取文本过长")
	}
	return b.Buffer.Write(data)
}

func extractPDF(ctx context.Context, data []byte, command string) (string, error) {
	if !bytes.HasPrefix(data, []byte("%PDF-")) {
		return "", fmt.Errorf("%w：来源没有返回有效 PDF", domain.ErrUnavailable)
	}
	executable, err := exec.LookPath(command)
	if err != nil {
		return "", fmt.Errorf("%w：PDF 提取需要 pdftotext，请配置 document_pdf_command；当前摘要仍可使用", domain.ErrUnavailable)
	}
	dir, err := os.MkdirTemp("", "autoresearch-pdf-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	path := dir + string(os.PathSeparator) + "source.pdf"
	if err = os.WriteFile(path, data, 0600); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	process := exec.CommandContext(ctx, executable, "-layout", "-enc", "UTF-8", path, "-")
	var out, stderr limitedOutput
	process.Stdout = &out
	process.Stderr = &stderr
	if err = process.Run(); err != nil {
		return "", fmt.Errorf("%w：PDF 解析失败：%v", domain.ErrUnavailable, err)
	}
	pages := strings.Split(domain.NormalizeSourceText(out.String()), "\f")
	var result strings.Builder
	for i, page := range pages {
		if strings.TrimSpace(page) != "" {
			fmt.Fprintf(&result, "# 第 %d 页\n\n%s\n\n", i+1, strings.TrimSpace(page))
		}
	}
	if len([]rune(strings.TrimSpace(out.String()))) < 20 {
		return "", fmt.Errorf("%w：PDF 没有可提取文本，可能为扫描件；尚未接入 OCR", domain.ErrUnavailable)
	}
	return strings.TrimSpace(result.String()), nil
}
