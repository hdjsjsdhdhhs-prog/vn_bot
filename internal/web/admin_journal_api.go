package web

import (
	"errors"
	"net/http"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/service"
)

const adminJournalMaxQuery = 128

// journalList serves GET /admin/api/journal. Query parameters:
//
//	q          Telegram ID / subscription ID (exact) or username substring
//	type       database.JournalEventType
//	plan_kind  free | trial | paid
//	from, to   RFC 3339 instants; from inclusive, to exclusive
//	limit, offset  paging (server default and cap as for users)
func (a *adminAPI) journalList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, ok1 := parseAdminInt(q.Get("limit"), 0)
	offset, ok2 := parseAdminInt(q.Get("offset"), 0)
	from, ok3 := parseAdminTime(q.Get("from"))
	to, ok4 := parseAdminTime(q.Get("to"))
	if !ok1 || !ok2 || !ok3 || !ok4 || limit < 0 || offset < 0 || len(q.Get("q")) > adminJournalMaxQuery {
		writeAdminError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	page, err := a.svc.ListJournal(r.Context(), database.JournalFilter{
		Query: q.Get("q"), Type: q.Get("type"), PlanKind: q.Get("plan_kind"),
		From: from, To: to, Limit: limit, Offset: offset,
	})
	if err != nil {
		switch {
		case errors.Is(err, database.ErrAdminInvalidRequest):
			writeAdminError(w, http.StatusBadRequest, "invalid_request")
		case errors.Is(err, service.ErrJournalUnavailable):
			writeAdminError(w, http.StatusServiceUnavailable, "service_unavailable")
		default:
			a.internal(w, "journal", err)
		}
		return
	}
	writeAdminJSON(w, http.StatusOK, page)
}

// journalEvent serves GET /admin/api/journal/{id}.
func (a *adminAPI) journalEvent(w http.ResponseWriter, r *http.Request, id uint) {
	event, err := a.svc.GetJournalEvent(r.Context(), id)
	if err != nil {
		switch {
		case errors.Is(err, database.ErrJournalEventNotFound):
			writeAdminError(w, http.StatusNotFound, "not_found")
		case errors.Is(err, service.ErrJournalUnavailable):
			writeAdminError(w, http.StatusServiceUnavailable, "service_unavailable")
		default:
			a.internal(w, "journal_event", err)
		}
		return
	}
	writeAdminJSON(w, http.StatusOK, event)
}

// parseAdminTime accepts an empty value (no bound) or an RFC 3339 instant.
func parseAdminTime(raw string) (*time.Time, bool) {
	if raw == "" {
		return nil, true
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, false
	}
	parsed = parsed.UTC()
	return &parsed, true
}
