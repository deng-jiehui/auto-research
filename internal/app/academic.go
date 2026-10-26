package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/deng-jiehui/auto-research/internal/domain"
)

type AcademicService struct {
	source domain.PaperSource
	mode   string
}

func NewAcademicService(source domain.PaperSource, modes ...string) *AcademicService {
	mode := "fixture"
	if len(modes) > 0 {
		mode = modes[0]
	}
	return &AcademicService{source: source, mode: mode}
}

type AcademicStatus struct {
	Mode       string `json:"mode"`
	Configured bool   `json:"configured"`
	ReadScope  string `json:"read_scope"`
}

func (s *AcademicService) Status() AcademicStatus {
	if s == nil {
		return AcademicStatus{ReadScope: "abstract"}
	}
	readScope := "abstract"
	if source, ok := s.source.(interface{ FullTextSupported() bool }); ok && source.FullTextSupported() {
		readScope = "abstract,fulltext"
	}
	return AcademicStatus{Mode: s.mode, Configured: s.source != nil, ReadScope: readScope}
}

func (s *AcademicService) Search(ctx context.Context, query domain.PaperQuery) ([]domain.Candidate, error) {
	if s == nil || s.source == nil {
		return nil, domain.ErrUnavailable
	}
	query.Text = strings.TrimSpace(query.Text)
	if query.Text == "" || len([]rune(query.Text)) > 1000 || query.Limit < 0 || query.Limit > 30 || query.YearFrom < 0 || query.YearTo < 0 || query.YearFrom > 9999 || query.YearTo > 9999 || (query.YearFrom > 0 && query.YearTo > 0 && query.YearFrom > query.YearTo) || (query.Provider != "" && query.Provider != "arxiv") {
		return nil, domain.ErrInvalid
	}
	if query.Limit == 0 {
		query.Limit = 5
	}
	return s.source.Search(ctx, query)
}

func (s *AcademicService) Resolve(ctx context.Context, reference string) (domain.Candidate, error) {
	if s == nil || s.source == nil {
		return domain.Candidate{}, domain.ErrUnavailable
	}
	return s.source.Resolve(ctx, reference)
}

func (s *AcademicService) Read(ctx context.Context, reference, section string) (domain.SourceDocument, error) {
	if s == nil || s.source == nil {
		return domain.SourceDocument{}, domain.ErrUnavailable
	}
	if section != "" && section != "abstract" && section != "fulltext" {
		return domain.SourceDocument{}, fmt.Errorf("%w: 阅读范围须为 abstract 或 fulltext", domain.ErrInvalid)
	}
	return s.source.Read(ctx, reference, section)
}
