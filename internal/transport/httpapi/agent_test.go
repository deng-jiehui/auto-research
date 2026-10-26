package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/deng-jiehui/auto-research/internal/app"
	"github.com/deng-jiehui/auto-research/internal/domain"
	"github.com/deng-jiehui/auto-research/internal/infra/sqlite"
	"github.com/deng-jiehui/auto-research/internal/transport/httpapi"
)

func TestAgentModeHTTPPinsRequestedModeWithoutChangingDefault(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	project, err := store.CreateProject(ctx, "local-user", "会话模式")
	if err != nil {
		t.Fatal(err)
	}
	scope := domain.Scope{UserID: "local-user", ProjectID: project.ID}
	turns := app.NewTurnService(store, store, store)
	if err = turns.SetAgentMode("multi"); err != nil {
		t.Fatal(err)
	}
	h := httpapi.NewHandlerWithRuntime(app.NewProjectService(store), app.NewSessionService(store), turns, store, "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	settings := call(t, h, http.MethodGet, "/api/runtime/agent", nil, http.StatusOK)
	var defaults struct {
		Mode string `json:"mode"`
	}
	if err = json.Unmarshal(settings.Body.Bytes(), &defaults); err != nil || defaults.Mode != "multi" {
		t.Fatalf("无法读取服务默认模式：%s err=%v", settings.Body.String(), err)
	}
	for _, tc := range []struct {
		mode, want string
		status     int
	}{
		{"single", "single", http.StatusAccepted},
		{"multi", "multi", http.StatusAccepted},
		{"", "multi", http.StatusAccepted},
		{"legacy", "", http.StatusUnprocessableEntity},
		{"unknown", "", http.StatusUnprocessableEntity},
	} {
		session, createErr := store.CreateSession(ctx, scope, "模式选择")
		if createErr != nil {
			t.Fatal(createErr)
		}
		path := "/api/projects/" + project.ID + "/sessions/" + session.ID + "/messages"
		response := call(t, h, http.MethodPost, path, map[string]any{"text": "研究方向", "idempotency_key": "mode-1", "agent_mode": tc.mode}, tc.status)
		if tc.status != http.StatusAccepted {
			continue
		}
		var result struct {
			Turn domain.Turn `json:"turn"`
		}
		if err = json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		var frozen struct {
			AgentMode string `json:"agent_mode"`
		}
		if err = json.Unmarshal(result.Turn.ContextJSON, &frozen); err != nil || frozen.AgentMode != tc.want {
			t.Fatalf("请求模式未固定：want=%s got=%s err=%v", tc.want, frozen.AgentMode, err)
		}
	}
	if turns.AgentMode() != "multi" {
		t.Fatal("会话模式覆盖了其他会话的默认模式")
	}
}
