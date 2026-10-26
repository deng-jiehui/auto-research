package app

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/deng-jiehui/auto-research/internal/domain"
	"github.com/goharness/goharness/pkg/llm"
	"github.com/goharness/goharness/pkg/schema"
)

// 离线演示沿用真实 read_file 和报告引用，不模拟论文内容或模型的语义推理。
func reportConversationFixture(request llm.Request) ([]schema.Message, bool, error) {
	var report domain.ReportContext
	for _, message := range request.Messages {
		if raw, ok := strings.CutPrefix(message.JoinedText(""), reportContextPrefix); ok {
			line, _, _ := strings.Cut(raw, "\n")
			if err := json.Unmarshal([]byte(line), &report); err != nil {
				return nil, true, err
			}
		}
	}
	if report.ReportID == "" {
		return nil, false, nil
	}
	last := request.Messages[len(request.Messages)-1]
	if last.Role != schema.RoleTool && (strings.Contains(last.JoinedText(""), "开始调研") || strings.Contains(last.JoinedText(""), "开展自动调研")) {
		return nil, false, nil
	}
	path := report.ResourcePath
	if last.Role == schema.RoleTool {
		for _, result := range last.FunctionToolResults() {
			if result.Name != "read_file" {
				continue
			}
			if result.IsError {
				return []schema.Message{schema.AssistantMessage("离线演示：报告资料读取失败，请查看工具结果；当前不能依据未读资料作答。")}, true, nil
			}
			var read ReadFileResult
			if err := json.Unmarshal([]byte(schema.NewMessage(schema.RoleTool, result.ContentBlocks...).JoinedText("")), &read); err != nil {
				return nil, true, err
			}
			if read.Path == report.ResourcePath {
				path = report.ReferencesPath
			} else if read.Path == report.ReferencesPath {
				var page struct {
					Items []reportEvidenceView `json:"items"`
				}
				if err := json.Unmarshal([]byte(read.Content), &page); err != nil {
					return nil, true, err
				}
				text := fmt.Sprintf("离线演示 · 报告 v%d\n\n%s\n\n", report.Revision, report.Summary)
				if len(page.Items) > 0 {
					e := page.Items[0]
					text += "已读取的报告证据：\n\n> " + e.Quote + "\n\n[查看原文](" + e.CitationURL + ")\n\n"
				} else {
					text += "报告未保存可引用原文，当前依据不足。\n\n"
				}
				text += "这是资料读取与引用演示；具体问题的分析和进一步推断需要配置真实模型，实验效果仍需验证。"
				return []schema.Message{schema.AssistantMessage(text)}, true, nil
			}
		}
	}
	raw, err := json.Marshal(ReadFileArgs{Path: path})
	return []schema.Message{schema.NewMessage(schema.RoleAssistant, schema.NewFunctionToolCallBlock("fixture-report-context-"+path, "read_file", string(raw)))}, true, err
}
