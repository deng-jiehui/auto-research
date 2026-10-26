package app

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/deng-jiehui/auto-research/internal/domain"
	"github.com/goharness/goharness/pkg/llm"
	"github.com/goharness/goharness/pkg/schema"
)

// 离线模式同样逐篇调用真实工具，只生成明确标注的 fixture 分析。
func analysisFixtureResponse(request llm.Request, deps ResearchGraphDeps, run domain.ResearchRun) ([]schema.Message, error) {
	call := func(name string, args any) ([]schema.Message, error) {
		raw, err := json.Marshal(args)
		id := fmt.Sprintf("fixture-%s-%d-%d", name, run.ModelCalls, run.ToolCalls)
		return []schema.Message{schema.NewMessage(schema.RoleAssistant, schema.NewFunctionToolCallBlock(id, name, string(raw)))}, err
	}
	completed, failed := 0, 0
	var current *domain.ResearchWorkItem
	for i := range run.WorkItems {
		w := &run.WorkItems[i]
		switch w.State {
		case "completed":
			completed++
		case "failed":
			failed++
		default:
			if current == nil || w.State == "reading" {
				current = w
			}
		}
	}
	if current == nil {
		if supportsResearchReport(deps) {
			return reportFixtureResponse(run)
		}
		return []schema.Message{schema.AssistantMessage(fmt.Sprintf("逐篇分析已保存：完成 %d 篇，失败 %d 篇。可在调研任务页展开结果、查看原文引用或重试失败项。\n\n这是离线演示数据；矩阵和正式报告尚未生成。", completed, failed))}, nil
	}
	w := *current
	last := request.Messages[len(request.Messages)-1]
	for _, result := range last.FunctionToolResults() {
		text := schema.NewMessage(schema.RoleTool, result.ContentBlocks...).JoinedText("")
		if result.Name == "read_paper" && !result.IsError {
			var block domain.DocumentBlock
			var doc domain.SourceDocument
			_ = json.Unmarshal([]byte(text), &block)
			_ = json.Unmarshal([]byte(text), &doc)
			if doc.ID == w.DocumentID && doc.Text != "" {
				block = domain.DocumentBlock{Text: doc.Text, ID: "b1"}
			}
			if block.Text != "" {
				quote := truncateRunes(block.Text, 180)
				return call("save_evidence", SaveEvidenceArgs{DocumentID: w.DocumentID, BlockID: block.ID, Quote: quote, Field: "method", Value: "离线资料描述的研究方法，需人工核对语义"})
			}
		}
		if result.Name == "save_evidence" && !result.IsError {
			var evidence EvidenceToolResult
			if err := json.Unmarshal([]byte(text), &evidence); err != nil {
				return nil, err
			}
			fields := []domain.AnalysisField{{Name: "method", Status: "finding", Text: "离线资料说明研究比较或工作流程，依据见引用", EvidenceIDs: []string{evidence.ID}}}
			for _, name := range []string{"datasets", "metrics", "code", "reproduction"} {
				fields = append(fields, domain.AnalysisField{Name: name, Status: "unknown", Text: "本次阅读片段未核实，不能据此推断"})
			}
			return call("submit_research_result", map[string]any{"kind": "paper_analysis", "expected_revision": w.Revision, "paper_analysis": domain.PaperAnalysisDraft{PaperID: w.PaperID, DocumentID: w.DocumentID, Status: "completed", Summary: "离线逐篇演示：保存有依据的信息，实验细节保持未知。阅读级别：" + w.ContentKind, Fields: fields}})
		}
		if result.Name == "read_paper" && result.IsError && w.DocumentID == "" {
			// 全文失败后允许明确降为摘要，仍经过阅读与证据工具。
			var readArgs ReadPaperDocumentArgs
			for i := len(request.Messages) - 2; i >= 0; i-- {
				for _, c := range request.Messages[i].FunctionToolCalls() {
					if c.ID == result.CallID {
						_ = json.Unmarshal([]byte(c.Arguments), &readArgs)
					}
				}
				if readArgs.PaperID != "" {
					break
				}
			}
			if readArgs.Section == "fulltext" {
				return call("read_paper", ReadPaperDocumentArgs{PaperID: w.PaperID, Section: "abstract"})
			}
			return call("submit_research_result", map[string]any{"kind": "paper_analysis", "expected_revision": w.Revision, "paper_analysis": domain.PaperAnalysisDraft{PaperID: w.PaperID, Status: "failed", Error: truncateRunes(text, 800)}})
		}
	}
	if w.DocumentID == "" {
		return call("read_paper", ReadPaperDocumentArgs{PaperID: w.PaperID, Section: "fulltext"})
	}
	if deps.Academic.Documents == nil {
		return nil, domain.ErrUnavailable
	}
	doc, err := deps.Academic.Documents.repo.GetDocument(context.Background(), deps.Scope, w.DocumentID)
	if err != nil {
		return nil, err
	}
	blocks := domain.DocumentBlocks(doc)
	if len(blocks) == 0 {
		return nil, domain.ErrInvalid
	}
	index := 0
	if len(blocks) > 1 {
		index = 1
	}
	return call("read_paper", ReadPaperDocumentArgs{PaperID: w.PaperID, Section: "block", DocumentID: doc.ID, BlockID: blocks[index].ID})
}
