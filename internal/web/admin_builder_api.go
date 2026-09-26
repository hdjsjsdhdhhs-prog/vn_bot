package web

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"

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
		if errors.Is(err, database.ErrProviderSourceNotFound) {
			writeAdminError(w, http.StatusNotFound, "not_found")
			return
		}
		a.internal(w, "builder/source/update", err)
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
	ct, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || ct != "application/json" {
		writeAdminError(w, http.StatusUnsupportedMediaType, "unsupported_media_type")
		return false
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, adminAPIMaxBody))
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
	view, err := a.builderSvc.CreateSource(r.Context(), service.CreateSourceInput{
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
		a.internal(w, "builder/source/create", err)
		return
	}
	writeAdminJSON(w, http.StatusCreated, view)
}

// ---------------------------------------------------------------------------
// Source entries + refresh
// ---------------------------------------------------------------------------

func (a *adminAPI) builderListSourceEntries(w http.ResponseWriter, r *http.Request, sourceID uint) {
	if !a.builderSvcOK(w) {
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

// refreshSourceRequest carries pre-parsed entries from the caller. The admin
// UI is responsible for fetching and parsing the upstream feed; the backend
// only stores the result and updates sync metadata.
type refreshSourceRequest struct {
	// Entries is the parsed catalogue from the upstream feed.
	Entries []refreshEntryInput `json:"entries"`
	// SyncStatus is a short stable code: "ok" | "fetch_error" | "parse_error".
	SyncStatus string `json:"sync_status"`
	// SyncError is a credential-free error description (max 64 chars).
	SyncError string `json:"sync_error"`
}

type refreshEntryInput struct {
	Fingerprint  string `json:"fingerprint"`
	OriginalName string `json:"original_name"`
	Protocol     string `json:"protocol"`
	CountryCode  string `json:"country_code"`
}

func (a *adminAPI) builderRefreshSource(w http.ResponseWriter, r *http.Request, sourceID uint) {
	if !a.builderSvcOK(w) {
		return
	}
	var body refreshSourceRequest
	if !decodeAdminJSON(w, r, &body) {
		return
	}
	entries := make([]database.ProviderSourceEntry, 0, len(body.Entries))
	for _, e := range body.Entries {
		entries = append(entries, database.ProviderSourceEntry{
			Fingerprint:  e.Fingerprint,
			OriginalName: e.OriginalName,
			Protocol:     e.Protocol,
			CountryCode:  e.CountryCode,
		})
	}
	view, err := a.builderSvc.RefreshSource(r.Context(), sourceID, entries, body.SyncStatus, body.SyncError)
	if err != nil {
		if errors.Is(err, database.ErrProviderSourceNotFound) {
			writeAdminError(w, http.StatusNotFound, "not_found")
			return
		}
		a.internal(w, "builder/source/refresh", err)
		return
	}
	writeAdminJSON(w, http.StatusOK, view)
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
