package httpapi

import (
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

func TestDetachedResearchStartsWithoutCopyingOrBlockingChat(t *testing.T) {
	ctx := t.Context()
	s, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p, _ := s.CreateProject(ctx, "local-user", "独立研究")
	scope := domain.Scope{UserID: "local-user", ProjectID: p.ID}
	session, _ := s.CreateSession(ctx, scope, "普通会话")
	turns := app.NewTurnService(s, s, s)
	turns.SetResearchService(app.NewResearchService(s))
	_, _ = s.SaveNote(ctx, scope, domain.Note{Text: "不应自动传给研究的项目笔记", ToolKey: "private-note"})
	chat, err := turns.Send(ctx, scope, session.ID, app.SendInput{Text: "不应自动传给研究的聊天历史", IdempotencyKey: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandlerWithResearch(app.NewProjectService(s), app.NewSessionService(s), turns, nil, nil, nil, s, "", slog.New(slog.NewTextHandler(io.Discard, nil)), nil, nil)
	request := func(method, path, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
		return w
	}
	path := "/api/projects/" + p.ID + "/research"
	body, _ := json.Marshal(map[string]any{"question": "比较预测基线", "constraints": []string{"单卡"}, "origin_session_id": session.ID, "origin_turn_id": chat.Turn.ID, "idempotency_key": "detached-start"})
	first := request(http.MethodPost, path, string(body))
	if first.Code != http.StatusAccepted {
		t.Fatalf("独立任务创建=%d %s", first.Code, first.Body.String())
	}
	var created struct {
		Run  domain.ResearchRun `json:"run"`
		Turn domain.Turn        `json:"turn"`
	}
	if err = json.Unmarshal(first.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Run.SessionID == session.ID || created.Run.TurnID == chat.Turn.ID {
		t.Fatal("研究仍占用来源会话")
	}
	contextText := string(created.Turn.ContextJSON)
	if strings.Contains(contextText, "不应自动传给研究") || !strings.Contains(contextText, "比较预测基线") {
		t.Fatalf("研究上下文未隔离: %s", contextText)
	}
	listed, err := s.ListSessions(ctx, scope)
	if err != nil || len(listed) != 1 {
		t.Fatalf("内部研究会话混入列表: %+v %v", listed, err)
	}
	replayed := request(http.MethodPost, path, string(body))
	if replayed.Code != 202 || !strings.Contains(replayed.Body.String(), `"replayed":true`) || !strings.Contains(replayed.Body.String(), created.Run.ID) {
		t.Fatalf("创建重放不幂等: %d %s", replayed.Code, replayed.Body.String())
	}
	conflict := request(http.MethodPost, path, strings.Replace(string(body), "比较预测基线", "换一个问题", 1))
	if conflict.Code != 409 {
		t.Fatalf("同身份换内容未拒绝: %d", conflict.Code)
	}
	for _, suffix := range []string{"/matrix", "/reports"} {
		response := request(http.MethodGet, path+"/"+created.Run.ID+suffix, "")
		if response.Code != 200 {
			t.Fatalf("只读成果接口 %s=%d %s", suffix, response.Code, response.Body.String())
		}
		other, _ := s.CreateProject(ctx, "local-user", "另一个项目")
		foreign := request(http.MethodGet, "/api/projects/"+other.ID+"/research/"+created.Run.ID+suffix, "")
		if foreign.Code != 404 {
			t.Fatalf("跨项目成果接口 %s=%d", suffix, foreign.Code)
		}
	}
	missing := request(http.MethodGet, "/api/projects/"+p.ID+"/reports/missing", "")
	if missing.Code != 404 {
		t.Fatalf("不存在的报告=%d", missing.Code)
	}
	snapshot := request(http.MethodGet, path+"/"+created.Run.ID+"/snapshot", "")
	if snapshot.Code != 200 {
		t.Fatalf("任务快照=%d %s", snapshot.Code, snapshot.Body.String())
	}
	cards := request(http.MethodGet, "/api/projects/"+p.ID+"/sessions/"+session.ID+"/research", "")
	if cards.Code != 200 || !strings.Contains(cards.Body.String(), created.Run.ID) || strings.Contains(cards.Body.String(), "context_json") {
		t.Fatalf("会话未收到简要入口: %d %s", cards.Code, cards.Body.String())
	}
	_ = s.SetTurnState(ctx, scope, chat.Turn.ID, domain.TurnCompleted, "", "")
	if err = s.DeleteSession(ctx, scope, session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetResearch(ctx, scope, created.Run.ID); err != nil {
		t.Fatalf("删除来源会话丢失研究: %v", err)
	}
}
