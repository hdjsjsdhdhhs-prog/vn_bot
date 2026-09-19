package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/service"
	"github.com/kereal/rs8kvn_bot/internal/telegramauth"
)

// This private context key can only be populated at the verified HTTP boundary.
// Neither request fields nor subscription bearer tokens are application identity.
type miniAppIdentityKey struct{}

func authorizeMiniApp(ctx context.Context, action service.SubscriptionManagementAction, days int) (int64, error) {
	id, ok := ctx.Value(miniAppIdentityKey{}).(int64)
	if !ok || id <= 0 || action != service.SubscriptionManagementRead || days != 0 {
		return 0, service.ErrSubscriptionAccessDenied
	}
	return id, nil
}

// newMiniAppHandler binds application policies once at startup. Purchase intents
// grant no access: no renewal or payment-confirmation route is exposed, even to
// the configured Telegram administrator.
func newMiniAppHandler(cfg *config.Config, subscriptions *service.SubscriptionService) http.Handler {
	var token string
	if cfg != nil {
		token = cfg.TelegramBotToken
	}
	validator := telegramauth.New(token)
	management := service.NewSubscriptionManagement(subscriptions, authorizeMiniApp)
	purchases := service.NewPurchaseService(subscriptions, authorizeMiniAppPurchase)
	return miniAppAuthentication(validator, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveMiniAppPurchase(w, r, purchases) {
			return
		}
		if r.URL.Path != "/api/miniapp/subscription" {
			writeMiniAppError(w, http.StatusNotFound, "not_found")
			return
		}
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeMiniAppError(w, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		info, err := management.Current(ctx)
		if err != nil {
			switch {
			case errors.Is(err, service.ErrSubscriptionAccessDenied):
				writeMiniAppError(w, http.StatusForbidden, "forbidden")
			case errors.Is(err, database.ErrSubscriptionNotFound):
				writeMiniAppError(w, http.StatusNotFound, "subscription_not_found")
			default:
				writeMiniAppError(w, http.StatusServiceUnavailable, "service_unavailable")
			}
			return
		}
		// Do not log the response (URLs are bearer credentials) or encoder errors
		// supplied by a ResponseWriter. Errors are never reflected to the client.
		_ = json.NewEncoder(w).Encode(info)
	}))
}

// miniAppAuthentication accepts only Authorization: tma <raw initData>.
// Validate every request; no cookies, bearer fallback or sliding session expiry.
func miniAppAuthentication(validator *telegramauth.Validator, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Robots-Tag", "noindex, nofollow")
		auth := r.Header.Values("Authorization")
		if len(auth) != 1 {
			writeMiniAppUnauthorized(w)
			return
		}
		scheme, raw, ok := strings.Cut(auth[0], " ")
		if !ok || !strings.EqualFold(scheme, "tma") {
			writeMiniAppUnauthorized(w)
			return
		}
		id, err := validator.Verify(raw, time.Now())
		if err != nil {
			if errors.Is(err, telegramauth.ErrNotConfigured) {
				writeMiniAppError(w, http.StatusServiceUnavailable, "service_unavailable")
			} else {
				writeMiniAppUnauthorized(w)
			}
			return
		}
		ctx := context.WithValue(r.Context(), miniAppIdentityKey{}, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func writeMiniAppUnauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `tma realm="miniapp"`)
	writeMiniAppError(w, http.StatusUnauthorized, "unauthorized")
}

func writeMiniAppError(w http.ResponseWriter, status int, code string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Error string `json:"error"`
	}{Error: code})
}
