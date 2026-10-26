package app

import (
	"context"
	"testing"

	"github.com/deng-jiehui/auto-research/internal/domain"
	"github.com/goharness/goharness/pkg/schema"
	"github.com/goharness/goharness/pkg/tool"
)

func TestAcademicToolReportsUnavailableToModel(t *testing.T) {
	node, err := tool.NewToolsNode(tool.ToolNodeConfig{Tools: NewAcademicTools(AcademicToolsDeps{})})
	if err != nil {
		t.Fatal(err)
	}
	result, err := node.Invoke(context.Background(), []schema.Message{schema.NewMessage(schema.RoleAssistant, schema.NewFunctionToolCallBlock("unavailable", "search_papers", `{"text":"transformer"}`))})
	if err != nil {
		t.Fatal(err)
	}
	returned := result[0].FunctionToolResults()[0]
	text := schema.NewMessage(schema.RoleTool, returned.ContentBlocks...).JoinedText("")
	if !returned.IsError || text != domain.ErrUnavailable.Error() {
		t.Fatalf("模型应收到来源错误原因，实际=%s", text)
	}
}

func TestAcademicReadReportsMissingDependencies(t *testing.T) {
	result, err := NewAcademicTools(AcademicToolsDeps{})[3].Invoke(context.Background(), tool.CallInput{RawArgs: `{"paper_id":"foreign"}`})
	if err != nil || !result.IsError {
		t.Fatalf("未配置仓储不应尝试访问来源: %v", err)
	}
}
