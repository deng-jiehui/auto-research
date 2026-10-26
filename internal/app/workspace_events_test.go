package app

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/deng-jiehui/auto-research/internal/domain"
	"github.com/goharness/goharness/pkg/schema"
)

func TestPlanToolEventShowsSummaryInsteadOfInternalJSON(t *testing.T) {
	data, _ := json.Marshal(ResearchResult{Kind: "plan", ID: "run-a", Revision: 2, ResourcePath: "research://runs/run-a.json"})
	events := workspaceToolEvents(schema.NewMessage(schema.RoleTool, schema.NewFunctionToolResultBlock("plan-call", "submit_research_result", false, schema.NewTextBlock(string(data)))))
	if len(events) != 1 || strings.Contains(events[0].Preview, "resource_path") || !strings.Contains(events[0].Preview, "v2") {
		t.Fatalf("计划预览泄漏实现内容: %+v", events)
	}
}

func TestSearchToolEventPreservesCandidatesBeyondPreview(t *testing.T) {
	candidates := []domain.Candidate{
		{SourceKey: "arxiv:2401.00001", Title: "First", Abstract: strings.Repeat("研究摘要", 250)},
		{SourceKey: "arxiv:2401.00002", Title: "Second", Abstract: "第二篇摘要"},
	}
	data, _ := json.Marshal(candidates)
	message := schema.NewMessage(schema.RoleTool, schema.NewFunctionToolResultBlock("search-1", "search_papers", false, schema.NewTextBlock(string(data))))
	events := workspaceToolEvents(message)
	encoded, _ := json.Marshal(events)
	var payload []struct {
		Papers []domain.Candidate `json:"papers"`
	}
	if err := json.Unmarshal(encoded, &payload); err != nil || len(payload) != 1 || len(payload[0].Papers) != 2 {
		t.Fatalf("检索事件没有可归档的完整论文：%s err=%v", encoded, err)
	}
	if payload[0].Papers[0].Abstract != candidates[0].Abstract || payload[0].Papers[1].SourceKey != "arxiv:2401.00002" {
		t.Fatal("快捷归档的元数据被摘要预览截断")
	}
}
