package domain

import (
	"fmt"
	"net/url"
	"strings"
)

var reportSections = []struct{ ID, Title string }{{"comparison", "方法比较"}, {"conflicts", "冲突和未知项"}, {"gaps", "待验证研究空白"}, {"next_steps", "下一步实验建议"}}
var certaintyLabels = map[string]string{"finding": "有原文依据", "inference": "推断", "unverified": "待验证", "unknown": "未知"}

// 模型只提交文字和引用身份，正文链接与版本信息由可信数据生成。
func reportText(s string) string {
	return strings.NewReplacer("\\", "\\\\", "`", "\\`", "*", "\\*", "_", "\\_", "[", "\\[", "]", "\\]", "<", "&lt;", ">", "&gt;", "#", "\\#", "|", "\\|", "\n", " ", "\r", " ").Replace(s)
}

func EvidenceCitationURL(project string, e Evidence) string {
	return "/p/" + url.PathEscape(project) + "/papers?paper=" + url.QueryEscape(e.PaperID) + "&evidence=" + url.QueryEscape(e.ID)
}

func RenderResearchReport(run ResearchRun, r ResearchReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n报告 v%d · %s\n\n## 研究问题与范围\n\n%s\n\n", reportText(r.Title), r.Revision, r.CreatedAt.UTC().Format("2006-01-02 15:04 UTC"), reportText(run.Spec.Question))
	fmt.Fprintf(&b, "年份：%d–%d（0 表示不限） · 来源：%s\n\n矩阵版本：`%s`\n\n", run.Spec.Settings.YearFrom, run.Spec.Settings.YearTo, reportText(strings.Join(run.Spec.Settings.Providers, "、")), r.MatrixHash)
	for _, v := range run.Spec.Constraints {
		fmt.Fprintf(&b, "- 约束：%s\n", reportText(v))
	}
	fmt.Fprintf(&b, "\n## 摘要（模型综合）\n\n%s\n\n## 检索与纳入排除\n\n", reportText(r.Summary))
	fmt.Fprintf(&b, "确认计划 v%d；候选审核 v%d；检索 %d 次，%d 轮。检索记录与时间可在任务执行详情核对。\n\n", run.ApprovedPlanRevision, run.ApprovedCandidateRevision, run.CandidateSearches, run.CandidateRound)
	for _, q := range run.Plan.Queries {
		fmt.Fprintf(&b, "- 检索式：%s\n", reportText(q))
	}
	for _, v := range run.Plan.Inclusion {
		fmt.Fprintf(&b, "- 纳入条件：%s\n", reportText(v))
	}
	for _, v := range run.Plan.Exclusion {
		fmt.Fprintf(&b, "- 排除条件：%s\n", reportText(v))
	}
	if run.CandidateReview != nil {
		for _, c := range run.CandidateReview.Choices {
			title := c.CandidateID
			for _, candidate := range run.Candidates {
				if candidate.ID == c.CandidateID {
					title = candidate.Title
				}
			}
			label := "排除"
			if c.Selected {
				label = "纳入"
			}
			fmt.Fprintf(&b, "- %s：%s；理由：%s\n", label, reportText(title), reportText(c.Reason))
		}
	}
	b.WriteString("\n## 证据矩阵\n\n| 论文 | 阅读级别与状态 | 方法 | 数据集 | 指标 | 代码 | 复现条件 |\n| --- | --- | --- | --- | --- | --- | --- |\n")
	for _, row := range r.Matrix.Rows {
		level := row.ContentKind
		if level == "abstract" {
			level = "仅摘要"
		}
		if level == "" {
			level = "未取得来源"
		}
		fmt.Fprintf(&b, "| %s | %s / %s", reportText(row.Title), reportText(level), reportText(row.State))
		for _, name := range []string{"method", "datasets", "metrics", "code", "reproduction"} {
			value := "未知"
			if row.State == "failed" && row.Analysis != nil {
				value = "失败：" + row.Analysis.Error
			}
			if row.Analysis != nil {
				for _, f := range row.Analysis.Fields {
					if f.Name == name {
						value = certaintyLabels[f.Status] + "：" + f.Text
					}
				}
			}
			fmt.Fprintf(&b, " | %s", reportText(value))
		}
		b.WriteString(" |\n")
	}
	for _, section := range reportSections {
		fmt.Fprintf(&b, "\n## %s\n\n", section.Title)
		for _, c := range r.Draft.Claims {
			if c.Section != section.ID {
				continue
			}
			fmt.Fprintf(&b, "- **%s**：%s", certaintyLabels[c.Status], reportText(c.Text))
			for _, id := range c.EvidenceIDs {
				for _, e := range r.References {
					if e.ID == id {
						fmt.Fprintf(&b, " [查看原文](%s)", EvidenceCitationURL(run.ProjectID, e))
					}
				}
			}
			b.WriteString("\n")
		}
	}
	b.WriteString("\n## 限制与覆盖说明\n\n")
	for _, v := range r.Draft.Limitations {
		fmt.Fprintf(&b, "- %s\n", reportText(v))
	}
	for _, row := range r.Matrix.Rows {
		if row.State == "failed" {
			fmt.Fprintf(&b, "- 未完成分析：%s。\n", reportText(row.Title))
		}
		if row.ContentKind == "abstract" {
			fmt.Fprintf(&b, "- %s 仅读取摘要，实验细节未经全文核实。\n", reportText(row.Title))
		}
	}
	b.WriteString("- 程序校验引用身份、原文片段和版本；证据是否支持模型的语义归纳，仍需人工复核。\n\n## 原文引用\n\n")
	if len(r.References) == 0 {
		b.WriteString("当前报告没有可核实的事实引用，结论均须按标注视为未知、推断或待验证。\n")
	}
	for i, e := range r.References {
		fmt.Fprintf(&b, "%d. [打开冻结原文](%s)：%s\n\n", i+1, EvidenceCitationURL(run.ProjectID, e), reportText(e.Quote))
	}
	return b.String()
}
