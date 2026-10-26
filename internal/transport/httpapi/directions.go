package httpapi

import (
	"net/http"
	"strings"

	"github.com/deng-jiehui/auto-research/internal/domain"
)

func (a *api) directionReady(w http.ResponseWriter, r *http.Request) bool {
	if a.directions == nil {
		a.fail(w, r, domain.ErrUnavailable)
		return false
	}
	if _, err := a.projects.Get(r.Context(), scope(r)); err != nil {
		a.fail(w, r, err)
		return false
	}
	return true
}
func (a *api) listDirections(w http.ResponseWriter, r *http.Request) {
	if !a.directionReady(w, r) {
		return
	}
	items, err := a.directions.List(r.Context(), scope(r))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}
func (a *api) getDirection(w http.ResponseWriter, r *http.Request) {
	if !a.directionReady(w, r) {
		return
	}
	item, err := a.directions.Get(r.Context(), scope(r), r.PathValue("directionID"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, 200, item)
}
func (a *api) directionHistory(w http.ResponseWriter, r *http.Request) {
	if !a.directionReady(w, r) {
		return
	}
	items, err := a.directions.History(r.Context(), scope(r), r.PathValue("directionID"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}
func (a *api) proposeDirection(w http.ResponseWriter, r *http.Request) {
	if !a.directionReady(w, r) {
		return
	}
	var input struct {
		domain.DirectionDraft
		IdempotencyKey string `json:"idempotency_key"`
	}
	if err := decode(w, r, &input); err != nil {
		a.fail(w, r, err)
		return
	}
	if strings.TrimSpace(input.IdempotencyKey) == "" || len(input.IdempotencyKey) > 128 {
		a.fail(w, r, domain.ErrInvalid)
		return
	}
	item, err := a.directions.Propose(r.Context(), scope(r), input.DirectionDraft, "", "", "http:"+input.IdempotencyKey)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, 201, item)
}
func (a *api) editDirection(w http.ResponseWriter, r *http.Request) {
	if !a.directionReady(w, r) {
		return
	}
	var input struct {
		domain.DirectionDraft
		Revision int64 `json:"revision"`
	}
	if err := decode(w, r, &input); err != nil {
		a.fail(w, r, err)
		return
	}
	item, err := a.directions.Edit(r.Context(), scope(r), r.PathValue("directionID"), input.Revision, input.DirectionDraft)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, 200, item)
}
func (a *api) decideDirection(state string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.directionReady(w, r) {
			return
		}
		var input struct {
			Revision int64 `json:"revision"`
		}
		if err := decode(w, r, &input); err != nil {
			a.fail(w, r, err)
			return
		}
		item, err := a.directions.Decide(r.Context(), scope(r), r.PathValue("directionID"), input.Revision, state)
		if err != nil {
			a.fail(w, r, err)
			return
		}
		writeJSON(w, 200, item)
	}
}
