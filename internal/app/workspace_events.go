package app

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/deng-jiehui/auto-research/internal/domain"
	"github.com/goharness/goharness/pkg/schema"
)

// WorkspaceToolEvent 是可公开给前端的工作区工具摘要，不包含完整调用参数。
type WorkspaceToolEvent struct {
	Tool      string             `json:"tool"`
	CallID    string             `json:"call_id"`
	Status    string             `json:"status"`
	IsError   bool               `json:"is_error"`
	Preview   string             `json:"preview,omitempty"`
	Papers    []domain.Candidate `json:"papers,omitempty"`
	Direction *domain.Direction  `json:"direction,omitempty"`
}

func workspaceToolEvents(message schema.Message) []WorkspaceToolEvent {
	results := message.FunctionToolResults()
	if len(results) == 0 {
		return nil
	}
	events := make([]WorkspaceToolEvent, 0, len(results))
	for _, result := range results {
		text := schema.NewMessage(schema.RoleTool, result.ContentBlocks...).JoinedText("")
		preview := truncateRunes(strings.TrimSpace(text), 500)
		status := "completed"
		if result.IsError {
			status = "failed"
		}
		event := WorkspaceToolEvent{
			Tool: result.Name, CallID: result.CallID, Status: status,
			IsError: result.IsError, Preview: preview,
		}
		if result.Name == "start_research" && !result.IsError {
			event.Preview = "已委派独立调研任务，可从任务卡片查看进度和成果。"
		}
		// 快捷归档使用工具的完整结构化结果，不能从截断预览或模型正文猜测论文。
		if result.Name == "submit_research_result" && !result.IsError {
			var saved ResearchResult
			var analysis struct {
				Kind     string                  `json:"kind"`
				WorkItem domain.ResearchWorkItem `json:"work_item"`
			}
			if json.Unmarshal([]byte(text), &analysis) == nil && analysis.Kind == "paper_analysis" {
				event.Preview = "已保存逐篇分析：" + analysis.WorkItem.Title + "。可展开论文分析查看字段和原文引用。"
				if analysis.WorkItem.State == "failed" {
					event.Preview = "已记录论文分析失败：" + analysis.WorkItem.Title + "。可在任务页查看原因并重试。"
				}
			}
			if json.Unmarshal([]byte(text), &saved) == nil && saved.Kind == "report" {
				event.Preview = "研究报告已保存，可在任务页查看证据矩阵、原文引用或导出 Markdown。"
			}
			if json.Unmarshal([]byte(text), &saved) == nil && saved.Kind == "plan" {
				event.Preview = fmt.Sprintf("已保存研究计划 v%d，等待用户检查与确认。", saved.Revision)
			}
			if json.Unmarshal([]byte(text), &saved) == nil && saved.Kind == "direction" && saved.Direction != nil && saved.ID == saved.Direction.ID {
				event.Direction = saved.Direction
				event.Preview = "已保存待确认方向：" + saved.Direction.Name + "。请查看方向卡片后编辑或确认。"
			}
		}
		if result.Name == "read_file" && !result.IsError {
			var saved ReadFileResult
			if json.Unmarshal([]byte(text), &saved) == nil && strings.HasPrefix(saved.Path, "research://") {
				event.Preview = fmt.Sprintf("已读取项目资料 %s，第 %d–%d 行。", saved.Path, saved.StartLine, saved.EndLine)
			}
		}
		if result.Name == "search_papers" && !result.IsError {
			var candidates []domain.Candidate
			if json.Unmarshal([]byte(text), &candidates) == nil {
				for _, candidate := range candidates {
					key, err := domain.NormalizePaperKey(candidate.SourceKey)
					if err == nil && strings.TrimSpace(candidate.Title) != "" {
						candidate.SourceKey = key
						event.Papers = append(event.Papers, candidate)
					}
				}
			}
		}
		events = append(events, event)
	}
	return events
}
