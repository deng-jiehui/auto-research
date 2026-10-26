package httpapi_test

import (
	"context"
	"testing"

	"github.com/deng-jiehui/auto-research/internal/app"
	"github.com/deng-jiehui/auto-research/internal/domain"
	"github.com/deng-jiehui/auto-research/internal/infra/mcp"
	"github.com/deng-jiehui/auto-research/internal/infra/sqlite"
	"github.com/deng-jiehui/auto-research/internal/transport/httpapi"
)

func TestFullTextHTTPFetchReadAndQuote(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	p, err := store.CreateProject(ctx, "local-user", "全文接口")
	if err != nil {
		t.Fatal(err)
	}
	scope := domain.Scope{UserID: "local-user", ProjectID: p.ID}
	paper, err := store.ImportPaper(ctx, scope, domain.Paper{SourceKey: "arxiv:2401.00001", Title: "Fixture Research"})
	if err != nil {
		t.Fatal(err)
	}
	papers := app.NewPaperService(store)
	academic := app.NewAcademicService(mcp.NewFixtureSource())
	documents := app.NewDocumentService(store, papers, academic)
	h := httpapi.NewHandlerWithAcademicAndEvidence(app.NewProjectService(store), app.NewSessionService(store), nil, papers, academic, app.NewEvidenceService(store), store, "", testLogger(), documents)
	base := "/api/projects/" + p.ID
	response := postJSON(t, h, base+"/papers/"+paper.ID+"/documents/fetch", map[string]any{})
	if response.StatusCode != 200 {
		t.Fatalf("全文获取接口状态=%d", response.StatusCode)
	}
	var doc domain.SourceDocument
	decodeJSON(t, response, &doc)
	if doc.Text != "" || len(doc.Blocks) == 0 {
		t.Fatalf("全文目录=%+v", doc)
	}
	var block domain.DocumentBlock
	decodeJSON(t, get(t, h, base+"/documents/"+doc.ID+"/blocks/b2"), &block)
	if block.Text == "" {
		t.Fatal("章节内容为空")
	}
	quote := postJSON(t, h, base+"/papers/"+paper.ID+"/evidence", map[string]any{"document_id": doc.ID, "block_id": block.ID, "quote": block.Text, "field": "method"})
	if quote.StatusCode != 201 {
		t.Fatalf("保存引用状态=%d", quote.StatusCode)
	}
	var ev domain.Evidence
	decodeJSON(t, quote, &ev)
	var saved domain.Evidence
	decodeJSON(t, get(t, h, base+"/evidence/"+ev.ID), &saved)
	if saved.Quote != block.Text {
		t.Fatal("引用内容不一致")
	}
	other, err := store.CreateProject(ctx, "local-user", "另一个项目")
	if err != nil {
		t.Fatal(err)
	}
	if response = get(t, h, "/api/projects/"+other.ID+"/documents/"+doc.ID); response.StatusCode != 404 {
		t.Fatalf("跨项目读取=%d", response.StatusCode)
	}
}
