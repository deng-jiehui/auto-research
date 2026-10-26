package sqlite

import (
	"context"
	"strings"
	"testing"

	"github.com/deng-jiehui/auto-research/internal/domain"
)

func TestLongDocumentIsExternalizedAndStillValidatesEvidence(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	project, err := store.CreateProject(ctx, "alice", "全文存储")
	if err != nil {
		t.Fatal(err)
	}
	scope := domain.Scope{UserID: "alice", ProjectID: project.ID}
	paper, err := store.ImportPaper(ctx, scope, domain.Paper{SourceKey: "arxiv:2401.00001", Title: "测试论文"})
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Repeat("多变量预测保留原始测量。\n", 6000)
	doc, err := store.SaveDocument(ctx, scope, domain.SourceDocument{PaperID: paper.ID, Text: text, SHA256: domain.SourceTextSHA256(text), Locator: "arxiv:2401.00001v2:fulltext"})
	if err != nil {
		t.Fatal(err)
	}
	if doc.ArtifactID == "" {
		t.Fatal("长全文没有外置 Artifact，仍会随文档列表传给模型")
	}
	items, err := store.ListDocuments(ctx, scope, paper.ID)
	if err != nil || len(items) != 1 || items[0].Text != "" {
		t.Fatalf("列表应只返回长文档元数据：%d err=%v", len(items), err)
	}
	store.Close()
	store, err = Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	loaded, err := store.GetDocument(ctx, scope, doc.ID)
	if err != nil || loaded.Text != text {
		t.Fatalf("重启后读取全文失败：%v", err)
	}
	ev := domain.Evidence{PaperID: paper.ID, DocumentID: doc.ID, Quote: "多变量预测", StartRune: 0, EndRune: 5}
	if err = domain.ValidateEvidence(loaded, ev); err != nil {
		t.Fatal(err)
	}
	if _, err = store.GetDocument(ctx, domain.Scope{UserID: "bob", ProjectID: project.ID}, doc.ID); err == nil {
		t.Fatal("跨用户读取不应成功")
	}
}

func TestOpenBackfillsLegacyDocumentsAndRecoversInterruptedFetch(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	project, err := store.CreateProject(ctx, "alice", "旧文档")
	if err != nil {
		t.Fatal(err)
	}
	scope := domain.Scope{UserID: "alice", ProjectID: project.ID}
	paper, err := store.ImportPaper(ctx, scope, domain.Paper{SourceKey: "arxiv:2401.00001", Title: "历史资料"})
	if err != nil {
		t.Fatal(err)
	}
	text := "历史资料中的多变量预测基线。"
	doc, err := store.SaveDocument(ctx, scope, domain.SourceDocument{PaperID: paper.ID, Text: text, SHA256: domain.SourceTextSHA256(text), Locator: "abstract"})
	if err != nil {
		t.Fatal(err)
	}
	// 模拟升级前已保存正文和证据、尚未建立块索引的数据库。
	if _, err = store.db.ExecContext(ctx, "DELETE FROM document_blocks WHERE document_id=?", doc.ID); err != nil {
		t.Fatal(err)
	}
	if err = store.SaveDocumentFetch(ctx, scope, domain.DocumentFetch{PaperID: paper.ID, State: "running"}); err != nil {
		t.Fatal(err)
	}
	store.Close()
	store, err = Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	hits, err := store.SearchDocumentBlocks(ctx, scope, "多变量预测", "", 5)
	if err != nil || len(hits) != 1 || hits[0].DocumentID != doc.ID {
		t.Fatalf("旧正文未进入索引: %+v %v", hits, err)
	}
	fetch, err := store.GetDocumentFetch(ctx, scope, paper.ID)
	if err != nil || fetch.State != "failed" || !strings.Contains(fetch.Error, "中断") {
		t.Fatalf("获取状态没有恢复: %+v %v", fetch, err)
	}
	loaded, err := store.GetDocument(ctx, scope, doc.ID)
	if err != nil || loaded.Text != text {
		t.Fatalf("升级改变了冻结正文: %+v %v", loaded, err)
	}
}
