package app_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/deng-jiehui/auto-research/internal/app"
	"github.com/deng-jiehui/auto-research/internal/domain"
	"github.com/deng-jiehui/auto-research/internal/infra/sqlite"
)

type fullTextSource struct {
	reads int
	err   error
	text  string
}

func (s *fullTextSource) Search(context.Context, domain.PaperQuery) ([]domain.Candidate, error) {
	return nil, nil
}
func (s *fullTextSource) Resolve(context.Context, string) (domain.Candidate, error) {
	return domain.Candidate{}, nil
}
func (s *fullTextSource) Read(context.Context, string, string) (domain.SourceDocument, error) {
	s.reads++
	text := s.text
	if text == "" {
		text = "# 方法\n\n多变量预测采用统一的训练集。重复短语，重复短语。\n\n# 结果\n\n测试误差降低 10%，但小样本评测仍有局限。"
	}
	return domain.SourceDocument{Text: text, ContentKind: "html", SourceVersion: "v2", ParserVersion: "fixture-v1", Locator: "arxiv:2401.00001v2:fulltext", Original: []byte("<article>fixture</article>")}, s.err
}

func TestDocumentFetchReadSearchAndQuoteSurviveRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := sqlite.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	p, err := store.CreateProject(ctx, "alice", "时间序列")
	if err != nil {
		t.Fatal(err)
	}
	scope := domain.Scope{UserID: "alice", ProjectID: p.ID}
	paper, err := store.ImportPaper(ctx, scope, domain.Paper{SourceKey: "arxiv:2401.00001", Title: "Forecast"})
	if err != nil {
		t.Fatal(err)
	}
	source := &fullTextSource{}
	svc := app.NewDocumentService(store, app.NewPaperService(store), app.NewAcademicService(source))
	doc, err := svc.Fetch(ctx, scope, paper.ID)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Text != "" || len(doc.Blocks) < 4 || doc.OriginalArtifactID == "" {
		t.Fatalf("目录应返回定位而不是整篇正文：%+v", doc)
	}
	if _, err = svc.Fetch(ctx, scope, paper.ID); err != nil || source.reads != 1 {
		t.Fatalf("成功结果没有复用：reads=%d err=%v", source.reads, err)
	}
	source.err = domain.ErrUnavailable
	if _, err = svc.Fetch(ctx, scope, paper.ID, true); err == nil {
		t.Fatal("刷新失败应报告失败")
	}
	source.err = nil
	if _, err = svc.Fetch(ctx, scope, paper.ID); err != nil {
		t.Fatal(err)
	}
	status, err := svc.Status(ctx, scope, paper.ID)
	if err != nil || status.State != "ready" || status.DocumentID != doc.ID {
		t.Fatalf("复用已有全文后仍保留失败状态: %+v %v", status, err)
	}
	hits, err := svc.Search(ctx, scope, "多变量预测", 10)
	if err != nil || len(hits) != 1 {
		t.Fatalf("项目检索：%+v %v", hits, err)
	}
	block, err := svc.Read(ctx, scope, doc.ID, hits[0].ID)
	if err != nil || !strings.Contains(block.Text, "统一的训练集") {
		t.Fatal(err)
	}
	ev, err := svc.SaveQuote(ctx, scope, domain.Evidence{PaperID: paper.ID, DocumentID: doc.ID, BlockID: block.ID, Field: "dataset", Value: "统一训练集", Quote: "多变量预测采用统一的训练集。"})
	if err != nil {
		t.Fatal(err)
	}
	again, err := svc.SaveQuote(ctx, scope, domain.Evidence{PaperID: paper.ID, DocumentID: doc.ID, BlockID: block.ID, Field: "dataset", Value: "统一训练集", Quote: ev.Quote})
	if err != nil || again.ID != ev.ID {
		t.Fatalf("证据重放重复：%v", err)
	}
	if _, err = svc.SaveQuote(ctx, scope, domain.Evidence{PaperID: paper.ID, DocumentID: doc.ID, BlockID: block.ID, Quote: "重复短语"}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("重复引用必须消歧：%v", err)
	}
	other, err := store.CreateProject(ctx, "alice", "其他项目")
	if err != nil {
		t.Fatal(err)
	}
	otherScope := domain.Scope{UserID: "alice", ProjectID: other.ID}
	if _, err = svc.Read(ctx, otherScope, doc.ID, block.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("跨项目读取：%v", err)
	}
	if foreign, err := svc.Search(ctx, otherScope, "预测", 10); err != nil || len(foreign) != 0 {
		t.Fatalf("检索越权：%v", err)
	}
	store.Close()
	store, err = sqlite.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	svc = app.NewDocumentService(store, app.NewPaperService(store), app.NewAcademicService(source))
	got, err := svc.Evidence(ctx, scope, ev.ID)
	if err != nil || got.Quote != ev.Quote {
		t.Fatalf("重启后证据：%v", err)
	}
	loaded, err := store.GetDocument(ctx, scope, got.DocumentID)
	if err != nil {
		t.Fatal(err)
	}
	if err = domain.ValidateEvidence(loaded, got); err != nil {
		t.Fatal(err)
	}
	source.text = "# 新方法\n\n🌟 Multivariate forecasting uses a linear baseline.\n\n实验继续保留旧版本的所有证据。"
	newDoc, err := svc.Fetch(ctx, scope, paper.ID, true)
	if err != nil || newDoc.ID == doc.ID {
		t.Fatalf("新正文未独立冻结: %v", err)
	}
	english, err := svc.Search(ctx, scope, "linear baseline", 3)
	if err != nil || len(english) != 1 || english[0].DocumentID != newDoc.ID {
		t.Fatalf("英文词项检索: %+v %v", english, err)
	}
	unicode, err := svc.SaveQuote(ctx, scope, domain.Evidence{DocumentID: newDoc.ID, BlockID: english[0].ID, Quote: "🌟 Multivariate", Field: "method"})
	if err != nil || unicode.EndRune-unicode.StartRune != len([]rune(unicode.Quote)) {
		t.Fatalf("Unicode 区间: %+v %v", unicode, err)
	}
	old, err := store.GetDocument(ctx, scope, got.DocumentID)
	if err != nil {
		t.Fatal(err)
	}
	if err = domain.ValidateEvidence(old, got); err != nil {
		t.Fatalf("新版本破坏旧引用: %v", err)
	}
}

func TestFailedFullTextKeepsAbstractAndPersistedReason(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	p, err := store.CreateProject(ctx, "alice", "失败保留摘要")
	if err != nil {
		t.Fatal(err)
	}
	scope := domain.Scope{UserID: "alice", ProjectID: p.ID}
	paper, err := store.ImportPaper(ctx, scope, domain.Paper{SourceKey: "arxiv:2401.00001", Title: "Forecast", Abstract: "可用摘要"})
	if err != nil {
		t.Fatal(err)
	}
	svc := app.NewDocumentService(store, app.NewPaperService(store), app.NewAcademicService(&fullTextSource{err: domain.ErrUnavailable}))
	if _, err = svc.Fetch(ctx, scope, paper.ID); err == nil {
		t.Fatal("下载失败不能返回成功全文")
	}
	status, err := svc.Status(ctx, scope, paper.ID)
	if err != nil || status.State != "failed" || status.Error == "" {
		t.Fatalf("失败状态：%+v %v", status, err)
	}
	paper, err = store.GetPaper(ctx, scope, paper.ID)
	if err != nil || paper.Abstract != "可用摘要" {
		t.Fatalf("摘要被破坏：%v", err)
	}
}
