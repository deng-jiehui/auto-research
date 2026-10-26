package httpapi

import (
	"net/http"
	"strconv"

	"github.com/deng-jiehui/auto-research/internal/domain"
)

func (a *api) fetchPaperDocument(w http.ResponseWriter, r *http.Request) {
	if a.documents == nil {
		a.fail(w, r, domain.ErrUnavailable)
		return
	}
	var input struct {
		Refresh bool `json:"refresh"`
	}
	if r.ContentLength != 0 {
		if err := decode(w, r, &input); err != nil {
			a.fail(w, r, err)
			return
		}
	}
	doc, err := a.documents.Fetch(r.Context(), scope(r), r.PathValue("paperID"), input.Refresh)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, 200, doc)
}
func (a *api) documentFetchStatus(w http.ResponseWriter, r *http.Request) {
	if a.documents == nil {
		a.fail(w, r, domain.ErrUnavailable)
		return
	}
	f, err := a.documents.Status(r.Context(), scope(r), r.PathValue("paperID"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, 200, f)
}
func (a *api) getDocument(w http.ResponseWriter, r *http.Request) {
	if a.documents == nil {
		a.fail(w, r, domain.ErrUnavailable)
		return
	}
	doc, err := a.documents.Contents(r.Context(), scope(r), r.PathValue("documentID"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, 200, doc)
}
func (a *api) getDocumentBlock(w http.ResponseWriter, r *http.Request) {
	if a.documents == nil {
		a.fail(w, r, domain.ErrUnavailable)
		return
	}
	b, err := a.documents.Read(r.Context(), scope(r), r.PathValue("documentID"), r.PathValue("blockID"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, 200, b)
}
func (a *api) searchDocuments(w http.ResponseWriter, r *http.Request) {
	if a.documents == nil {
		a.fail(w, r, domain.ErrUnavailable)
		return
	}
	limit := 10
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			a.fail(w, r, domain.ErrInvalid)
			return
		}
		limit = n
	}
	hits, err := a.documents.Search(r.Context(), scope(r), r.URL.Query().Get("q"), limit)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": hits})
}
func (a *api) getEvidence(w http.ResponseWriter, r *http.Request) {
	if a.documents == nil {
		a.fail(w, r, domain.ErrUnavailable)
		return
	}
	ev, err := a.documents.Evidence(r.Context(), scope(r), r.PathValue("evidenceID"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, 200, ev)
}
