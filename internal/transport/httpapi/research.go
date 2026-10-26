package httpapi

import (
	"errors"
	"net/http"

	"github.com/deng-jiehui/auto-research/internal/app"
	"github.com/deng-jiehui/auto-research/internal/domain"
	"github.com/deng-jiehui/auto-research/internal/ui"
)

func (a *api) startResearch(w http.ResponseWriter, r *http.Request) {
	if a.researchReady(w, r) == nil {
		return
	}
	var in app.StartResearchInput
	if err := decode(w, r, &in); err != nil {
		a.fail(w, r, err)
		return
	}
	created, err := a.turns.StartResearch(r.Context(), scope(r), in)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, created)
}

func (a *api) researchSnapshot(w http.ResponseWriter, r *http.Request) {
	if a.researchReady(w, r) == nil {
		return
	}
	snapshot, err := a.ui.Snapshot(r.Context(), scope(r), ui.Target{Kind: "research", ID: r.PathValue("runID")}, 100)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, 200, snapshot)
}

func (a *api) researchEvents(w http.ResponseWriter, r *http.Request) {
	a.uiEvents(w, r, ui.Target{Kind: "research", ID: r.PathValue("runID")})
}

func (a *api) respondResearch(w http.ResponseWriter, r *http.Request) {
	a.respondUI(w, r, ui.Target{Kind: "research", ID: r.PathValue("runID")})
}

func (a *api) sessionResearch(w http.ResponseWriter, r *http.Request) {
	service := a.researchReady(w, r)
	if service == nil {
		return
	}
	items, err := service.TaskSummaries(r.Context(), scope(r), r.PathValue("sessionID"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (a *api) researchReady(w http.ResponseWriter, r *http.Request) *app.ResearchService {
	if a.turns == nil || a.turns.ResearchService() == nil {
		a.fail(w, r, domain.ErrUnavailable)
		return nil
	}
	if _, err := a.projects.Get(r.Context(), scope(r)); err != nil {
		a.fail(w, r, err)
		return nil
	}
	return a.turns.ResearchService()
}
func (a *api) listResearch(w http.ResponseWriter, r *http.Request) {
	service := a.researchReady(w, r)
	if service == nil {
		return
	}
	items, err := service.List(r.Context(), scope(r))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}
func (a *api) getResearch(w http.ResponseWriter, r *http.Request) {
	service := a.researchReady(w, r)
	if service == nil {
		return
	}
	item, err := service.Get(r.Context(), scope(r), r.PathValue("runID"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, 200, item)
}

func (a *api) deleteResearch(w http.ResponseWriter, r *http.Request) {
	service := a.researchReady(w, r)
	if service == nil {
		return
	}
	if err := service.Delete(r.Context(), scope(r), r.PathValue("runID")); err != nil {
		if errors.Is(err, domain.ErrBusy) {
			writeProblem(w, http.StatusConflict, "research_active", "调研任务尚未结束，请先结束任务后再删除。")
			return
		}
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) controlResearch(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if a.researchReady(w, r) == nil {
			return
		}
		var input app.ResearchControlInput
		if err := decode(w, r, &input); err != nil {
			a.fail(w, r, err)
			return
		}
		result, err := a.ui.Act(r.Context(), scope(r), ui.Target{Kind: "research", ID: r.PathValue("runID")}, "", kind, input)
		if err != nil {
			a.fail(w, r, err)
			return
		}
		writeJSON(w, http.StatusAccepted, result)
	}
}
