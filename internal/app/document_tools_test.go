package app_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/deng-jiehui/auto-research/internal/app"
	"github.com/deng-jiehui/auto-research/internal/domain"
	"github.com/deng-jiehui/auto-research/internal/infra/sqlite"
	"github.com/goharness/goharness/pkg/tool"
)

func TestPaperToolsFetchBoundedContentsReadBlockAndSaveExactQuote(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	p, err := store.CreateProject(ctx, "alice", "工具读取全文")
	if err != nil {
		t.Fatal(err)
	}
	scope := domain.Scope{UserID: "alice", ProjectID: p.ID}
	paper, err := store.ImportPaper(ctx, scope, domain.Paper{SourceKey: "arxiv:2401.00001", Title: "Forecast"})
	if err != nil {
		t.Fatal(err)
	}
	papers := app.NewPaperService(store)
	academic := app.NewAcademicService(&fullTextSource{})
	documents := app.NewDocumentService(store, papers, academic)
	tools := app.NewAcademicTools(app.AcademicToolsDeps{Scope: scope, Papers: papers, Academic: academic, Evidence: app.NewEvidenceService(store), Documents: documents})
	invoke := func(name string, args any, target any) {
		t.Helper()
		raw, _ := json.Marshal(args)
		for _, item := range tools {
			if item.Spec().Name == name {
				r, err := item.Invoke(ctx, tool.CallInput{RawArgs: string(raw)})
				if err != nil || r.IsError {
					t.Fatalf("%s 调用失败：%+v %v", name, r, err)
				}
				data, err := json.Marshal(r.Data)
				if err != nil {
					t.Fatal(err)
				}
				if err = json.Unmarshal(data, target); err != nil {
					t.Fatal(err)
				}
				return
			}
		}
		t.Fatalf("没有工具 %s", name)
	}
	var doc domain.SourceDocument
	invoke("read_paper", map[string]any{"paper_id": paper.ID, "section": "fulltext"}, &doc)
	if doc.Text != "" || len(doc.Blocks) == 0 {
		t.Fatal("全文工具应返回目录，不向上下文灌入整篇正文")
	}
	var block domain.DocumentBlock
	invoke("read_paper", map[string]any{"paper_id": paper.ID, "section": "block", "document_id": doc.ID, "block_id": "b2"}, &block)
	var ev domain.Evidence
	invoke("save_evidence", map[string]any{"document_id": doc.ID, "block_id": block.ID, "quote": "多变量预测采用统一的训练集。", "field": "dataset", "value": "统一训练集"}, &ev)
	if ev.ID == "" || ev.EndRune <= ev.StartRune {
		t.Fatalf("没有保存准确引用：%+v", ev)
	}
}
