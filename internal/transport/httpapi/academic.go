package httpapi

import (
	"errors"
	"net/http"

	"github.com/deng-jiehui/auto-research/internal/domain"
)

func (a *api) searchAcademic(w http.ResponseWriter, r *http.Request) {
	if _, err := a.projects.Get(r.Context(), scope(r)); err != nil {
		a.fail(w, r, err)
		return
	}
	if a.academic == nil {
		a.fail(w, r, domain.ErrUnavailable)
		return
	}
	var query domain.PaperQuery
	if err := decode(w, r, &query); err != nil {
		a.fail(w, r, err)
		return
	}
	items, err := a.academic.Search(r.Context(), query)
	if err != nil {
		if errors.Is(err, domain.ErrUnavailable) {
			a.logger.Error("学术来源查询失败", "error", err)
			writeProblem(w, http.StatusServiceUnavailable, "academic_unavailable", "学术来源暂不可用，请检查 MCP 连接或稍后重试；当前不会切换为演示数据。")
			return
		}
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (a *api) academicStatus(w http.ResponseWriter, r *http.Request) {
	if _, err := a.projects.Get(r.Context(), scope(r)); err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, a.academic.Status())
}
