package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/deng-jiehui/auto-research/internal/app"
	"github.com/deng-jiehui/auto-research/internal/domain"
	"github.com/deng-jiehui/auto-research/internal/infra/sqlite"
)

func TestResearchHTTPStartsAndReplaysWithoutDuplicateTurn(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	p, _ := store.CreateProject(ctx, "local-user", "HTTP 调研")
	scope := domain.Scope{UserID: "local-user", ProjectID: p.ID}
	session, _ := store.CreateSession(ctx, scope, "调研")
	turns := app.NewTurnService(store, store, store)
	turns.SetResearchService(app.NewResearchService(store))
	h := NewHandlerWithResearch(app.NewProjectService(store), app.NewSessionService(store), turns, nil, nil, nil, store, "", slog.New(slog.NewTextHandler(io.Discard, nil)), nil, nil)
	body := `{"text":"研究预测基线","idempotency_key":"research-http","research":{"question":"研究预测基线"}}`
	request := func(method, path, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
		return w
	}
	path := "/api/projects/" + p.ID + "/sessions/" + session.ID + "/messages"
	first := request(http.MethodPost, path, body)
	if first.Code != 202 {
		t.Fatalf("创建=%d %s", first.Code, first.Body.String())
	}
	list := request(http.MethodGet, "/api/projects/"+p.ID+"/research", "")
	if list.Code != 200 {
		t.Fatalf("列表=%d %s", list.Code, list.Body.String())
	}
	var items struct {
		Items []domain.ResearchRun `json:"items"`
	}
	_ = json.Unmarshal(list.Body.Bytes(), &items)
	if len(items.Items) != 1 {
		t.Fatalf("未登记任务: %s", list.Body.String())
	}
	second := request(http.MethodPost, path, body)
	if second.Code != 202 || !strings.Contains(second.Body.String(), `"replayed":true`) {
		t.Fatalf("重发=%d %s", second.Code, second.Body.String())
	}
	if len(items.Items[0].SpecHash) != 64 || items.Items[0].ProjectSnapshot.Title != "HTTP 调研" {
		t.Fatalf("快照不完整: %+v", items.Items[0])
	}
}

func TestResearchHTTPDeletesFinishedTaskAndRejectsActiveTask(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	project, _ := store.CreateProject(ctx, "local-user", "删除调研")
	scope := domain.Scope{UserID: "local-user", ProjectID: project.ID}
	turns := app.NewTurnService(store, store, store)
	turns.SetResearchService(app.NewResearchService(store))
	h := NewHandlerWithResearch(app.NewProjectService(store), app.NewSessionService(store), turns, nil, nil, nil, store, "", slog.New(slog.NewTextHandler(io.Discard, nil)), nil, nil)
	request := func(method, path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(method, path, nil))
		return w
	}
	source, _ := store.CreateSession(ctx, scope, "来源")
	draft := domain.ResearchTaskDraft{Spec: domain.ResearchSpec{Question: "可删除调研"}, OriginSessionID: source.ID, OperationID: "delete-http", ContextJSON: []byte(`{}`), MessageJSON: []byte(`{"role":"user","content_blocks":[]}`), DefinitionVersion: "autoresearch-agent-single-v8"}
	created, err := store.CreateResearchTask(ctx, scope, draft)
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/projects/" + project.ID + "/research/" + created.Run.ID
	active := request(http.MethodDelete, path)
	if active.Code != http.StatusConflict || !strings.Contains(active.Body.String(), "先结束任务") {
		t.Fatalf("活动任务删除=%d %s", active.Code, active.Body.String())
	}
	other, _ := store.CreateProject(ctx, "local-user", "其他项目")
	foreign := request(http.MethodDelete, "/api/projects/"+other.ID+"/research/"+created.Run.ID)
	if foreign.Code != http.StatusNotFound {
		t.Fatalf("跨项目删除=%d %s", foreign.Code, foreign.Body.String())
	}
	if err = store.SetTurnState(ctx, scope, created.Turn.ID, domain.TurnCompleted, "", ""); err != nil {
		t.Fatal(err)
	}
	finished := request(http.MethodDelete, path)
	if finished.Code != http.StatusNoContent {
		t.Fatalf("已结束任务删除=%d %s", finished.Code, finished.Body.String())
	}
	missing := request(http.MethodGet, path)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("删除后读取=%d %s", missing.Code, missing.Body.String())
	}
	missingDelete := request(http.MethodDelete, path)
	if missingDelete.Code != http.StatusNotFound {
		t.Fatalf("重复删除=%d %s", missingDelete.Code, missingDelete.Body.String())
	}
}
