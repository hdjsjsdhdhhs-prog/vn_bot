package web

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/service"
)

// Customer warm-up routes (/admin/api/warmup*). Same session, Origin/CSRF,
// audit/idempotency (request_key) and optimistic-locking (version) contract
// as the trial and tariff editors:
//
//	GET    /admin/api/warmup                         overview
//	GET    /admin/api/warmup/settings                global settings
//	PUT    /admin/api/warmup/settings                save settings (request_key, version)
//	GET    /admin/api/warmup/scenarios               list (category, enabled, trigger, order)
//	POST   /admin/api/warmup/scenarios               create (request_key, key)
//	GET    /admin/api/warmup/scenarios/{id}          one scenario
//	PATCH  /admin/api/warmup/scenarios/{id}          partial edit (request_key, version)
//	POST   /admin/api/warmup/scenarios/{id}/preview  preview stored + unsaved patch (no write)
//	POST   /admin/api/warmup/preview                 preview a full unsaved draft (no write)
//	POST   /admin/api/warmup/images                  upload (request_key, base64 data)
//	GET    /admin/api/warmup/images/{id}             the picture bytes (for <img>)
//	DELETE /admin/api/warmup/images/{id}             delete an unused picture (request_key)
//	GET    /admin/api/warmup/deliveries              delivery journal (filters, paging)
//	GET    /admin/api/warmup/coupons                 issued coupons (filters, paging)
const (
	// adminWarmupMaxBody fits a scenario: a 16 KB message and its buttons.
	adminWarmupMaxBody = 64 << 10
	// adminWarmupImageMaxBody fits a 2 MB picture in base64 (4/3) plus the
	// JSON envelope; the decoded size is checked again by the service.
	adminWarmupImageMaxBody = database.WarmupImageMaxBytes*4/3 + 4096
)

func (a *adminAPI) warmupSvcOK(w http.ResponseWriter) bool {
	if a.warmupSvc == nil {
		writeAdminError(w, http.StatusServiceUnavailable, "service_unavailable")
		return false
	}
	return true
}

func allowMethods(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	writeAdminError(w, http.StatusMethodNotAllowed, "method_not_allowed")
}

// routeWarmup dispatches /admin/api/warmup[/...]; segments[0] == "warmup".
func (a *adminAPI) routeWarmup(w http.ResponseWriter, r *http.Request, segments []string) {
	if !a.warmupSvcOK(w) {
		return
	}
	isGet := r.Method == http.MethodGet || r.Method == http.MethodHead
	switch {
	case len(segments) == 1:
		a.get(w, r, a.warmupOverview)
	case len(segments) == 2 && segments[1] == "settings":
		switch {
		case isGet:
			a.warmupSettings(w, r)
		case r.Method == http.MethodPut:
			a.warmupSaveSettings(w, r)
		default:
			allowMethods(w, "GET, HEAD, PUT")
		}
	case len(segments) == 2 && segments[1] == "scenarios":
		switch {
		case isGet:
			a.warmupScenarios(w, r)
		case r.Method == http.MethodPost:
			a.warmupCreateScenario(w, r)
		default:
			allowMethods(w, "GET, HEAD, POST")
		}
	case len(segments) == 3 && segments[1] == "scenarios":
		id, ok := parseAdminID(segments[2])
		if !ok {
			writeAdminError(w, http.StatusNotFound, "scenario_not_found")
			return
		}
		switch {
		case isGet:
			a.warmupScenario(w, r, id)
		case r.Method == http.MethodPatch:
			a.warmupPatchScenario(w, r, id)
		default:
			allowMethods(w, "GET, HEAD, PATCH")
		}
	case len(segments) == 4 && segments[1] == "scenarios" && segments[3] == "preview":
		id, ok := parseAdminID(segments[2])
		if !ok {
			writeAdminError(w, http.StatusNotFound, "scenario_not_found")
			return
		}
		if r.Method != http.MethodPost {
			allowMethods(w, "POST")
			return
		}
		a.warmupPreviewScenario(w, r, id)
	case len(segments) == 2 && segments[1] == "preview":
		if r.Method != http.MethodPost {
			allowMethods(w, "POST")
			return
		}
		a.warmupPreviewDraft(w, r)
	case len(segments) == 2 && segments[1] == "images":
		if r.Method != http.MethodPost {
			allowMethods(w, "POST")
			return
		}
		a.warmupUploadImage(w, r)
	case len(segments) == 3 && segments[1] == "images":
		id, ok := parseAdminID(segments[2])
		if !ok {
			writeAdminError(w, http.StatusNotFound, "image_not_found")
			return
		}
		switch {
		case isGet:
			a.warmupImage(w, r, id)
		case r.Method == http.MethodDelete:
			a.warmupDeleteImage(w, r, id)
		default:
			allowMethods(w, "GET, HEAD, DELETE")
		}
	case len(segments) == 2 && segments[1] == "deliveries":
		a.get(w, r, a.warmupDeliveries)
	case len(segments) == 2 && segments[1] == "coupons":
		a.get(w, r, a.warmupCoupons)
	default:
		writeAdminError(w, http.StatusNotFound, "not_found")
	}
}

func (a *adminAPI) warmupOverview(w http.ResponseWriter, r *http.Request) {
	overview, err := a.warmupSvc.Overview(r.Context())
	if err != nil {
		a.internal(w, "warmup", err)
		return
	}
	writeAdminJSON(w, http.StatusOK, overview)
}

func (a *adminAPI) warmupSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := a.warmupSvc.Settings(r.Context())
	if err != nil {
		a.internal(w, "warmup/settings", err)
		return
	}
	writeAdminJSON(w, http.StatusOK, settings)
}

type warmupSettingsBody struct {
	service.WarmupSettingsSave

	RequestKey string `json:"request_key"`
}

func (a *adminAPI) warmupSaveSettings(w http.ResponseWriter, r *http.Request) {
	var body warmupSettingsBody
	if !decodeAdminJSON(w, r, &body) {
		return
	}
	outcome, err := a.warmupSvc.SaveSettings(r.Context(), a.actor, body.RequestKey, body.WarmupSettingsSave)
	if err != nil {
		a.writeWarmupError(w, "warmup/settings", err)
		return
	}
	writeAdminJSON(w, http.StatusOK, outcome)
}

func (a *adminAPI) warmupScenarios(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filter := service.WarmupScenarioFilter{Category: q.Get("category"), Trigger: q.Get("trigger"), Order: q.Get("order")}
	if raw := q.Get("enabled"); raw != "" {
		enabled, err := strconv.ParseBool(raw)
		if err != nil {
			writeAdminError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		filter.Enabled = &enabled
	}
	scenarios, err := a.warmupSvc.ListScenariosFiltered(r.Context(), filter)
	if err != nil {
		a.writeWarmupError(w, "warmup/scenarios", err)
		return
	}
	writeAdminJSON(w, http.StatusOK, struct {
		Scenarios []service.WarmupScenarioView `json:"scenarios"`
	}{scenarios})
}

func (a *adminAPI) warmupScenario(w http.ResponseWriter, r *http.Request, id uint) {
	view, err := a.warmupSvc.Scenario(r.Context(), id)
	if err != nil {
		a.writeWarmupError(w, "warmup/scenario", err)
		return
	}
	writeAdminJSON(w, http.StatusOK, view)
}

type warmupPatchBody struct {
	service.WarmupScenarioPatch

	RequestKey string `json:"request_key"`
	Version    int    `json:"version"`
}

func (a *adminAPI) warmupPatchScenario(w http.ResponseWriter, r *http.Request, id uint) {
	var body warmupPatchBody
	if !decodeAdminJSONLimit(w, r, &body, adminWarmupMaxBody) {
		return
	}
	outcome, err := a.warmupSvc.PatchScenario(r.Context(), a.actor, body.RequestKey, id, body.Version, body.WarmupScenarioPatch)
	a.writeWarmupScenarioOutcome(w, "warmup/scenario/patch", outcome, err)
}

type warmupCreateBody struct {
	service.WarmupScenarioInput

	RequestKey string `json:"request_key"`
	Key        string `json:"key"`
}

func (a *adminAPI) warmupCreateScenario(w http.ResponseWriter, r *http.Request) {
	var body warmupCreateBody
	if !decodeAdminJSONLimit(w, r, &body, adminWarmupMaxBody) {
		return
	}
	outcome, err := a.warmupSvc.CreateScenarioOutcome(r.Context(), a.actor, body.RequestKey, body.Key, body.WarmupScenarioInput)
	a.writeWarmupScenarioOutcome(w, "warmup/scenario/create", outcome, err)
}

func (a *adminAPI) writeWarmupScenarioOutcome(w http.ResponseWriter, route string, outcome *service.WarmupScenarioOutcome, err error) {
	if err != nil {
		if outcome != nil && outcome.Audit.ID != 0 {
			// Committed and audited, but reloading failed: a retry replays.
			a.internal(w, route, err)
			return
		}
		a.writeWarmupError(w, route, err)
		return
	}
	writeAdminJSON(w, http.StatusOK, outcome)
}

// warmupPreviewBody previews the stored scenario with an optional unsaved
// patch, for an optional example recipient.
type warmupPreviewBody struct {
	service.WarmupScenarioPatch

	Context service.WarmupPreviewContext `json:"context"`
}

func (a *adminAPI) warmupPreviewScenario(w http.ResponseWriter, r *http.Request, id uint) {
	var body warmupPreviewBody
	if !decodeAdminJSONLimit(w, r, &body, adminWarmupMaxBody) {
		return
	}
	preview, err := a.warmupSvc.PreviewScenario(r.Context(), id, body.WarmupScenarioPatch, body.Context)
	if err != nil {
		a.writeWarmupError(w, "warmup/scenario/preview", err)
		return
	}
	writeAdminJSON(w, http.StatusOK, preview)
}

type warmupDraftPreviewBody struct {
	service.WarmupScenarioInput

	Context service.WarmupPreviewContext `json:"context"`
}

func (a *adminAPI) warmupPreviewDraft(w http.ResponseWriter, r *http.Request) {
	var body warmupDraftPreviewBody
	if !decodeAdminJSONLimit(w, r, &body, adminWarmupMaxBody) {
		return
	}
	preview, err := a.warmupSvc.PreviewWith(r.Context(), body.WarmupScenarioInput, body.Context)
	if err != nil {
		a.writeWarmupError(w, "warmup/preview", err)
		return
	}
	writeAdminJSON(w, http.StatusOK, preview)
}

// warmupImageBody uploads a picture as base64 (JSON keeps the request_key
// contract of every other mutation). The MIME type is sniffed from the bytes.
type warmupImageBody struct {
	RequestKey string `json:"request_key"`
	Data       string `json:"data"`
}

func (a *adminAPI) warmupUploadImage(w http.ResponseWriter, r *http.Request) {
	var body warmupImageBody
	if !decodeAdminJSONLimitCode(w, r, &body, adminWarmupImageMaxBody, "image_too_large") {
		return
	}
	data, err := base64.StdEncoding.DecodeString(body.Data)
	if err != nil {
		writeAdminJSON(w, http.StatusBadRequest, warmupErrorBody{Error: "image_invalid_type", Field: "data", Reason: "base64"})
		return
	}
	outcome, err := a.warmupSvc.UploadImage(r.Context(), a.actor, body.RequestKey, data)
	if err != nil {
		a.writeWarmupError(w, "warmup/images/upload", err)
		return
	}
	writeAdminJSON(w, http.StatusOK, outcome)
}

// warmupImage serves the picture itself for the admin <img>: the sniffed
// MIME type, nosniff, inline, never cached (adminauth sets no-store).
func (a *adminAPI) warmupImage(w http.ResponseWriter, r *http.Request, id uint) {
	img, err := a.warmupSvc.Image(r.Context(), id, true)
	if err != nil {
		a.writeWarmupError(w, "warmup/image", err)
		return
	}
	w.Header().Set("Content-Type", img.Mime)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", "inline")
	w.Header().Set("Content-Length", strconv.Itoa(len(img.Data)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		// #nosec G705 -- binary image, not HTML: UploadImage accepts only bytes
		// sniffed as image/jpeg or image/png (also a DB CHECK on mime),
		// Content-Type is that stored type and nosniff stops the browser from
		// reinterpreting it; the route is behind the admin session.
		_, _ = w.Write(img.Data)
	}
}

type warmupRequestKeyBody struct {
	RequestKey string `json:"request_key"`
}

func (a *adminAPI) warmupDeleteImage(w http.ResponseWriter, r *http.Request, id uint) {
	var body warmupRequestKeyBody
	if !decodeAdminJSON(w, r, &body) {
		return
	}
	outcome, err := a.warmupSvc.DeleteImage(r.Context(), a.actor, body.RequestKey, id)
	if err != nil {
		a.writeWarmupError(w, "warmup/images/delete", err)
		return
	}
	writeAdminJSON(w, http.StatusOK, outcome)
}

func (a *adminAPI) warmupDeliveries(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	scenarioID, ok1 := parseAdminInt(q.Get("scenario_id"), 0)
	limit, ok2 := parseAdminInt(q.Get("limit"), 0)
	offset, ok3 := parseAdminInt(q.Get("offset"), 0)
	telegramID, ok4 := parseAdminOptionalInt64(q.Get("telegram_id"))
	from, ok5 := parseAdminTime(q.Get("from"))
	to, ok6 := parseAdminTime(q.Get("to"))
	if !ok1 || !ok2 || !ok3 || !ok4 || !ok5 || !ok6 || scenarioID < 0 || limit < 0 || offset < 0 || len(q.Get("coupon")) > 64 {
		writeAdminError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	page, err := a.warmupSvc.Deliveries(r.Context(), database.WarmupDeliveryFilter{
		ScenarioID: uint(scenarioID), Status: q.Get("status"), TelegramID: telegramID, From: from, To: to,
		CouponCode: q.Get("coupon"), Category: q.Get("category"), Limit: limit, Offset: offset,
	})
	if err != nil {
		a.writeWarmupError(w, "warmup/deliveries", err)
		return
	}
	writeAdminJSON(w, http.StatusOK, page)
}

func (a *adminAPI) warmupCoupons(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	scenarioID, ok1 := parseAdminInt(q.Get("scenario_id"), 0)
	limit, ok2 := parseAdminInt(q.Get("limit"), 0)
	offset, ok3 := parseAdminInt(q.Get("offset"), 0)
	telegramID, ok4 := parseAdminOptionalInt64(q.Get("telegram_id"))
	if !ok1 || !ok2 || !ok3 || !ok4 || scenarioID < 0 || limit < 0 || offset < 0 || len(q.Get("code")) > 64 {
		writeAdminError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	page, err := a.warmupSvc.Coupons(r.Context(), database.WarmupCouponFilter{
		ScenarioID: uint(scenarioID), TelegramID: telegramID, Code: q.Get("code"), Status: q.Get("status"),
		Limit: limit, Offset: offset,
	})
	if err != nil {
		a.writeWarmupError(w, "warmup/coupons", err)
		return
	}
	writeAdminJSON(w, http.StatusOK, page)
}

// decodeAdminJSONLimitCode is decodeAdminJSONLimit that answers a body over
// the limit with 413 and tooLarge instead of a generic invalid_request.
func decodeAdminJSONLimitCode(w http.ResponseWriter, r *http.Request, dst any, limit int64, tooLarge string) bool {
	if r.ContentLength > limit {
		writeAdminError(w, http.StatusRequestEntityTooLarge, tooLarge)
		return false
	}
	return decodeAdminJSONLimit(w, r, dst, limit)
}

func parseAdminOptionalInt64(raw string) (int64, bool) {
	if raw == "" {
		return 0, true
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || v <= 0 {
		return 0, false
	}
	return v, true
}

// warmupErrorBody is a validation rejection: a stable code plus the field and
// the reason, so the editor can point at the input.
type warmupErrorBody struct {
	Error  string `json:"error"`
	Field  string `json:"field"`
	Reason string `json:"reason"`
	Detail string `json:"detail,omitempty"`
}

// warmupErrorCode maps a service field error to the stable API code.
func warmupErrorCode(fe *service.WarmupFieldError) (int, string) {
	switch {
	case fe.Reason == "duplicates_expiry_reminder":
		return http.StatusConflict, "duplicates_expiry_reminder"
	case fe.Field == "trigger_type" || fe.Field == "timing":
		return http.StatusBadRequest, "invalid_trigger"
	case fe.Field == "image_id" && fe.Reason == "not_found":
		return http.StatusBadRequest, "image_not_found"
	case fe.Field == "image" && fe.Reason == "too_large":
		return http.StatusRequestEntityTooLarge, "image_too_large"
	case fe.Field == "image":
		return http.StatusBadRequest, "image_invalid_type"
	case strings.HasPrefix(fe.Reason, "html_"):
		return http.StatusBadRequest, "invalid_html"
	case strings.HasPrefix(fe.Reason, "variable_") || fe.Reason == "placeholder_malformed" || fe.Reason == "discount_code_required":
		return http.StatusBadRequest, "invalid_variable"
	case fe.Reason == "button_callback":
		return http.StatusBadRequest, "callback_not_allowed"
	case fe.Field == "buttons":
		return http.StatusBadRequest, "invalid_button"
	default:
		return http.StatusBadRequest, "validation_error"
	}
}

// writeWarmupError maps the warm-up rejections to stable codes.
func (a *adminAPI) writeWarmupError(w http.ResponseWriter, route string, err error) {
	var fieldErr *service.WarmupFieldError
	switch {
	case errors.As(err, &fieldErr):
		status, code := warmupErrorCode(fieldErr)
		writeAdminJSON(w, status, warmupErrorBody{Error: code, Field: fieldErr.Field, Reason: fieldErr.Reason, Detail: fieldErr.Detail})
	case errors.Is(err, database.ErrWarmupScenarioNotFound):
		writeAdminError(w, http.StatusNotFound, "scenario_not_found")
	case errors.Is(err, database.ErrWarmupImageNotFound):
		writeAdminError(w, http.StatusNotFound, "image_not_found")
	case errors.Is(err, database.ErrWarmupImageInUse):
		writeAdminError(w, http.StatusConflict, "image_in_use")
	case errors.Is(err, database.ErrWarmupVersionConflict):
		writeAdminError(w, http.StatusConflict, "version_conflict")
	case errors.Is(err, database.ErrWarmupScenarioKeyTaken):
		writeAdminError(w, http.StatusConflict, "scenario_key_taken")
	case errors.Is(err, database.ErrAdminInvalidRequest):
		writeAdminError(w, http.StatusBadRequest, "invalid_request")
	case errors.Is(err, database.ErrAdminRequestKeyConflict):
		writeAdminError(w, http.StatusConflict, "request_key_conflict")
	default:
		a.internal(w, route, err)
	}
}
