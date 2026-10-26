package app

import (
	"context"
	"fmt"
	"testing"

	"github.com/deng-jiehui/auto-research/internal/domain"
	"github.com/deng-jiehui/auto-research/internal/infra/sqlite"
)

// fiveCandidateSource 只用于阶段四综合验收，模拟五篇候选及一篇仅摘要来源。
type fiveCandidateSource struct {
	items        []domain.Candidate
	abstractOnly string
}

func newFiveCandidateSource() *fiveCandidateSource {
	items := make([]domain.Candidate, 0, 5)
	for i := 1; i <= 5; i++ {
		key := fmt.Sprintf("arxiv:2501.0000%d", i)
		items = append(items, domain.Candidate{
			SourceKey: key,
			Title:     fmt.Sprintf("Reproducible Baseline %d", i),
			Authors:   []string{"Fixture Author"},
			Year:      2025,
			Abstract:  fmt.Sprintf("Reproducible forecasting baseline %d.", i),
			URL:       "https://arxiv.org/abs/" + key[len("arxiv:"):],
			ArxivID:   key[len("arxiv:"):],
			Provider:  "arxiv",
		})
	}
	return &fiveCandidateSource{items: items, abstractOnly: items[4].SourceKey}
}

func (s *fiveCandidateSource) Search(_ context.Context, _ domain.PaperQuery) ([]domain.Candidate, error) {
	return append([]domain.Candidate(nil), s.items...), nil
}

func (s *fiveCandidateSource) Resolve(_ context.Context, reference string) (domain.Candidate, error) {
	key, err := domain.NormalizePaperKey(reference)
	if err != nil {
		return domain.Candidate{}, err
	}
	for _, item := range s.items {
		if item.SourceKey == key {
			return item, nil
		}
	}
	return domain.Candidate{}, domain.ErrNotFound
}

func (s *fiveCandidateSource) Read(_ context.Context, reference, section string) (domain.SourceDocument, error) {
	key, err := domain.NormalizePaperKey(reference)
	if err != nil {
		return domain.SourceDocument{}, err
	}
	if key == s.abstractOnly && section == "fulltext" {
		return domain.SourceDocument{}, domain.ErrUnavailable
	}
	if section != "fulltext" && section != "abstract" {
		return domain.SourceDocument{}, domain.ErrInvalid
	}
	for _, item := range s.items {
		if item.SourceKey != key {
			continue
		}
		text := item.Abstract
		kind := "abstract"
		if section == "fulltext" {
			text = fmt.Sprintf("# Method\n\n%s uses a fixed train/test split.\n\n# Results\n\nThe reported baseline result is 0.%02d.", item.Title, len(item.SourceKey))
			kind = "fulltext"
		}
		return domain.SourceDocument{
			ID:          "doc-" + key,
			PaperID:     key,
			Text:        text,
			ContentKind: kind,
			Locator:     key + ":" + section,
		}, nil
	}
	return domain.SourceDocument{}, domain.ErrNotFound
}

func TestPhaseFourComprehensiveFiveCandidatesAndAbstractFallback(t *testing.T) {
	ctx := t.Context()
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	service := analysisService(store, newFiveCandidateSource())
	scope, turn, run := approveAnalysisInputs(t, service, store, "autoresearch-agent-single-v8")
	if len(run.Candidates) != 5 {
		t.Fatalf("候选数量=%d，期望 5", len(run.Candidates))
	}
	if _, err = service.RunQueued(ctx); err != nil {
		t.Fatal(err)
	}

	after, err := store.GetResearch(ctx, scope, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.ExecutionState != domain.TurnCompleted || after.Phase != "reported" {
		t.Fatalf("综合调研未完成: %+v", after)
	}
	if len(after.WorkItems) != 5 || after.LatestReport == nil {
		t.Fatalf("逐篇分析或报告缺失: work_items=%d report=%v", len(after.WorkItems), after.LatestReport != nil)
	}
	abstractCount := 0
	for _, item := range after.WorkItems {
		if item.State != "completed" {
			t.Fatalf("工作项未完成: %+v", item)
		}
		if item.ContentKind == "abstract" {
			abstractCount++
		}
	}
	if abstractCount != 1 {
		t.Fatalf("仅摘要工作项数量=%d，期望 1", abstractCount)
	}
	matrix := domain.BuildEvidenceMatrix(after)
	if len(matrix.Rows) != 5 {
		t.Fatalf("矩阵行数=%d，期望 5", len(matrix.Rows))
	}
	report, err := store.GetResearchReport(ctx, scope, after.LatestReport.ID)
	if err != nil {
		t.Fatal(err)
	}
	if report.MatrixHash != matrix.Hash || len(report.References) == 0 {
		t.Fatalf("报告未绑定矩阵或引用: %+v", report)
	}
	if turn.ID != after.TurnID {
		t.Fatalf("报告任务未绑定原始 Turn: turn=%s run_turn=%s", turn.ID, after.TurnID)
	}
}
