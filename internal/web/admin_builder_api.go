package web

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/service"
)

// builderSvcOK returns false and writes 503 when the builder service is not wired.
func (a *adminAPI) builderSvcOK(w http.ResponseWriter) bool {
	if a.builderSvc == nil {
		writeAdminError(w, http.StatusServiceUnavailable, "service_unavailable")
		return false
	}
	return true
}

// ---------------------------------------------------------------------------
// Sources
// ---------------------------------------------------------------------------

func (a *adminAPI) builderListSources(w http.ResponseWriter, r *http.Request) {
	if !a.builderSvcOK(w) {
		return
	}
	views, err := a.builderSvc.ListSources(r.Context())
	if err != nil {
		a.internal(w, "builder/sources", err)
		return
	}
	writeAdminJSON(w, http.StatusOK, struct {
		Sources []service.SourceView `json:"sources"`
	}{views})
}

func (a *adminAPI) builderGetSource(w http.ResponseWriter, r *http.Request, id uint) {
	if !a.builderSvcOK(w) {
		return
	}
	view, err := a.builderSvc.GetSource(r.Context(), id)
	if err != nil {
		if errors.Is(err, database.ErrProviderSourceNotFound) {
			writeAdminError(w, http.StatusNotFound, "not_found")
			return
		}
		a.internal(w, "builder/source", err)
		return
	}
	writeAdminJSON(w, http.StatusOK, view)
}

type updateSourceRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Type        string `json:"type"`
}

func (a *adminAPI) builderUpdateSource(w http.ResponseWriter, r *http.Request, id uint) {
	if !a.builderSvcOK(w) {
		return
	}
	var body updateSourceRequest
	if !decodeAdminJSON(w, r, &body) {
		return
	}
	view, err := a.builderSvc.UpdateSource(r.Context(), id, service.UpdateSourceInput{
		Name:        body.Name,
		Description: body.Description,
		Type:        body.Type,
	})
	if err != nil {
		a.writeSourceError(w, "builder/source/update", err)
		return
	}
	writeAdminJSON(w, http.StatusOK, view)
}

func (a *adminAPI) builderSetSourceEnabled(w http.ResponseWriter, r *http.Request, id uint, enabled bool) {
	if !a.builderSvcOK(w) {
		return
	}
	view, err := a.builderSvc.SetSourceEnabled(r.Context(), id, enabled)
	if err != nil {
		if errors.Is(err, database.ErrProviderSourceNotFound) {
			writeAdminError(w, http.StatusNotFound, "not_found")
			return
		}
		a.internal(w, "builder/source/enable", err)
		return
	}
	writeAdminJSON(w, http.StatusOK, view)
}

// ---------------------------------------------------------------------------
// Builders
// ---------------------------------------------------------------------------

func (a *adminAPI) builderList(w http.ResponseWriter, r *http.Request) {
	if !a.builderSvcOK(w) {
		return
	}
	views, err := a.builderSvc.ListBuilders(r.Context())
	if err != nil {
		a.internal(w, "builder/list", err)
		return
	}
	writeAdminJSON(w, http.StatusOK, struct {
		Builders []service.BuilderView `json:"builders"`
	}{views})
}

func (a *adminAPI) builderGet(w http.ResponseWriter, r *http.Request, id uint) {
	if !a.builderSvcOK(w) {
		return
	}
	view, err := a.builderSvc.GetBuilder(r.Context(), id)
	if err != nil {
		if errors.Is(err, database.ErrBuilderNotFound) {
			writeAdminError(w, http.StatusNotFound, "not_found")
			return
		}
		a.internal(w, "builder/get", err)
		return
	}
	writeAdminJSON(w, http.StatusOK, view)
}

type createBuilderRequest struct {
	RequestKey   string `json:"request_key"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	Enabled      bool   `json:"enabled"`
	ProfileTitle string `json:"profile_title"`
	SupportURL   string `json:"support_url"`
	Announce     string `json:"announce"`
}

func (a *adminAPI) builderCreate(w http.ResponseWriter, r *http.Request) {
	if !a.builderSvcOK(w) {
		return
	}
	var body createBuilderRequest
	if !decodeAdminJSON(w, r, &body) {
		return
	}
	view, audit, err := a.builderSvc.CreateBuilder(r.Context(), service.CreateBuilderInput{
		Actor:        a.actor,
		RequestKey:   body.RequestKey,
		Name:         body.Name,
		Description:  body.Description,
		Enabled:      body.Enabled,
		ProfileTitle: body.ProfileTitle,
		SupportURL:   body.SupportURL,
		Announce:     body.Announce,
	})
	if err != nil {
		switch {
		case errors.Is(err, database.ErrBuilderNameTaken):
			writeAdminError(w, http.StatusConflict, "name_taken")
		case errors.Is(err, database.ErrAdminInvalidRequest):
			writeAdminError(w, http.StatusBadRequest, "invalid_request")
		case errors.Is(err, database.ErrAdminRequestKeyConflict):
			writeAdminError(w, http.StatusConflict, "request_key_conflict")
		default:
			a.internal(w, "builder/create", err)
		}
		return
	}
	writeAdminJSON(w, http.StatusCreated, struct {
		Builder *service.BuilderView    `json:"builder"`
		Audit   *database.AdminAuditLog `json:"audit"`
	}{view, audit})
}

type updateBuilderRequest struct {
	RequestKey   string `json:"request_key"`
	Version      int    `json:"version"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	Enabled      bool   `json:"enabled"`
	ProfileTitle string `json:"profile_title"`
	SupportURL   string `json:"support_url"`
	Announce     string `json:"announce"`
}

func (a *adminAPI) builderUpdate(w http.ResponseWriter, r *http.Request, id uint) {
	if !a.builderSvcOK(w) {
		return
	}
	var body updateBuilderRequest
	if !decodeAdminJSON(w, r, &body) {
		return
	}
	view, audit, err := a.builderSvc.UpdateBuilder(r.Context(), service.UpdateBuilderInput{
		Actor:        a.actor,
		RequestKey:   body.RequestKey,
		ID:           id,
		Version:      body.Version,
		Name:         body.Name,
		Description:  body.Description,
		Enabled:      body.Enabled,
		ProfileTitle: body.ProfileTitle,
		SupportURL:   body.SupportURL,
		Announce:     body.Announce,
	})
	if err != nil {
		switch {
		case errors.Is(err, database.ErrBuilderNotFound):
			writeAdminError(w, http.StatusNotFound, "not_found")
		case errors.Is(err, database.ErrBuilderVersionConflict):
			writeAdminError(w, http.StatusConflict, "version_conflict")
		case errors.Is(err, database.ErrBuilderNameTaken):
			writeAdminError(w, http.StatusConflict, "name_taken")
		case errors.Is(err, database.ErrAdminInvalidRequest):
			writeAdminError(w, http.StatusBadRequest, "invalid_request")
		case errors.Is(err, database.ErrAdminRequestKeyConflict):
			writeAdminError(w, http.StatusConflict, "request_key_conflict")
		default:
			a.internal(w, "builder/update", err)
		}
		return
	}
	writeAdminJSON(w, http.StatusOK, struct {
		Builder *service.BuilderView    `json:"builder"`
		Audit   *database.AdminAuditLog `json:"audit"`
	}{view, audit})
}

type setBuilderSourcesRequest struct {
	SourceIDs []uint `json:"source_ids"`
}

func (a *adminAPI) builderSetSources(w http.ResponseWriter, r *http.Request, builderID uint) {
	if !a.builderSvcOK(w) {
		return
	}
	var body setBuilderSourcesRequest
	if !decodeAdminJSON(w, r, &body) {
		return
	}
	if err := a.builderSvc.SetBuilderSources(r.Context(), builderID, body.SourceIDs); err != nil {
		if errors.Is(err, database.ErrBuilderNotFound) {
			writeAdminError(w, http.StatusNotFound, "not_found")
			return
		}
		a.internal(w, "builder/sources/set", err)
		return
	}
	// Return the updated builder.
	view, err := a.builderSvc.GetBuilder(r.Context(), builderID)
	if err != nil {
		a.internal(w, "builder/sources/reload", err)
		return
	}
	writeAdminJSON(w, http.StatusOK, view)
}

type upsertBuilderItemRequest struct {
	ID           uint    `json:"id,omitempty"`
	Kind         string  `json:"kind"`
	SourceID     uint    `json:"source_id"`
	CountryCode  string  `json:"country_code,omitempty"`
	Fingerprint  string  `json:"fingerprint,omitempty"`
	OriginalName string  `json:"original_name,omitempty"`
	CustomName   *string `json:"custom_name,omitempty"`
	Description  string  `json:"description,omitempty"`
	Position     int     `json:"position"`
	Enabled      bool    `json:"enabled"`
}

func (a *adminAPI) builderUpsertItem(w http.ResponseWriter, r *http.Request, builderID uint) {
	if !a.builderSvcOK(w) {
		return
	}
	var body upsertBuilderItemRequest
	if !decodeAdminJSON(w, r, &body) {
		return
	}
	item, err := a.builderSvc.UpsertBuilderItem(r.Context(), service.UpsertBuilderItemInput{
		ID:           body.ID,
		BuilderID:    builderID,
		Kind:         body.Kind,
		SourceID:     body.SourceID,
		CountryCode:  body.CountryCode,
		Fingerprint:  body.Fingerprint,
		OriginalName: body.OriginalName,
		CustomName:   body.CustomName,
		Description:  body.Description,
		Position:     body.Position,
		Enabled:      body.Enabled,
	})
	if err != nil {
		if errors.Is(err, database.ErrProviderSourceInvalid) {
			writeAdminError(w, http.StatusBadRequest, "invalid_kind")
			return
		}
		a.internal(w, "builder/item/upsert", err)
		return
	}
	writeAdminJSON(w, http.StatusOK, item)
}

func (a *adminAPI) builderDeleteItem(w http.ResponseWriter, r *http.Request, itemID uint) {
	if !a.builderSvcOK(w) {
		return
	}
	if err := a.builderSvc.DeleteBuilderItem(r.Context(), itemID); err != nil {
		if errors.Is(err, database.ErrBuilderNotFound) {
			writeAdminError(w, http.StatusNotFound, "not_found")
			return
		}
		a.internal(w, "builder/item/delete", err)
		return
	}
	writeAdminJSON(w, http.StatusOK, struct {
		Deleted uint `json:"deleted"`
	}{itemID})
}

type reorderBuilderItemsRequest struct {
	ItemIDs []uint `json:"item_ids"`
}

func (a *adminAPI) builderReorderItems(w http.ResponseWriter, r *http.Request, builderID uint) {
	if !a.builderSvcOK(w) {
		return
	}
	var body reorderBuilderItemsRequest
	if !decodeAdminJSON(w, r, &body) {
		return
	}
	if err := a.builderSvc.ReorderBuilderItems(r.Context(), builderID, body.ItemIDs); err != nil {
		a.internal(w, "builder/item/reorder", err)
		return
	}
	writeAdminJSON(w, http.StatusOK, struct {
		OK bool `json:"ok"`
	}{true})
}

// ---------------------------------------------------------------------------
// JSON decode helper (shared with admin_api.go pattern)
// ---------------------------------------------------------------------------

// decodeAdminJSON reads and decodes a JSON body, rejecting unknown fields and
// extra trailing content. Returns false and writes the error response on failure.
func decodeAdminJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	return decodeAdminJSONLimit(w, r, dst, adminAPIMaxBody)
}

// decodeAdminJSONLimit is decodeAdminJSON with an explicit body limit.
func decodeAdminJSONLimit(w http.ResponseWriter, r *http.Request, dst any, limit int64) bool {
	ct, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || ct != "application/json" {
		writeAdminError(w, http.StatusUnsupportedMediaType, "unsupported_media_type")
		return false
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid_request")
		return false
	}
	if err := dec.Decode(new(json.RawMessage)); err != io.EOF {
		writeAdminError(w, http.StatusBadRequest, "invalid_request")
		return false
	}
	return true
}

// ---------------------------------------------------------------------------
// Source creation
// ---------------------------------------------------------------------------

type createSourceRequest struct {
	Name            string `json:"name"`
	Description     string `json:"description"`
	Type            string `json:"type"`
	SubscriptionURL string `json:"subscription_url"`
	HWID            string `json:"hwid"`
	UserAgent       string `json:"user_agent"`
	Headers         string `json:"headers"`
	Enabled         bool   `json:"enabled"`
}

func (a *adminAPI) builderCreateSource(w http.ResponseWriter, r *http.Request) {
	if !a.builderSvcOK(w) {
		return
	}
	var body createSourceRequest
	if !decodeAdminJSON(w, r, &body) {
		return
	}
	view, sync, err := a.builderSvc.CreateSource(r.Context(), service.CreateSourceInput{
		Name:            body.Name,
		Description:     body.Description,
		Type:            body.Type,
		SubscriptionURL: body.SubscriptionURL,
		HWID:            body.HWID,
		UserAgent:       body.UserAgent,
		Headers:         body.Headers,
		Enabled:         body.Enabled,
	})
	if err != nil {
		a.writeSourceError(w, "builder/source/create", err)
		return
	}
	// sync is the initial catalogue sync (null only when it could not run).
	writeAdminJSON(w, http.StatusCreated, struct {
		Source *service.SourceView       `json:"source"`
		Sync   *service.SourceSyncResult `json:"sync"`
	}{view, sync})
}

// ---------------------------------------------------------------------------
// Source entries + refresh
// ---------------------------------------------------------------------------

func (a *adminAPI) builderListSourceEntries(w http.ResponseWriter, r *http.Request, sourceID uint) {
	if !a.builderSvcOK(w) {
		return
	}
	// ?all=1 lists every entry including those that disappeared upstream.
	if r.URL.Query().Get("all") == "1" {
		entries, err := a.builderSvc.ListAllSourceEntries(r.Context(), sourceID)
		if err != nil {
			a.writeSourceError(w, "builder/source/entries", err)
			return
		}
		if entries == nil {
			entries = []database.ProviderSourceEntry{}
		}
		writeAdminJSON(w, http.StatusOK, struct {
			SourceID uint                           `json:"source_id"`
			Entries  []database.ProviderSourceEntry `json:"entries"`
		}{sourceID, entries})
		return
	}
	// Optional ?country= filter; "*" = all countries.
	country := r.URL.Query().Get("country")
	if country == "" {
		country = "*"
	}
	entries, err := a.builderSvc.ListSourceEntries(r.Context(), sourceID, country)
	if err != nil {
		if errors.Is(err, database.ErrProviderSourceNotFound) {
			writeAdminError(w, http.StatusNotFound, "not_found")
			return
		}
		a.internal(w, "builder/source/entries", err)
		return
	}
	writeAdminJSON(w, http.StatusOK, struct {
		SourceID uint                           `json:"source_id"`
		Entries  []database.ProviderSourceEntry `json:"entries"`
	}{sourceID, entries})
}

// builderRefreshSource runs a server-side catalogue sync: the backend fetches
// the upstream URL, parses it and stores the catalogue. The caller never
// supplies entries; the body must be empty or "{}". A failed fetch/parse is a
// committed outcome (status=error, previous catalogue kept) and answers 200.
func (a *adminAPI) builderRefreshSource(w http.ResponseWriter, r *http.Request, sourceID uint) {
	if !a.builderSvcOK(w) {
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64))
	if err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if body := strings.TrimSpace(string(raw)); body != "" && body != "{}" {
		writeAdminError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	result, err := a.builderSvc.SyncSource(r.Context(), sourceID)
	if err != nil {
		a.writeSourceError(w, "builder/source/refresh", err)
		return
	}
	writeAdminJSON(w, http.StatusOK, result)
}

// writeSourceError maps source create/update/sync failures to stable codes.
func (a *adminAPI) writeSourceError(w http.ResponseWriter, route string, err error) {
	var fieldErr *service.SourceFieldError
	switch {
	case errors.As(err, &fieldErr):
		writeAdminJSON(w, http.StatusBadRequest, struct {
			Error string `json:"error"`
			Field string `json:"field"`
		}{"invalid_source", fieldErr.Field})
	case errors.Is(err, database.ErrProviderSourceNotFound):
		writeAdminError(w, http.StatusNotFound, "not_found")
	case errors.Is(err, database.ErrProviderSourceSyncInProgress):
		writeAdminError(w, http.StatusConflict, "sync_in_progress")
	case errors.Is(err, service.ErrCatalogueFetcherUnavailable):
		writeAdminError(w, http.StatusServiceUnavailable, "service_unavailable")
	default:
		a.internal(w, route, err)
	}
}

// ---------------------------------------------------------------------------
// Builder enable / disable
// ---------------------------------------------------------------------------

func (a *adminAPI) builderSetEnabled(w http.ResponseWriter, r *http.Request, id uint, enabled bool) {
	if !a.builderSvcOK(w) {
		return
	}
	var view *service.BuilderView
	var err error
	if enabled {
		view, err = a.builderSvc.EnableBuilder(r.Context(), id)
	} else {
		view, err = a.builderSvc.DisableBuilder(r.Context(), id)
	}
	if err != nil {
		if errors.Is(err, database.ErrBuilderNotFound) {
			writeAdminError(w, http.StatusNotFound, "not_found")
			return
		}
		a.internal(w, "builder/enable", err)
		return
	}
	writeAdminJSON(w, http.StatusOK, view)
}

// ---------------------------------------------------------------------------
// Preview
// ---------------------------------------------------------------------------

func (a *adminAPI) builderPreview(w http.ResponseWriter, r *http.Request, id uint) {
	if !a.builderSvcOK(w) {
		return
	}
	result, err := a.builderSvc.PreviewBuilder(r.Context(), id)
	if err != nil {
		if errors.Is(err, database.ErrBuilderNotFound) {
			writeAdminError(w, http.StatusNotFound, "not_found")
			return
		}
		a.internal(w, "builder/preview", err)
		return
	}
	writeAdminJSON(w, http.StatusOK, result)
}

// ---------------------------------------------------------------------------
// Plan / Subscription builder assignment
// ---------------------------------------------------------------------------

type setPlanBuilderRequest struct {
	RequestKey string `json:"request_key"`
	// BuilderID is nil/omitted to clear the assignment.
	BuilderID *uint `json:"builder_id"`
}

func (a *adminAPI) builderSetPlanBuilder(w http.ResponseWriter, r *http.Request, planID uint) {
	if !a.builderSvcOK(w) {
		return
	}
	var body setPlanBuilderRequest
	if !decodeAdminJSON(w, r, &body) {
		return
	}
	audit, err := a.builderSvc.SetPlanBuilder(r.Context(), service.SetPlanBuilderInput{
		Actor:      a.actor,
		RequestKey: body.RequestKey,
		PlanID:     planID,
		BuilderID:  body.BuilderID,
	})
	if err != nil {
		switch {
		case errors.Is(err, database.ErrBuilderNotFound):
			writeAdminError(w, http.StatusNotFound, "not_found")
		case errors.Is(err, database.ErrAdminInvalidRequest):
			writeAdminError(w, http.StatusBadRequest, "invalid_request")
		case errors.Is(err, database.ErrAdminRequestKeyConflict):
			writeAdminError(w, http.StatusConflict, "request_key_conflict")
		default:
			a.internal(w, "builder/plan/set", err)
		}
		return
	}
	writeAdminJSON(w, http.StatusOK, struct {
		Audit *database.AdminAuditLog `json:"audit"`
	}{audit})
}

type setSubscriptionBuilderRequest struct {
	RequestKey string `json:"request_key"`
	// BuilderID is nil/omitted to clear the override (fall back to plan default).
	BuilderID *uint `json:"builder_id"`
}

func (a *adminAPI) builderSetSubscriptionBuilder(w http.ResponseWriter, r *http.Request, subscriptionID uint) {
	if !a.builderSvcOK(w) {
		return
	}
	var body setSubscriptionBuilderRequest
	if !decodeAdminJSON(w, r, &body) {
		return
	}
	audit, err := a.builderSvc.SetSubscriptionBuilder(r.Context(), service.SetSubscriptionBuilderInput{
		Actor:          a.actor,
		RequestKey:     body.RequestKey,
		SubscriptionID: subscriptionID,
		BuilderID:      body.BuilderID,
	})
	if err != nil {
		switch {
		case errors.Is(err, database.ErrBuilderNotFound):
			writeAdminError(w, http.StatusNotFound, "not_found")
		case errors.Is(err, database.ErrSubscriptionNotFound):
			writeAdminError(w, http.StatusNotFound, "not_found")
		case errors.Is(err, database.ErrAdminInvalidRequest):
			writeAdminError(w, http.StatusBadRequest, "invalid_request")
		case errors.Is(err, database.ErrAdminRequestKeyConflict):
			writeAdminError(w, http.StatusConflict, "request_key_conflict")
		default:
			a.internal(w, "builder/subscription/set", err)
		}
		return
	}
	writeAdminJSON(w, http.StatusOK, struct {
		Audit *database.AdminAuditLog `json:"audit"`
	}{audit})
}
