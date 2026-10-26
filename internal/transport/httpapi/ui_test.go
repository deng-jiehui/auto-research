package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/deng-jiehui/auto-research/internal/app"
	"github.com/deng-jiehui/auto-research/internal/domain"
	"github.com/deng-jiehui/auto-research/internal/infra/mcp"
	"github.com/deng-jiehui/auto-research/internal/infra/sqlite"
	"github.com/deng-jiehui/auto-research/internal/ui"
)

func TestUIHTTPUsesStructuredSnapshotsAndResearchAnswers(t *testing.T) {
	ctx := t.Context()
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	project, _ := store.CreateProject(ctx, "local-user", "共用 UI")
	scope := domain.Scope{UserID: "local-user", ProjectID: project.ID}
	turns := app.NewTurnService(store, store, store)
	turns.SetResearchService(app.NewResearchService(store))
	academic := app.NewAcademicService(mcp.NewFixtureSource())
	papers := app.NewPaperService(store)
	turns.SetAcademicTools(app.AcademicToolsDeps{Academic: academic, Papers: papers})
	created, err := turns.StartResearch(ctx, scope, app.StartResearchInput{ResearchSpec: domain.ResearchSpec{Question: "可重复基线"}, IdempotencyKey: "start-ui"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = turns.RunQueued(ctx); err != nil {
		t.Fatal(err)
	}
	h := NewHandlerWithResearch(app.NewProjectService(store), app.NewSessionService(store), turns, papers, academic, nil, store, "", slog.New(slog.NewTextHandler(io.Discard, nil)), nil, nil)
	request := func(method, path, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
		return w
	}
	base := "/api/projects/" + project.ID + "/research/" + created.Run.ID
	w := request("GET", base+"/snapshot", "")
	var view ui.View
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &view) != nil || view.UI.Version != "1" || view.UI.Target.ID != created.Run.ID || view.UI.Interaction == nil || view.Research == nil {
		t.Fatalf("快照缺少 UI 协议: status=%d ui=%+v", w.Code, view.UI)
	}
	prompt := view.UI.Interaction
	input := app.ResumeInput{Revision: view.Turn.Revision, IdempotencyKey: "ui-approve", Answers: []app.AnswerInput{{InterruptID: prompt.ID, ResearchPlan: &domain.ResearchPlanReview{RunID: prompt.RunID, PlanRevision: prompt.Revision, PlanHash: prompt.Hash, Decision: "approve", Plan: *prompt.Plan}}}}
	body, _ := json.Marshal(input)
	if bad := request("POST", base+"/turns/wrong-turn/resume", string(body)); bad.Code != 409 {
		t.Fatalf("错误轮次未拒绝: %d %s", bad.Code, bad.Body.String())
	}
	other, _ := store.CreateProject(ctx, "local-user", "另一个项目")
	foreign := "/api/projects/" + other.ID + "/research/" + created.Run.ID
	if bad := request("POST", foreign+"/turns/"+view.Turn.ID+"/resume", string(body)); bad.Code != 404 {
		t.Fatalf("跨项目回答未拒绝: %d", bad.Code)
	}
	if response := request("POST", base+"/turns/"+view.Turn.ID+"/resume", string(body)); response.Code != 202 {
		t.Fatalf("无法通过研究身份回答: %d %s", response.Code, response.Body.String())
	}
	if response := request("POST", base+"/turns/"+view.Turn.ID+"/resume", string(body)); response.Code != 202 || !strings.Contains(response.Body.String(), `"replayed":true`) {
		t.Fatalf("回答重放失败: %d %s", response.Code, response.Body.String())
	}
	list := request("GET", base+"/events?after=0", "")
	var events struct {
		Items []domain.SessionEvent `json:"items"`
	}
	if list.Code != 200 || json.Unmarshal(list.Body.Bytes(), &events) != nil || len(events.Items) == 0 {
		t.Fatalf("研究事件不可读: %d %s", list.Code, list.Body.String())
	}
	last := events.Items[len(events.Items)-1].ID
	if response := request("GET", base+"/events?after="+strconv.FormatInt(last, 10), ""); response.Code != 200 || !strings.Contains(response.Body.String(), `"items":[]`) {
		t.Fatalf("游标重复返回事件: %d %s", response.Code, response.Body.String())
	}
	if response := request("GET", foreign+"/events", ""); response.Code != 404 {
		t.Fatalf("跨项目事件未拒绝: %d", response.Code)
	}
	// 终态 SSE 也应先回放已保存事件，随后关闭连接。
	if err = store.SetTurnState(ctx, scope, created.Turn.ID, domain.TurnCompleted, "", ""); err != nil {
		t.Fatal(err)
	}
	sse := httptest.NewRecorder()
	req := httptest.NewRequest("GET", base+"/events?after=0", nil)
	req.Header.Set("Accept", "text/event-stream")
	h.ServeHTTP(sse, req)
	if sse.Code != 200 || !strings.Contains(sse.Body.String(), "id: ") || !strings.Contains(sse.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("SSE 回放失败: %d %s", sse.Code, sse.Body.String())
	}
	// 模拟首次事件回放结束、读取终态之前又保存了最后一条事件。
	tail := &uiFlushRecorder{ResponseRecorder: httptest.NewRecorder(), afterFlush: func() {
		_, err := store.AppendEvent(ctx, scope, domain.SessionEvent{SessionID: created.Run.SessionID, TurnID: created.Turn.ID, Kind: "ui.tail", PayloadJSON: []byte(`{"done":true}`)})
		if err != nil {
			t.Fatal(err)
		}
	}}
	req = httptest.NewRequest("GET", base+"/events?after=0", nil)
	req.Header.Set("Accept", "text/event-stream")
	h.ServeHTTP(tail, req)
	if !strings.Contains(tail.Body.String(), "event: ui.tail") {
		t.Fatal("读取终态时丢失刚保存的末尾事件")
	}
}

type uiFlushRecorder struct {
	*httptest.ResponseRecorder
	afterFlush func()
}

func (w *uiFlushRecorder) Flush() {
	w.ResponseRecorder.Flush()
	if w.afterFlush != nil {
		action := w.afterFlush
		w.afterFlush = nil
		action()
	}
}
