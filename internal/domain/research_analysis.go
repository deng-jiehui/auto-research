package domain

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// 内容版本与执行身份分别保存；EvidenceID 始终指向冻结来源。
type AnalysisField struct {
	Name        string   `json:"name" jsonschema:"method/datasets/metrics/code/reproduction 五个字段各出现一次"`
	Status      string   `json:"status" jsonschema:"finding 有原文依据；inference 推断；unverified 待验证；unknown 未提供"`
	Text        string   `json:"text" jsonschema:"简短归纳或未知原因，最多 1000 字符；摘要不能支持全文实验结论"`
	EvidenceIDs []string `json:"evidence_ids,omitempty" jsonschema:"save_evidence 返回的真实 ID；finding 必填，必须同论文同冻结文档"`
}
type PaperAnalysisDraft struct {
	PaperID    string          `json:"paper_id"`
	DocumentID string          `json:"document_id,omitempty" jsonschema:"成功阅读的冻结文档 ID；完成分析必填，失败可为空"`
	Status     string          `json:"status" jsonschema:"completed 或 failed；failed 必须说明 error，不能伪造完成"`
	Summary    string          `json:"summary,omitempty" jsonschema:"简短结论，最多 800 字符；仅摘要阅读时明确说明"`
	Error      string          `json:"error,omitempty" jsonschema:"无法完成的具体原因，最多 1000 字符"`
	Fields     []AnalysisField `json:"fields,omitempty"`
}
type ResearchWorkItem struct {
	ID               string              `json:"id"`
	RunID            string              `json:"run_id"`
	PaperID          string              `json:"paper_id"`
	Title            string              `json:"title"`
	DocumentID       string              `json:"document_id,omitempty"`
	ContentKind      string              `json:"content_kind,omitempty"`
	SpecHash         string              `json:"spec_hash"`
	IdentityHash     string              `json:"identity_hash"`
	State            string              `json:"state"`
	AttemptID        string              `json:"attempt_id,omitempty"`
	Attempts         int                 `json:"attempts"`
	Revision         int64               `json:"revision"`
	AnalysisRevision int64               `json:"analysis_revision"`
	AnalysisHash     string              `json:"analysis_hash,omitempty"`
	Analysis         *PaperAnalysisDraft `json:"analysis,omitempty"`
}

func ValidatePaperAnalysis(d PaperAnalysisDraft) error {
	if d.PaperID == "" || utf8.RuneCountInString(d.Summary) > 800 || utf8.RuneCountInString(d.Error) > 1000 {
		return ErrInvalid
	}
	if d.Status == "failed" {
		if strings.TrimSpace(d.Error) == "" || len(d.Fields) != 0 {
			return fmt.Errorf("%w：失败项须有原因且不能夹带结论", ErrInvalid)
		}
		return nil
	}
	if d.Status != "completed" || d.DocumentID == "" || strings.TrimSpace(d.Summary) == "" || d.Error != "" || len(d.Fields) != 5 {
		return fmt.Errorf("%w：完成分析须有冻结文档、简短说明和五个分析字段", ErrInvalid)
	}
	seen := map[string]bool{}
	for _, f := range d.Fields {
		if (f.Name != "method" && f.Name != "datasets" && f.Name != "metrics" && f.Name != "code" && f.Name != "reproduction") || seen[f.Name] || strings.TrimSpace(f.Text) == "" || utf8.RuneCountInString(f.Text) > 1000 || len(f.EvidenceIDs) > 8 {
			return fmt.Errorf("%w：分析字段缺失、重复或超长", ErrInvalid)
		}
		seen[f.Name] = true
		switch f.Status {
		case "finding":
			if len(f.EvidenceIDs) == 0 {
				return fmt.Errorf("%w：finding 必须有原文证据", ErrInvalid)
			}
		case "inference", "unverified", "unknown":
		default:
			return ErrInvalid
		}
		if f.Status == "unknown" && len(f.EvidenceIDs) > 0 {
			return fmt.Errorf("%w：unknown 不应附带结论证据", ErrInvalid)
		}
	}
	return nil
}
