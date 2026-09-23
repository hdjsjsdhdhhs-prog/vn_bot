package web

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/service"

	"go.uber.org/zap"
)

const adminAPIMaxBody = 4096

// adminAPI serves the browser-admin JSON API behind adminauth.Middleware. It
// is installed as the protected handler of adminauth.Routes, so every request
// here already carries an authenticated session (and CSRF for unsafe methods).
//
// Routing is exact-match on the decoded path on purpose: ServeMux would
// redirect noncanonical paths (double slashes, dot segments), and the admin
// namespace must never emit redirects. Unknown routes answer 404 JSON.
type adminAPI struct {
	svc   *service.AdminService
	actor string
}

func newAdminAPI(svc *service.AdminService, actor string) http.Handler {
	return &adminAPI{svc: svc, actor: actor}
}

func (a *adminAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	const prefix = "/admin/api/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		writeAdminError(w, http.StatusNotFound, "not_found")
		return
	}
	if a.svc == nil {
		writeAdminError(w, http.StatusServiceUnavailable, "service_unavailable")
		return
	}
	segments := strings.Split(strings.TrimPrefix(r.URL.Path, prefix), "/")
	switch {
	case len(segments) == 1 && segments[0] == "dashboard":
		a.get(w, r, a.dashboard)
	case len(segments) == 1 && segments[0] == "users":
		a.get(w, r, a.users)
	case len(segments) == 2 && segments[0] == "users":
		telegramID, ok := parseAdminTelegramID(segments[1])
		if !ok {
			writeAdminError(w, http.StatusNotFound, "not_found")
			return
		}
		a.get(w, r, func(w http.ResponseWriter, r *http.Request) { a.user(w, r, telegramID) })
	case len(segments) == 1 && segments[0] == "audit":
		a.get(w, r, a.audit)
	case len(segments) == 2 && segments[0] == "subscriptions":
		id, ok := parseAdminID(segments[1])
		if !ok {
			writeAdminError(w, http.StatusNotFound, "not_found")
			return
		}
		a.get(w, r, func(w http.ResponseWriter, r *http.Request) { a.subscription(w, r, id) })
	case len(segments) == 3 && segments[0] == "subscriptions":
		id, ok := parseAdminID(segments[1])
		action, known := adminActionRoutes[segments[2]]
		if !ok || !known {
			writeAdminError(w, http.StatusNotFound, "not_found")
			return
		}
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			writeAdminError(w, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
		a.mutate(w, r, id, action)
	default:
		writeAdminError(w, http.StatusNotFound, "not_found")
	}
}

var adminActionRoutes = map[string]database.AdminAction{
	"renew":   database.AdminActionRenew,
	"disable": database.AdminActionDisable,
	"enable":  database.AdminActionEnable,
	"expiry":  database.AdminActionChangeExpiry,
}

func (a *adminAPI) get(w http.ResponseWriter, r *http.Request, handler http.HandlerFunc) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeAdminError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	handler(w, r)
}

func (a *adminAPI) dashboard(w http.ResponseWriter, r *http.Request) {
	dashboard, err := a.svc.Dashboard(r.Context())
	if err != nil {
		a.internal(w, "dashboard", err)
		return
	}
	writeAdminJSON(w, http.StatusOK, dashboard)
}

func (a *adminAPI) users(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, ok1 := parseAdminInt(q.Get("limit"), 0)
	offset, ok2 := parseAdminInt(q.Get("offset"), 0)
	if !ok1 || !ok2 || limit < 0 || offset < 0 || len(q.Get("q")) > 128 {
		writeAdminError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	page, err := a.svc.ListUsers(r.Context(), database.AdminUserFilter{Query: q.Get("q"), Status: q.Get("status"), Limit: limit, Offset: offset})
	if err != nil {
		if errors.Is(err, database.ErrAdminInvalidRequest) {
			writeAdminError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		a.internal(w, "users", err)
		return
	}
	writeAdminJSON(w, http.StatusOK, page)
}

// user resolves a linked customer by Telegram ID and answers with the same
// page as the subscription route. The service has no dedicated lookup: a
// numeric users query matches telegram_id OR id exactly (at most two rows), so
// the match is narrowed to the Telegram ID here — a database ID that happens
// to equal the requested number must not be served as that user.
func (a *adminAPI) user(w http.ResponseWriter, r *http.Request, telegramID int64) {
	page, err := a.svc.ListUsers(r.Context(), database.AdminUserFilter{Query: strconv.FormatInt(telegramID, 10), Limit: 2})
	if err != nil {
		a.internal(w, "user", err)
		return
	}
	for _, u := range page.Users {
		if u.TelegramID == telegramID {
			a.subscription(w, r, u.ID)
			return
		}
	}
	writeAdminError(w, http.StatusNotFound, "not_found")
}

func (a *adminAPI) subscription(w http.ResponseWriter, r *http.Request, id uint) {
	page, err := a.svc.GetSubscription(r.Context(), id)
	if err != nil {
		if errors.Is(err, database.ErrSubscriptionNotFound) {
			writeAdminError(w, http.StatusNotFound, "not_found")
			return
		}
		a.internal(w, "subscription", err)
		return
	}
	writeAdminJSON(w, http.StatusOK, page)
}

func (a *adminAPI) audit(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	subscriptionID, ok1 := parseAdminInt(q.Get("subscription_id"), 0)
	beforeID, ok2 := parseAdminInt(q.Get("before_id"), 0)
	limit, ok3 := parseAdminInt(q.Get("limit"), 0)
	if !ok1 || !ok2 || !ok3 || subscriptionID < 0 || beforeID < 0 || limit < 0 {
		writeAdminError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	entries, err := a.svc.ListAudit(r.Context(), database.AdminAuditFilter{SubscriptionID: uint(subscriptionID), BeforeID: uint(beforeID), Limit: limit})
	if err != nil {
		a.internal(w, "audit", err)
		return
	}
	writeAdminJSON(w, http.StatusOK, struct {
		Entries []database.AdminAuditLog `json:"entries"`
	}{entries})
}

// adminMutationRequest is the body of every mutation. Unknown fields are
// rejected so a typo cannot silently turn into a no-op.
type adminMutationRequest struct {
	RequestKey string `json:"request_key"`
	Days       int    `json:"days,omitempty"`
	ExpiresAt  string `json:"expires_at,omitempty"`
}

func (a *adminAPI) mutate(w http.ResponseWriter, r *http.Request, id uint, action database.AdminAction) {
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		writeAdminError(w, http.StatusUnsupportedMediaType, "unsupported_media_type")
		return
	}
	defer r.Body.Close()
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, adminAPIMaxBody))
	decoder.DisallowUnknownFields()
	var body adminMutationRequest
	if err := decoder.Decode(&body); err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if err := decoder.Decode(new(json.RawMessage)); err != io.EOF {
		writeAdminError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	mutation := database.AdminMutation{Actor: a.actor, RequestKey: body.RequestKey, Action: action, SubscriptionID: id, Days: body.Days}
	if action == database.AdminActionChangeExpiry {
		expiry, err := time.Parse(time.RFC3339, body.ExpiresAt)
		if err != nil {
			writeAdminError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		expiry = expiry.UTC()
		mutation.ExpiresAt = &expiry
	}
	if err := mutation.Validate(); err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid_request")
		return
	}

	outcome, err := a.svc.Mutate(r.Context(), mutation)
	switch {
	case err == nil:
		writeAdminJSON(w, http.StatusOK, outcome)
	case outcome == nil:
		switch {
		case errors.Is(err, database.ErrAdminInvalidRequest):
			writeAdminError(w, http.StatusBadRequest, "invalid_request")
		case errors.Is(err, database.ErrSubscriptionNotFound):
			writeAdminError(w, http.StatusNotFound, "not_found")
		case errors.Is(err, database.ErrAdminRequestKeyConflict):
			writeAdminError(w, http.StatusConflict, "request_key_conflict")
		default:
			a.internal(w, string(action), err)
		}
	default:
		// The mutation (or its rejection) is committed and audited; report the
		// outcome alongside the error so the operator sees the recorded state.
		code := database.AdminErrorCode(err)
		status := http.StatusConflict
		if code == "" {
			// Post-commit node-sync setup failed: the row changed, sync did not.
			code, status = "sync_failed", http.StatusInternalServerError
			logger.Error("admin mutation committed but node sync setup failed",
				zap.String("action", string(action)),
				zap.Uint("subscription_id", id),
				zap.Error(err))
		}
		writeAdminJSON(w, status, struct {
			Error string `json:"error"`
			*service.AdminMutationOutcome
		}{code, outcome})
	}
}

func (a *adminAPI) internal(w http.ResponseWriter, route string, err error) {
	logger.Error("admin API failure", zap.String("route", route), zap.Error(err))
	writeAdminError(w, http.StatusInternalServerError, "internal_error")
}

// parseAdminID accepts a positive database ID in canonical decimal form only:
// no sign, no leading zeros. Aliases such as /subscriptions/007 must answer
// 404 rather than resolve to the same resource.
func parseAdminID(raw string) (uint, bool) {
	id, err := strconv.ParseUint(raw, 10, 32)
	if err != nil || id == 0 || strconv.FormatUint(id, 10) != raw {
		return 0, false
	}
	return uint(id), true
}

// parseAdminTelegramID accepts only linked customers: positive int64 in
// canonical form (no sign, no leading zeros). Trials (negative IDs) are not
// users.
func parseAdminTelegramID(raw string) (int64, bool) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != raw {
		return 0, false
	}
	return id, true
}

func parseAdminInt(raw string, def int) (int, bool) {
	if raw == "" {
		return def, true
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false
	}
	return value, true
}

func writeAdminJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeAdminError(w http.ResponseWriter, status int, code string) {
	writeAdminJSON(w, status, struct {
		Error string `json:"error"`
	}{code})
}
