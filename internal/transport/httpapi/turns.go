package httpapi

import (
	"net/http"
	"strconv"

	"github.com/deng-jiehui/auto-research/internal/app"
	"github.com/deng-jiehui/auto-research/internal/domain"
	"github.com/deng-jiehui/auto-research/internal/ui"
)

func unavailableRuntimeError() error { return domain.ErrUnavailable }

func (a *api) sendMessage(w http.ResponseWriter, r *http.Request) {
	if a.turns == nil {
		a.fail(w, r, unavailableRuntimeError())
		return
	}
	var in app.SendInput
	if err := decode(w, r, &in); err != nil {
		a.fail(w, r, err)
		return
	}
	result, err := a.turns.Send(r.Context(), scope(r), r.PathValue("sessionID"), in)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, result)
}

func (a *api) getTurn(w http.ResponseWriter, r *http.Request) {
	if a.turns == nil {
		a.fail(w, r, unavailableRuntimeError())
		return
	}
	turn, err := a.turns.Get(r.Context(), scope(r), r.PathValue("sessionID"), r.PathValue("turnID"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, turn)
}

func (a *api) getSnapshot(w http.ResponseWriter, r *http.Request) {
	if a.turns == nil {
		a.fail(w, r, unavailableRuntimeError())
		return
	}
	limit := queryInt(r, "limit", 100)
	snapshot, err := a.ui.Snapshot(r.Context(), scope(r), ui.Target{Kind: "session", ID: r.PathValue("sessionID")}, limit)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (a *api) listEvents(w http.ResponseWriter, r *http.Request) {
	a.uiEvents(w, r, ui.Target{Kind: "session", ID: r.PathValue("sessionID")})
}

func (a *api) listNotes(w http.ResponseWriter, r *http.Request) {
	if a.turns == nil {
		a.fail(w, r, unavailableRuntimeError())
		return
	}
	notes, err := a.turns.Notes(r.Context(), scope(r), queryInt(r, "limit", 20))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": notes})
}

func (a *api) deleteNote(w http.ResponseWriter, r *http.Request) {
	if a.turns == nil {
		a.fail(w, r, unavailableRuntimeError())
		return
	}
	if err := a.turns.DeleteNote(r.Context(), scope(r), r.PathValue("noteID")); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) resumeTurn(w http.ResponseWriter, r *http.Request) {
	a.respondUI(w, r, ui.Target{Kind: "session", ID: r.PathValue("sessionID")})
}

func (a *api) retryTurn(w http.ResponseWriter, r *http.Request) {
	a.actionTurn(w, r, func(in app.TurnActionInput) (app.TurnActionResult, error) {
		return a.ui.Act(r.Context(), scope(r), ui.Target{Kind: "session", ID: r.PathValue("sessionID")}, r.PathValue("turnID"), "retry", app.ResearchControlInput{TurnActionInput: in})
	})
}

func (a *api) editMessage(w http.ResponseWriter, r *http.Request) {
	if a.turns == nil {
		a.fail(w, r, unavailableRuntimeError())
		return
	}
	var in app.EditMessageInput
	if err := decode(w, r, &in); err != nil {
		a.fail(w, r, err)
		return
	}
	result, err := a.turns.EditMessage(r.Context(), scope(r), r.PathValue("sessionID"), r.PathValue("turnID"), in)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, result)
}

func (a *api) cancelTurn(w http.ResponseWriter, r *http.Request) {
	a.actionTurn(w, r, func(in app.TurnActionInput) (app.TurnActionResult, error) {
		return a.ui.Act(r.Context(), scope(r), ui.Target{Kind: "session", ID: r.PathValue("sessionID")}, r.PathValue("turnID"), "cancel", app.ResearchControlInput{TurnActionInput: in})
	})
}

func (a *api) actionTurn(w http.ResponseWriter, r *http.Request, action func(app.TurnActionInput) (app.TurnActionResult, error)) {
	if a.turns == nil {
		a.fail(w, r, unavailableRuntimeError())
		return
	}
	var in app.TurnActionInput
	if err := decode(w, r, &in); err != nil {
		a.fail(w, r, err)
		return
	}
	result, err := action(in)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, result)
}

func queryInt(r *http.Request, key string, fallback int) int {
	value := r.URL.Query().Get(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return parsed
}
