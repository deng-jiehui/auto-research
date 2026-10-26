package httpapi

import "net/http"

func (a *api) researchMatrix(w http.ResponseWriter, r *http.Request) {
	service := a.researchReady(w, r)
	if service == nil {
		return
	}
	result, err := service.Matrix(r.Context(), scope(r), r.PathValue("runID"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
func (a *api) researchReports(w http.ResponseWriter, r *http.Request) {
	service := a.researchReady(w, r)
	if service == nil {
		return
	}
	reports, err := service.Reports(r.Context(), scope(r), r.PathValue("runID"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	// 列表只返回版本和摘要，完整矩阵与正文按选中的报告 ID 读取。
	items := make([]any, 0, len(reports))
	for _, report := range reports {
		items = append(items, report.ReportReference)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
func (a *api) researchReport(w http.ResponseWriter, r *http.Request) {
	service := a.researchReady(w, r)
	if service == nil {
		return
	}
	report, err := service.Report(r.Context(), scope(r), r.PathValue("reportID"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}
