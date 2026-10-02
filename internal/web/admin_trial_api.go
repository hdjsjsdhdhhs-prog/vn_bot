package web

import (
	"errors"
	"net/http"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/service"
)

// Trial editor routes (/admin/api/trial*). Same session, CSRF and
// audit/idempotency contract as the tariff editor:
//
//	GET  /admin/api/trial               editor state
//	PUT  /admin/api/trial               save (request_key, version)
//	POST /admin/api/trial/preview       evaluate an unsaved draft (no write)
//	POST /admin/api/trial/builder-copy  copy a builder for the trial (request_key)
//
// The composition carries up to TrialMaxRules rules, hence the larger limit.
const adminTrialMaxBody = 128 << 10

func (a *adminAPI) trialSvcOK(w http.ResponseWriter) bool {
	if a.trialSvc == nil {
		writeAdminError(w, http.StatusServiceUnavailable, "service_unavailable")
		return false
	}
	return true
}

// routeTrial dispatches /admin/api/trial[/...]; segments[0] == "trial".
func (a *adminAPI) routeTrial(w http.ResponseWriter, r *http.Request, segments []string) {
	switch {
	case len(segments) == 1:
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			a.trialGet(w, r)
		case http.MethodPut:
			a.trialUpdate(w, r)
		default:
			w.Header().Set("Allow", "GET, HEAD, PUT")
			writeAdminError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		}
	case len(segments) == 2 && (segments[1] == "preview" || segments[1] == "builder-copy"):
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			writeAdminError(w, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
		if segments[1] == "preview" {
			a.trialPreview(w, r)
		} else {
			a.trialCopyBuilder(w, r)
		}
	default:
		writeAdminError(w, http.StatusNotFound, "not_found")
	}
}

func (a *adminAPI) trialGet(w http.ResponseWriter, r *http.Request) {
	if !a.trialSvcOK(w) {
		return
	}
	view, err := a.trialSvc.View(r.Context())
	if err != nil {
		a.internal(w, "trial", err)
		return
	}
	writeAdminJSON(w, http.StatusOK, view)
}

// trialDraftBody is the editable trial configuration. Unknown fields are
// rejected (decodeAdminJSON), so a typo is never a silent no-op.
type trialDraftBody struct {
	Enabled          bool                       `json:"enabled"`
	DurationHours    int                        `json:"duration_hours"`
	RateLimitPerHour int                        `json:"rate_limit_per_hour"`
	Title            string                     `json:"title"`
	Description      string                     `json:"description"`
	Features         []string                   `json:"features"`
	Badge            string                     `json:"badge"`
	BuilderID        *uint                      `json:"builder_id"`
	Composition      *database.TrialComposition `json:"composition"`
	// LegacyNodeIDs: issuance nodes of the trial plan; null keeps the links.
	LegacyNodeIDs *[]uint `json:"legacy_node_ids"`
}

func (b trialDraftBody) input() service.TrialInput {
	return service.TrialInput{
		Enabled: b.Enabled, DurationHours: b.DurationHours, RateLimitPerHour: b.RateLimitPerHour,
		Title: b.Title, Description: b.Description, Features: b.Features, Badge: b.Badge,
		BuilderID: b.BuilderID, Composition: b.Composition, LegacyNodeIDs: b.LegacyNodeIDs,
	}
}

type trialUpdateBody struct {
	RequestKey string `json:"request_key"`
	Version    int    `json:"version"`
	trialDraftBody
}

func (a *adminAPI) trialUpdate(w http.ResponseWriter, r *http.Request) {
	if !a.trialSvcOK(w) {
		return
	}
	var body trialUpdateBody
	if !decodeAdminJSONLimit(w, r, &body, adminTrialMaxBody) {
		return
	}
	outcome, err := a.trialSvc.Update(r.Context(), a.actor, body.RequestKey, body.Version, body.input())
	a.writeTrialOutcome(w, "trial/update", outcome, err)
}

func (a *adminAPI) trialPreview(w http.ResponseWriter, r *http.Request) {
	if !a.trialSvcOK(w) {
		return
	}
	var body trialDraftBody
	if !decodeAdminJSONLimit(w, r, &body, adminTrialMaxBody) {
		return
	}
	preview, err := a.trialSvc.Preview(r.Context(), body.input())
	if err != nil {
		a.internal(w, "trial/preview", err)
		return
	}
	writeAdminJSON(w, http.StatusOK, preview)
}

type trialCopyBody struct {
	RequestKey string `json:"request_key"`
	BuilderID  uint   `json:"builder_id"`
}

func (a *adminAPI) trialCopyBuilder(w http.ResponseWriter, r *http.Request) {
	if !a.trialSvcOK(w) {
		return
	}
	var body trialCopyBody
	if !decodeAdminJSONLimit(w, r, &body, adminTariffMaxBody) {
		return
	}
	outcome, err := a.trialSvc.CopyBuilder(r.Context(), a.actor, body.RequestKey, body.BuilderID)
	a.writeTrialOutcome(w, "trial/builder-copy", outcome, err)
}

func (a *adminAPI) writeTrialOutcome(w http.ResponseWriter, route string, outcome *service.TrialOutcome, err error) {
	if err != nil {
		if outcome != nil && outcome.Audit.ID != 0 {
			// Committed and audited, but reloading the editor state failed: a
			// retry with the same key replays.
			a.internal(w, route, err)
			return
		}
		a.writeTrialError(w, route, err)
		return
	}
	writeAdminJSON(w, http.StatusOK, outcome)
}

// writeTrialError maps the trial rejections to stable codes. An invalid draft
// names the field and the reason so the editor can point at it.
func (a *adminAPI) writeTrialError(w http.ResponseWriter, route string, err error) {
	var fieldErr *database.TrialFieldError
	switch {
	case errors.As(err, &fieldErr):
		status, code := http.StatusBadRequest, "invalid_trial"
		if errors.Is(err, database.ErrTrialBuilderShared) {
			status, code = http.StatusConflict, "builder_shared"
		}
		writeAdminJSON(w, status, struct {
			Error  string `json:"error"`
			Field  string `json:"field"`
			Reason string `json:"reason"`
		}{code, fieldErr.Field, fieldErr.Reason})
	case errors.Is(err, database.ErrAdminInvalidRequest):
		writeAdminError(w, http.StatusBadRequest, "invalid_request")
	case errors.Is(err, database.ErrTrialVersionConflict):
		writeAdminError(w, http.StatusConflict, "version_conflict")
	case errors.Is(err, database.ErrBuilderVersionConflict):
		writeAdminError(w, http.StatusConflict, "builder_version_conflict")
	case errors.Is(err, database.ErrBuilderNameTaken):
		writeAdminError(w, http.StatusConflict, "builder_name_taken")
	case errors.Is(err, database.ErrAdminRequestKeyConflict):
		writeAdminError(w, http.StatusConflict, "request_key_conflict")
	default:
		a.internal(w, route, err)
	}
}
