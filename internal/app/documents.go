package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/deng-jiehui/auto-research/internal/domain"
)

type DocumentService struct {
	repo     domain.DocumentRepository
	papers   *PaperService
	academic *AcademicService
	fetchMu  sync.Mutex
}

func NewDocumentService(repo domain.DocumentRepository, papers *PaperService, academic *AcademicService) *DocumentService {
	return &DocumentService{repo: repo, papers: papers, academic: academic}
}

func (s *DocumentService) Fetch(ctx context.Context, scope domain.Scope, paperID string, refresh ...bool) (domain.SourceDocument, error) {
	paper, err := s.papers.Get(ctx, scope, paperID)
	if err != nil {
		return domain.SourceDocument{}, err
	}
	s.fetchMu.Lock()
	defer s.fetchMu.Unlock()
	docs, err := s.repo.ListDocuments(ctx, scope, paperID)
	if err != nil {
		return domain.SourceDocument{}, err
	}
	if len(refresh) == 0 || !refresh[0] {
		for _, doc := range docs {
			if doc.ContentKind != "abstract" && doc.ContentKind != "" {
				if err = s.repo.SaveDocumentFetch(ctx, scope, domain.DocumentFetch{PaperID: paperID, State: "ready", DocumentID: doc.ID}); err != nil {
					return domain.SourceDocument{}, err
				}
				return s.Contents(ctx, scope, doc.ID)
			}
		}
	}
	if err = s.repo.SaveDocumentFetch(ctx, scope, domain.DocumentFetch{PaperID: paperID, State: "running"}); err != nil {
		return domain.SourceDocument{}, err
	}
	doc, err := s.academic.Read(ctx, paper.SourceKey, "fulltext")
	if err == nil {
		doc.ID = ""
		doc.PaperID = paperID
		doc.ArtifactID = ""
		doc.OriginalArtifactID = ""
		if doc.ContentKind == "" || doc.ContentKind == "abstract" || strings.TrimSpace(doc.Text) == "" {
			err = fmt.Errorf("%w：来源未提供可用全文", domain.ErrUnavailable)
		} else {
			doc, err = NewEvidenceService(s.repo).SaveDocument(ctx, scope, doc)
		}
	}
	if err != nil {
		// 请求取消也要留下可重试的失败原因，不能永久停留在 running。
		statusCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if saveErr := s.repo.SaveDocumentFetch(statusCtx, scope, domain.DocumentFetch{PaperID: paperID, State: "failed", Error: truncateRunes(err.Error(), 500)}); saveErr != nil {
			return domain.SourceDocument{}, errors.Join(err, saveErr)
		}
		return domain.SourceDocument{}, err
	}
	if err = s.repo.SaveDocumentFetch(ctx, scope, domain.DocumentFetch{PaperID: paperID, State: "ready", DocumentID: doc.ID}); err != nil {
		return domain.SourceDocument{}, err
	}
	return s.Contents(ctx, scope, doc.ID)
}

func (s *DocumentService) Status(ctx context.Context, scope domain.Scope, paperID string) (domain.DocumentFetch, error) {
	if _, err := s.papers.Get(ctx, scope, paperID); err != nil {
		return domain.DocumentFetch{}, err
	}
	f, err := s.repo.GetDocumentFetch(ctx, scope, paperID)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.DocumentFetch{PaperID: paperID, State: "idle"}, nil
	}
	return f, err
}
func (s *DocumentService) Contents(ctx context.Context, scope domain.Scope, documentID string) (domain.SourceDocument, error) {
	doc, err := s.repo.GetDocument(ctx, scope, documentID)
	if err != nil {
		return domain.SourceDocument{}, err
	}
	doc.Blocks = domain.DocumentBlocks(doc)
	for i := range doc.Blocks {
		doc.Blocks[i].Text = ""
	}
	doc.Text = ""
	doc.Original = nil
	return doc, nil
}
func (s *DocumentService) Read(ctx context.Context, scope domain.Scope, documentID, blockID string) (domain.DocumentBlock, error) {
	doc, err := s.repo.GetDocument(ctx, scope, documentID)
	if err != nil {
		return domain.DocumentBlock{}, err
	}
	for _, block := range domain.DocumentBlocks(doc) {
		if block.ID == blockID {
			return block, nil
		}
	}
	return domain.DocumentBlock{}, domain.ErrNotFound
}
func (s *DocumentService) Search(ctx context.Context, scope domain.Scope, query string, limit int) ([]domain.DocumentHit, error) {
	return s.searchPaper(ctx, scope, query, limit, "")
}

// 正式调研使用可信纳入集限定数据库检索，不能先返回其他论文的正文再交给模型筛选。
func (s *DocumentService) SearchForPapers(ctx context.Context, scope domain.Scope, query string, limit int, paperIDs []string) ([]domain.DocumentHit, error) {
	if limit < 0 || limit > 30 || len(paperIDs) > 30 {
		return nil, domain.ErrInvalid
	}
	if limit == 0 {
		limit = 10
	}
	result := make([]domain.DocumentHit, 0, limit)
	for _, id := range paperIDs {
		if id == "" {
			return nil, domain.ErrInvalid
		}
		hits, err := s.searchPaper(ctx, scope, query, limit-len(result), id)
		if err != nil {
			return nil, err
		}
		result = append(result, hits...)
		if len(result) >= limit {
			break
		}
	}
	return result, nil
}

func (s *DocumentService) searchPaper(ctx context.Context, scope domain.Scope, query string, limit int, paperID string) ([]domain.DocumentHit, error) {
	query = strings.TrimSpace(query)
	if query == "" || len([]rune(query)) > 500 || limit < 0 || limit > 30 {
		return nil, domain.ErrInvalid
	}
	if limit == 0 {
		limit = 10
	}
	hits, err := s.repo.SearchDocumentBlocks(ctx, scope, query, paperID, limit)
	if err != nil {
		return nil, err
	}
	for i := range hits {
		runes := []rune(hits[i].Text)
		if len(runes) <= 700 {
			continue
		}
		start := 0
		for _, term := range strings.Fields(query) {
			if index := strings.Index(strings.ToLower(hits[i].Text), strings.ToLower(term)); index >= 0 {
				start = len([]rune(hits[i].Text[:index])) - 120
				break
			}
		}
		if start < 0 {
			start = 0
		}
		if start+700 > len(runes) {
			start = len(runes) - 700
		}
		hits[i].Text = string(runes[start : start+700])
		hits[i].StartRune += start
		hits[i].EndRune = hits[i].StartRune + 700
	}
	return hits, nil
}
func (s *DocumentService) Evidence(ctx context.Context, scope domain.Scope, id string) (domain.Evidence, error) {
	return s.repo.GetEvidence(ctx, scope, id)
}
func (s *DocumentService) SaveQuote(ctx context.Context, scope domain.Scope, e domain.Evidence) (domain.Evidence, error) {
	block, err := s.Read(ctx, scope, e.DocumentID, e.BlockID)
	if err != nil {
		return domain.Evidence{}, err
	}
	if e.PaperID != "" && e.PaperID != block.PaperID {
		return domain.Evidence{}, domain.ErrInvalid
	}
	text, quote := []rune(block.Text), []rune(e.Quote)
	if len(quote) == 0 || len(quote) > 4000 || len([]rune(e.Field)) > 120 || len([]rune(e.Value)) > 4000 {
		return domain.Evidence{}, domain.ErrInvalid
	}
	index, count := -1, 0
	positions := make([]string, 0, 5)
	for i := 0; i+len(quote) <= len(text); i++ {
		if string(text[i:i+len(quote)]) == e.Quote {
			index = i
			count++
			if len(positions) < 5 {
				positions = append(positions, fmt.Sprintf("[%d,%d)", block.StartRune+i, block.StartRune+i+len(quote)))
			}
		}
	}
	if count != 1 {
		return domain.Evidence{}, fmt.Errorf("%w：引用在所选块中匹配 %d 次，候选区间 %v；请提供包含上下文的唯一原文或选择更精确的块", domain.ErrInvalid, count, positions)
	}
	e.PaperID = block.PaperID
	e.ProjectID = scope.ProjectID
	e.StartRune = block.StartRune + index
	e.EndRune = e.StartRune + len(quote)
	h := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%d\x00%d", e.DocumentID, e.BlockID, e.Field, e.Value, e.StartRune, e.EndRune)))
	e.ID = hex.EncodeToString(h[:])
	return NewEvidenceService(s.repo).Save(ctx, scope, e)
}
