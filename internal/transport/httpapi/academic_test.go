package httpapi_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/deng-jiehui/auto-research/internal/app"
	"github.com/deng-jiehui/auto-research/internal/infra/mcp"
	"github.com/deng-jiehui/auto-research/internal/infra/sqlite"
	"github.com/deng-jiehui/auto-research/internal/transport/httpapi"
)

func TestAcademicHTTPChecksProjectAndSearchValidation(t *testing.T) {
	store, err := sqlite.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	h := httpapi.NewHandlerWithAcademicAndEvidence(app.NewProjectService(store), app.NewSessionService(store), nil, app.NewPaperService(store), app.NewAcademicService(mcp.NewFixtureSource()), app.NewEvidenceService(store), store, "", testLogger())
	if response := postJSON(t, h, "/api/projects/unknown/academic/search", map[string]any{"text": "fixture"}); response.StatusCode != http.StatusNotFound {
		t.Fatalf("未知项目可访问学术来源: %d", response.StatusCode)
	}
	project, err := store.CreateProject(context.Background(), "local-user", "来源测试")
	if err != nil {
		t.Fatal(err)
	}
	base := "/api/projects/" + project.ID + "/academic"
	status := get(t, h, base+"/status")
	var settings struct {
		Mode       string `json:"mode"`
		Configured bool   `json:"configured"`
		ReadScope  string `json:"read_scope"`
	}
	decodeJSON(t, status, &settings)
	if settings.Mode != "fixture" || !settings.Configured || settings.ReadScope != "abstract,fulltext" {
		t.Fatalf("来源状态错误: %+v", settings)
	}
	if response := postJSON(t, h, base+"/search", map[string]any{"text": "fixture", "limit": 31}); response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("未限制搜索数量: %d", response.StatusCode)
	}
	response := postJSON(t, h, base+"/search", map[string]any{"text": "does-not-exist"})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("无匹配应返回正常空列表: %d", response.StatusCode)
	}
	var items struct {
		Items []any `json:"items"`
	}
	decodeJSON(t, response, &items)
	if len(items.Items) != 0 {
		t.Fatal("空搜索返回了论文")
	}
}
