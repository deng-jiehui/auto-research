package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

type MatrixRow struct {
	WorkItemID       string              `json:"work_item_id"`
	PaperID          string              `json:"paper_id"`
	Title            string              `json:"title"`
	DocumentID       string              `json:"document_id,omitempty"`
	ContentKind      string              `json:"content_kind,omitempty"`
	State            string              `json:"state"`
	AnalysisRevision int64               `json:"analysis_revision"`
	AnalysisHash     string              `json:"analysis_hash,omitempty"`
	Analysis         *PaperAnalysisDraft `json:"analysis,omitempty"`
}

type EvidenceMatrix struct {
	RunID             string      `json:"run_id"`
	SpecHash          string      `json:"spec_hash"`
	PlanHash          string      `json:"plan_hash"`
	CandidateRevision int64       `json:"candidate_revision"`
	Hash              string      `json:"hash"`
	Rows              []MatrixRow `json:"rows"`
}

// 矩阵由唯一的分析台账投影，执行次数与 Token 不能改变内容版本。
func BuildEvidenceMatrix(run ResearchRun) EvidenceMatrix {
	m := EvidenceMatrix{RunID: run.ID, SpecHash: run.SpecHash, PlanHash: run.PlanHash, CandidateRevision: run.ApprovedCandidateRevision, Rows: []MatrixRow{}}
	for _, w := range run.WorkItems {
		m.Rows = append(m.Rows, MatrixRow{WorkItemID: w.ID, PaperID: w.PaperID, Title: w.Title, DocumentID: w.DocumentID, ContentKind: w.ContentKind, State: w.State, AnalysisRevision: w.AnalysisRevision, AnalysisHash: w.AnalysisHash, Analysis: w.Analysis})
	}
	data, _ := json.Marshal(m)
	sum := sha256.Sum256(data)
	m.Hash = hex.EncodeToString(sum[:])
	return m
}

type AnalysisReference struct {
	WorkItemID       string `json:"work_item_id"`
	AnalysisRevision int64  `json:"analysis_revision"`
}

type ReportClaim struct {
	Section      string              `json:"section" jsonschema:"comparison 方法比较；conflicts 冲突和未知；gaps 待验证空白；next_steps 实验建议。四类均须覆盖"`
	Status       string              `json:"status" jsonschema:"finding 有原文依据；inference 推断；unverified 待验证；unknown 未知"`
	Text         string              `json:"text" jsonschema:"结论或未知原因，最多 1500 字符；不夹带引用链接，真实链接由程序生成"`
	AnalysisRefs []AnalysisReference `json:"analysis_refs" jsonschema:"本任务矩阵中的 work_item_id 和 analysis_revision，至少一个"`
	EvidenceIDs  []string            `json:"evidence_ids,omitempty" jsonschema:"被所引用分析使用的真实 EvidenceID；finding 必填"`
}

type ReportDraft struct {
	Title       string        `json:"title"`
	Summary     string        `json:"summary" jsonschema:"简要综合，最多 800 字符，不能冒充已核实事实"`
	MatrixHash  string        `json:"matrix_hash" jsonschema:"当前 matrix.json 返回的 hash，绑定全部分析版本"`
	Limitations []string      `json:"limitations" jsonschema:"1 至 12 条阅读范围或评测限制，不能遗漏失败或仅摘要事实"`
	Claims      []ReportClaim `json:"claims" jsonschema:"4 至 40 条结构化结论，四个 section 都须覆盖；无资料时明确 unknown"`
}

type ReportReference struct {
	ID           string `json:"id"`
	Revision     int64  `json:"revision"`
	MatrixHash   string `json:"matrix_hash"`
	Summary      string `json:"summary"`
	ResourcePath string `json:"resource_path"`
}

type ResearchReport struct {
	ReportReference
	RunID      string         `json:"run_id"`
	ProjectID  string         `json:"project_id"`
	Title      string         `json:"title"`
	CreatedAt  time.Time      `json:"created_at"`
	ArtifactID string         `json:"artifact_id"`
	Draft      ReportDraft    `json:"draft"`
	Matrix     EvidenceMatrix `json:"matrix"`
	References []Evidence     `json:"references"`
	Markdown   string         `json:"markdown,omitempty"`
}

func ValidateReportDraft(d ReportDraft) error {
	validText := func(v string, max int) bool { return strings.TrimSpace(v) != "" && utf8.RuneCountInString(v) <= max }
	if !validText(d.Title, 200) || !validText(d.Summary, 800) || d.MatrixHash == "" || len(d.Limitations) < 1 || len(d.Limitations) > 12 || len(d.Claims) < 4 || len(d.Claims) > 40 {
		return fmt.Errorf("%w：报告须有标题、摘要、矩阵版本、限制和四类结论", ErrInvalid)
	}
	for _, v := range d.Limitations {
		if !validText(v, 1000) {
			return ErrInvalid
		}
	}
	sections := map[string]bool{"comparison": false, "conflicts": false, "gaps": false, "next_steps": false}
	for _, c := range d.Claims {
		if _, ok := sections[c.Section]; !ok {
			return ErrInvalid
		}
		sections[c.Section] = true
		if !validText(c.Text, 1500) || len(c.AnalysisRefs) < 1 || len(c.AnalysisRefs) > 30 || len(c.EvidenceIDs) > 16 {
			return ErrInvalid
		}
		if c.Status != "finding" && c.Status != "inference" && c.Status != "unverified" && c.Status != "unknown" {
			return ErrInvalid
		}
		if c.Status == "finding" && len(c.EvidenceIDs) == 0 || c.Status == "unknown" && len(c.EvidenceIDs) > 0 {
			return fmt.Errorf("%w：finding 需要证据，unknown 不应附结论证据", ErrInvalid)
		}
		seen := map[string]bool{}
		for _, ref := range c.AnalysisRefs {
			if ref.WorkItemID == "" || ref.AnalysisRevision < 1 || seen[ref.WorkItemID] {
				return ErrInvalid
			}
			seen[ref.WorkItemID] = true
		}
		seen = map[string]bool{}
		for _, id := range c.EvidenceIDs {
			if id == "" || seen[id] {
				return ErrInvalid
			}
			seen[id] = true
		}
	}
	for _, present := range sections {
		if !present {
			return fmt.Errorf("%w：比较、冲突与未知、空白假设、下一步实验四类都必须覆盖", ErrInvalid)
		}
	}
	return nil
}
