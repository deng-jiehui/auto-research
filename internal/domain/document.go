package domain

import (
	"context"
	"fmt"
	"strings"
)

type DocumentBlock struct {
	ID         string `json:"id"`
	DocumentID string `json:"document_id"`
	PaperID    string `json:"paper_id"`
	Title      string `json:"title"`
	Text       string `json:"text,omitempty"`
	StartRune  int    `json:"start_rune"`
	EndRune    int    `json:"end_rune"`
}

type DocumentHit struct {
	DocumentBlock
	Locator     string `json:"locator"`
	ContentKind string `json:"content_kind"`
}

type DocumentFetch struct {
	PaperID    string `json:"paper_id"`
	State      string `json:"state"`
	DocumentID string `json:"document_id,omitempty"`
	Error      string `json:"error,omitempty"`
	UpdatedAt  string `json:"updated_at"`
}

type DocumentRepository interface {
	EvidenceRepository
	GetEvidence(context.Context, Scope, string) (Evidence, error)
	SearchDocumentBlocks(context.Context, Scope, string, string, int) ([]DocumentHit, error)
	SaveDocumentFetch(context.Context, Scope, DocumentFetch) error
	GetDocumentFetch(context.Context, Scope, string) (DocumentFetch, error)
}

// DocumentBlocks 的定位始终引用冻结文本，模型和前端不自行估算 Unicode 偏移。
func DocumentBlocks(document SourceDocument) []DocumentBlock {
	runes := []rune(document.Text)
	blocks := make([]DocumentBlock, 0)
	title := "正文"
	for start := 0; start < len(runes); {
		for start < len(runes) && (runes[start] == '\n' || runes[start] == '\r' || runes[start] == ' ' || runes[start] == '\t') {
			start++
		}
		if start >= len(runes) {
			break
		}
		end := start
		for end < len(runes) && end-start < 2200 {
			if end > start && runes[end] == '\n' && end+1 < len(runes) && runes[end+1] == '\n' {
				break
			}
			end++
		}
		text := string(runes[start:end])
		if strings.HasPrefix(text, "#") {
			title = strings.TrimSpace(strings.TrimLeft(text, "#"))
		}
		blocks = append(blocks, DocumentBlock{ID: fmt.Sprintf("b%d", len(blocks)+1), DocumentID: document.ID, PaperID: document.PaperID, Title: title, Text: text, StartRune: start, EndRune: end})
		start = end
	}
	return blocks
}
