package httpapi_test

import (
	"context"
	"testing"

	"github.com/deng-jiehui/auto-research/internal/app"
	"github.com/deng-jiehui/auto-research/internal/domain"
	"github.com/deng-jiehui/auto-research/internal/infra/sqlite"
	"github.com/deng-jiehui/auto-research/internal/transport/httpapi"
)

func TestDirectionHTTPProposeEditAndConfirmRevision(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	project, err := store.CreateProject(ctx, "local-user", "方向接口")
	if err != nil {
		t.Fatal(err)
	}
	h := httpapi.NewHandlerWithResearch(app.NewProjectService(store), app.NewSessionService(store), nil, app.NewPaperService(store), nil, app.NewEvidenceService(store), store, "", testLogger(), nil, app.NewDirectionService(store, store))
	base := "/api/projects/" + project.ID + "/directions"
	response := postJSON(t, h, base, map[string]any{"name": "线性基线", "description": "确认统一划分", "idempotency_key": "http-propose"})
	if response.StatusCode != 201 {
		t.Fatalf("提案接口: %d", response.StatusCode)
	}
	var first domain.Direction
	decodeJSON(t, response, &first)
	response = patchJSON(t, h, base+"/"+first.ID, map[string]any{"name": "线性与 Transformer", "description": "修改后的提案", "revision": first.Revision})
	if response.StatusCode != 200 {
		t.Fatalf("编辑接口: %d", response.StatusCode)
	}
	var edited domain.Direction
	decodeJSON(t, response, &edited)
	if got := postJSON(t, h, base+"/"+first.ID+"/accept", map[string]any{"revision": first.Revision}); got.StatusCode != 409 {
		t.Fatalf("旧版本确认: %d", got.StatusCode)
	}
	response = postJSON(t, h, base+"/"+first.ID+"/accept", map[string]any{"revision": edited.Revision})
	var accepted domain.Direction
	decodeJSON(t, response, &accepted)
	if accepted.State != "accepted" {
		t.Fatalf("未确认: %+v", accepted)
	}
	if got := postJSON(t, h, base, map[string]any{"name": "伪造", "description": "不允许", "state": "accepted", "idempotency_key": "bad"}); got.StatusCode != 422 {
		t.Fatalf("可伪造状态: %d", got.StatusCode)
	}
}
