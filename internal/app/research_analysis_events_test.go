package app

import (
	"github.com/goharness/goharness/pkg/schema"
	"strings"
	"testing"
)

func TestResearchAnalysisProgressDoesNotExposeRawJSON(t *testing.T) {
	message := schema.NewMessage(schema.RoleTool, schema.NewFunctionToolResultBlock("save", "submit_research_result", false, schema.NewTextBlock(`{"kind":"paper_analysis","work_item":{"title":"预测基线","state":"completed","analysis":{"summary":"核对方法，指标未知"}}}`)))
	events := workspaceToolEvents(message)
	if len(events) != 1 || !strings.Contains(events[0].Preview, "预测基线") || strings.Contains(events[0].Preview, "work_item") {
		t.Fatalf("逐篇摘要仍是原始 JSON: %+v", events)
	}
}
