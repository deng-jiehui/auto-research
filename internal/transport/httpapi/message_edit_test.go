package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/deng-jiehui/auto-research/internal/app"
	"github.com/deng-jiehui/auto-research/internal/domain"
	"github.com/deng-jiehui/auto-research/internal/infra/sqlite"
	"github.com/deng-jiehui/auto-research/internal/transport/httpapi"
	"github.com/goharness/goharness/pkg/llm"
	"github.com/goharness/goharness/pkg/schema"
	"github.com/goharness/goharness/pkg/state"
)

func TestMessageRestartReplacesDownstreamConversation(t *testing.T) {
	for _, action := range []string{"retry", "edit"} {
		t.Run(action, func(t *testing.T) {
			ctx := context.Background()
			store, err := sqlite.Open(ctx, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			project, err := store.CreateProject(ctx, "local-user", "消息编辑")
			if err != nil {
				t.Fatal(err)
			}
			scope := domain.Scope{UserID: "local-user", ProjectID: project.ID}
			session, err := store.CreateSession(ctx, scope, "研究")
			if err != nil {
				t.Fatal(err)
			}
			var calls int
			var lastRequest llm.Request
			model := llm.NewHandlerModel(func(_ context.Context, req llm.Request, _ llm.Options) ([]schema.Message, error) {
				calls++
				lastRequest = req
				return []schema.Message{schema.AssistantMessage(fmt.Sprintf("回答-%d", calls))}, nil
			})
			turns := app.NewTurnService(store, store, store, model)
			turns.SetCheckpointFactory(func(s domain.Scope, sessionID, turnID string) state.Store {
				return sqlite.NewCheckpointStore(store, s, sessionID, turnID)
			})
			h := httpapi.NewHandlerWithRuntime(app.NewProjectService(store), app.NewSessionService(store), turns, store, "", slog.New(slog.NewTextHandler(io.Discard, nil)))
			path := "/api/projects/" + project.ID + "/sessions/" + session.ID
			var target domain.Turn
			for index, text := range []string{"保留前文", "原始问题", "应移除的后续问题"} {
				sent, sendErr := turns.Send(ctx, scope, session.ID, app.SendInput{Text: text, IdempotencyKey: fmt.Sprintf("send-%d", index)})
				if sendErr != nil {
					t.Fatal(sendErr)
				}
				if _, err = turns.RunQueued(ctx); err != nil {
					t.Fatal(err)
				}
				if index == 1 {
					target = sent.Turn
				}
			}
			target, err = turns.Get(ctx, scope, session.ID, target.ID)
			if err != nil {
				t.Fatal(err)
			}
			_, err = store.AppendEvent(ctx, scope, domain.SessionEvent{SessionID: session.ID, TurnID: target.ID, Attempt: target.Attempt, Kind: "academic.tool", PayloadJSON: []byte(`{"tool":"search_papers","call_id":"旧检索结果"}`)})
			if err != nil {
				t.Fatal(err)
			}
			payload := map[string]any{"revision": target.Revision, "idempotency_key": "restart-1"}
			wantQuestion := "原始问题"
			if action == "edit" {
				payload["text"] = "修改后的问题"
				wantQuestion = "修改后的问题"
			}
			result := call(t, h, http.MethodPost, path+"/turns/"+target.ID+"/"+action, payload, http.StatusAccepted)
			var restarted app.TurnActionResult
			if err = json.Unmarshal(result.Body.Bytes(), &restarted); err != nil {
				t.Fatal(err)
			}
			if restarted.Turn.State != domain.TurnQueued || restarted.ActionID == "" {
				t.Fatalf("没有重新排队执行: %+v", restarted)
			}
			snapshot, err := store.Snapshot(ctx, scope, session.ID, 100)
			if err != nil {
				t.Fatal(err)
			}
			if snapshot.Turn.ID != target.ID || len(snapshot.Messages) != 3 || !strings.Contains(string(snapshot.Messages[2].ContentJSON), wantQuestion) {
				t.Fatalf("编辑后快照未截断并保留原消息位置: %+v", snapshot)
			}
			for _, event := range snapshot.Events {
				if event.TurnID == target.ID && (event.Kind == "assistant.delta" || event.Kind == "academic.tool" || event.Kind == "turn.completed") {
					t.Fatalf("旧执行结果进入新快照: %+v", event)
				}
			}
			if _, err = turns.RunQueued(ctx); err != nil {
				t.Fatal(err)
			}
			if calls != 4 {
				t.Fatalf("未真实重新调用模型: calls=%d", calls)
			}
			for _, message := range lastRequest.Messages {
				if strings.Contains(message.JoinedText(""), "回答-2") || strings.Contains(message.JoinedText(""), "应移除的后续问题") {
					t.Fatalf("已替换的会话进入模型上下文: %+v", lastRequest.Messages)
				}
			}
			snapshot, err = store.Snapshot(ctx, scope, session.ID, 100)
			if err != nil || len(snapshot.Messages) != 4 || !strings.Contains(string(snapshot.Messages[3].ContentJSON), "回答-4") {
				t.Fatalf("重新执行结果=%+v err=%v", snapshot, err)
			}
			// 同一网络请求重放不得再截断或触发第二次执行。
			call(t, h, http.MethodPost, path+"/turns/"+target.ID+"/"+action, payload, http.StatusAccepted)
			if ran, runErr := turns.RunQueued(ctx); runErr != nil || ran {
				t.Fatalf("重放重复执行: ran=%v err=%v", ran, runErr)
			}
			editPath := path + "/turns/" + target.ID + "/edit"
			call(t, h, http.MethodPost, editPath, map[string]any{"text": " ", "revision": snapshot.Turn.Revision, "idempotency_key": "empty-edit"}, http.StatusUnprocessableEntity)
			call(t, h, http.MethodPost, editPath, map[string]any{"text": "越长的消息", "revision": target.Revision, "idempotency_key": "stale-edit"}, http.StatusConflict)
			call(t, h, http.MethodPost, path+"/turns/missing/edit", map[string]any{"text": "越界消息", "revision": 1, "idempotency_key": "missing-edit"}, http.StatusNotFound)
			sent, err := turns.Send(ctx, scope, session.ID, app.SendInput{Text: "继续研究", IdempotencyKey: "continue"})
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(sent.Turn.ContextJSON), "应移除的后续问题") || strings.Contains(string(sent.Turn.ContextJSON), "回答-2") {
				t.Fatal("新消息重新带入已移除的历史")
			}
			call(t, h, http.MethodPost, editPath, map[string]any{"text": "不能打断执行", "revision": snapshot.Turn.Revision, "idempotency_key": "busy-edit"}, http.StatusConflict)
			afterBusy, err := turns.Get(ctx, scope, session.ID, sent.Turn.ID)
			if err != nil || afterBusy.State != domain.TurnQueued {
				t.Fatalf("失败的编辑影响了执行中任务: %+v err=%v", afterBusy, err)
			}
		})
	}
}
