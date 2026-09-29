package web

import (
	"errors"
	"net/http"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/service"
)

// Tariff editor routes (/admin/api/tariffs*, /admin/api/plans). They share the
// admin session, CSRF and audit/idempotency contract of the other mutations:
// every write carries a request_key; the same key with the same payload
// replays the recorded outcome, with another payload it is a conflict.
//
// Tariff bodies carry free text (description, features), so they get a larger
// limit than the 4 KiB lifecycle mutations; the reorder list is larger still.
const (
	adminTariffMaxBody  = 16 << 10
	adminReorderMaxBody = 64 << 10
)

func (a *adminAPI) tariffSvcOK(w http.ResponseWriter) bool {
	if a.tariffSvc == nil {
		writeAdminError(w, http.StatusServiceUnavailable, "service_unavailable")
		return false
	}
	return true
}

// routeTariffs dispatches /admin/api/tariffs[/...]; segments[0] == "tariffs".
func (a *adminAPI) routeTariffs(w http.ResponseWriter, r *http.Request, segments []string) {
	switch {
	case len(segments) == 1:
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			a.tariffList(w, r)
		case http.MethodPost:
			a.tariffCreate(w, r)
		default:
			w.Header().Set("Allow", "GET, HEAD, POST")
			writeAdminError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		}
	case len(segments) == 2 && segments[1] == "reorder":
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			writeAdminError(w, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
		a.tariffReorder(w, r)
	case len(segments) == 2:
		id, ok := parseAdminID(segments[1])
		if !ok {
			writeAdminError(w, http.StatusNotFound, "not_found")
			return
		}
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			a.tariffGet(w, r, id)
		case http.MethodPatch:
			a.tariffUpdate(w, r, id)
		case http.MethodDelete:
			a.tariffDelete(w, r, id)
		default:
			w.Header().Set("Allow", "GET, HEAD, PATCH, DELETE")
			writeAdminError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		}
	case len(segments) == 3 && (segments[2] == "enable" || segments[2] == "disable"):
		id, ok := parseAdminID(segments[1])
		if !ok {
			writeAdminError(w, http.StatusNotFound, "not_found")
			return
		}
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			writeAdminError(w, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
		a.tariffSetActive(w, r, id, segments[2] == "enable")
	default:
		writeAdminError(w, http.StatusNotFound, "not_found")
	}
}

func (a *adminAPI) tariffList(w http.ResponseWriter, r *http.Request) {
	if !a.tariffSvcOK(w) {
		return
	}
	views, err := a.tariffSvc.ListTariffs(r.Context())
	if err != nil {
		a.internal(w, "tariffs", err)
		return
	}
	writeAdminJSON(w, http.StatusOK, struct {
		Tariffs []service.TariffView `json:"tariffs"`
	}{views})
}

func (a *adminAPI) tariffGet(w http.ResponseWriter, r *http.Request, id uint) {
	if !a.tariffSvcOK(w) {
		return
	}
	view, err := a.tariffSvc.GetTariff(r.Context(), id)
	if err != nil {
		if errors.Is(err, database.ErrProductNotFound) {
			writeAdminError(w, http.StatusNotFound, "not_found")
			return
		}
		a.internal(w, "tariff", err)
		return
	}
	writeAdminJSON(w, http.StatusOK, view)
}

func (a *adminAPI) plansList(w http.ResponseWriter, r *http.Request) {
	if !a.tariffSvcOK(w) {
		return
	}
	views, err := a.tariffSvc.ListPlans(r.Context())
	if err != nil {
		a.internal(w, "plans", err)
		return
	}
	writeAdminJSON(w, http.StatusOK, struct {
		Plans []service.TariffPlanView `json:"plans"`
	}{views})
}

// tariffBody is the editable content shared by create and update. Unknown
// fields are rejected (decodeAdminJSON), so a typo is never a silent no-op.
type tariffBody struct {
	RequestKey   string   `json:"request_key"`
	Name         string   `json:"name"`
	PlanID       uint     `json:"plan_id"`
	DurationDays int      `json:"duration_days"`
	PriceCents   int64    `json:"price_cents"`
	Currency     string   `json:"currency"`
	Description  string   `json:"description"`
	Features     []string `json:"features"`
	Badge        string   `json:"badge"`
	SortOrder    *int     `json:"sort_order"`
	IsActive     bool     `json:"is_active"`
}

func (b tariffBody) input() service.TariffInput {
	return service.TariffInput{
		Name: b.Name, PlanID: b.PlanID, DurationDays: b.DurationDays, PriceCents: b.PriceCents,
		Currency: b.Currency, Description: b.Description, Features: b.Features, Badge: b.Badge,
		SortOrder: b.SortOrder, IsActive: b.IsActive,
	}
}

func (a *adminAPI) tariffCreate(w http.ResponseWriter, r *http.Request) {
	if !a.tariffSvcOK(w) {
		return
	}
	var body tariffBody
	if !decodeAdminJSONLimit(w, r, &body, adminTariffMaxBody) {
		return
	}
	outcome, err := a.tariffSvc.CreateTariff(r.Context(), a.actor, body.RequestKey, body.input())
	a.writeTariffOutcome(w, "tariff/create", http.StatusCreated, outcome, err)
}

type tariffUpdateBody struct {
	tariffBody
	Version int `json:"version"`
}

func (a *adminAPI) tariffUpdate(w http.ResponseWriter, r *http.Request, id uint) {
	if !a.tariffSvcOK(w) {
		return
	}
	var body tariffUpdateBody
	if !decodeAdminJSONLimit(w, r, &body, adminTariffMaxBody) {
		return
	}
	outcome, err := a.tariffSvc.UpdateTariff(r.Context(), a.actor, body.RequestKey, id, body.Version, body.input())
	a.writeTariffOutcome(w, "tariff/update", http.StatusOK, outcome, err)
}

// tariffVersionBody is the body of enable/disable/delete.
type tariffVersionBody struct {
	RequestKey string `json:"request_key"`
	Version    int    `json:"version"`
}

func (a *adminAPI) tariffSetActive(w http.ResponseWriter, r *http.Request, id uint, active bool) {
	if !a.tariffSvcOK(w) {
		return
	}
	var body tariffVersionBody
	if !decodeAdminJSONLimit(w, r, &body, adminTariffMaxBody) {
		return
	}
	outcome, err := a.tariffSvc.SetTariffActive(r.Context(), a.actor, body.RequestKey, id, body.Version, active)
	a.writeTariffOutcome(w, "tariff/active", http.StatusOK, outcome, err)
}

func (a *adminAPI) tariffDelete(w http.ResponseWriter, r *http.Request, id uint) {
	if !a.tariffSvcOK(w) {
		return
	}
	var body tariffVersionBody
	if !decodeAdminJSONLimit(w, r, &body, adminTariffMaxBody) {
		return
	}
	outcome, err := a.tariffSvc.DeleteTariff(r.Context(), a.actor, body.RequestKey, id, body.Version)
	if err != nil {
		a.writeTariffError(w, "tariff/delete", err)
		return
	}
	writeAdminJSON(w, http.StatusOK, struct {
		Deleted uint `json:"deleted"`
		*service.TariffOutcome
	}{id, outcome})
}

type tariffReorderBody struct {
	RequestKey string `json:"request_key"`
	IDs        []uint `json:"ids"`
}

func (a *adminAPI) tariffReorder(w http.ResponseWriter, r *http.Request) {
	if !a.tariffSvcOK(w) {
		return
	}
	var body tariffReorderBody
	if !decodeAdminJSONLimit(w, r, &body, adminReorderMaxBody) {
		return
	}
	views, outcome, err := a.tariffSvc.ReorderTariffs(r.Context(), a.actor, body.RequestKey, body.IDs)
	if err != nil {
		a.writeTariffError(w, "tariff/reorder", err)
		return
	}
	writeAdminJSON(w, http.StatusOK, struct {
		Tariffs  []service.TariffView   `json:"tariffs"`
		Replayed bool                   `json:"replayed"`
		Audit    database.AdminAuditLog `json:"audit"`
	}{views, outcome.Replayed, outcome.Audit})
}

func (a *adminAPI) writeTariffOutcome(w http.ResponseWriter, route string, status int, outcome *service.TariffOutcome, err error) {
	if err != nil {
		if outcome != nil && outcome.Audit.ID != 0 {
			// Committed and audited, but reloading the result failed: report
			// it as an internal error; a retry with the same key replays.
			a.internal(w, route, err)
			return
		}
		a.writeTariffError(w, route, err)
		return
	}
	writeAdminJSON(w, status, outcome)
}

// writeTariffError maps the tariff rejections to stable codes. Invalid content
// also names the offending field so the editor can highlight it.
func (a *adminAPI) writeTariffError(w http.ResponseWriter, route string, err error) {
	var fieldErr *database.TariffFieldError
	switch {
	case errors.As(err, &fieldErr):
		writeAdminJSON(w, http.StatusBadRequest, struct {
			Error string `json:"error"`
			Field string `json:"field"`
		}{"invalid_tariff", fieldErr.Field})
	case errors.Is(err, database.ErrAdminInvalidRequest):
		writeAdminError(w, http.StatusBadRequest, "invalid_request")
	case errors.Is(err, database.ErrProductNotFound):
		writeAdminError(w, http.StatusNotFound, "not_found")
	case errors.Is(err, database.ErrTariffVersionConflict):
		writeAdminError(w, http.StatusConflict, "version_conflict")
	case errors.Is(err, database.ErrTariffInUse):
		writeAdminError(w, http.StatusConflict, "tariff_in_use")
	case errors.Is(err, database.ErrTariffSuperseded):
		writeAdminError(w, http.StatusConflict, "tariff_superseded")
	case errors.Is(err, database.ErrTariffOrderStale):
		writeAdminError(w, http.StatusConflict, "order_stale")
	case errors.Is(err, database.ErrAdminRequestKeyConflict):
		writeAdminError(w, http.StatusConflict, "request_key_conflict")
	default:
		a.internal(w, route, err)
	}
}
