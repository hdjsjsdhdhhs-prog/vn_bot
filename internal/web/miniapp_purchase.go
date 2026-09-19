package web

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/service"
)

func authorizeMiniAppPurchase(ctx context.Context) (int64, error) {
	id, ok := ctx.Value(miniAppIdentityKey{}).(int64)
	if !ok || id <= 0 {
		return 0, service.ErrPurchaseAccessDenied
	}
	return id, nil
}

// Called only behind miniAppAuthentication. This adapter decodes transport
// input and maps application errors; no product/ownership/lifecycle rules here.
func serveMiniAppPurchase(w http.ResponseWriter, r *http.Request, purchases *service.PurchaseService) bool {
	const orderPrefix = "/api/miniapp/orders/"
	path := r.URL.Path
	method := http.MethodGet
	switch {
	case path == "/api/miniapp/offers":
	case path == "/api/miniapp/orders":
		method = http.MethodPost
	case strings.HasPrefix(path, orderPrefix):
	default:
		return false
	}
	if r.Method != method {
		w.Header().Set("Allow", method)
		writeMiniAppError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return true
	}
	// These routes accept no query inputs, including unsigned identity/price.
	if r.URL.RawQuery != "" {
		writeMiniAppError(w, http.StatusBadRequest, "invalid_request")
		return true
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	switch {
	case path == "/api/miniapp/orders/recent":
		orders, err := purchases.Recent(ctx)
		if err != nil {
			writeMiniAppPurchaseError(w, err)
			return true
		}
		_ = json.NewEncoder(w).Encode(struct { Orders []service.PurchaseOrderInfo `json:"orders"` }{orders})
	case path == "/api/miniapp/offers":
		offers, err := purchases.Offers(ctx)
		if err != nil {
			writeMiniAppPurchaseError(w, err)
			return true
		}
		_ = json.NewEncoder(w).Encode(struct { Offers []service.SubscriptionOffer `json:"offers"` }{offers})
	case path == "/api/miniapp/orders":
		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" {
			writeMiniAppError(w, http.StatusUnsupportedMediaType, "unsupported_media_type")
			return true
		}
		keys := r.Header.Values("Idempotency-Key")
		if len(keys) != 1 {
			writeMiniAppError(w, http.StatusBadRequest, "invalid_request")
			return true
		}
		offer, err := decodePurchaseOffer(w, r)
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				writeMiniAppError(w, http.StatusRequestEntityTooLarge, "request_too_large")
			} else {
				writeMiniAppError(w, http.StatusBadRequest, "invalid_request")
			}
			return true
		}
		order, created, err := purchases.Create(ctx, offer, keys[0])
		if err != nil {
			writeMiniAppPurchaseError(w, err)
			return true
		}
		w.Header().Set("Location", orderPrefix+order.OrderID)
		if created {
			w.WriteHeader(http.StatusCreated)
		}
		_ = json.NewEncoder(w).Encode(order)
	default:
		order, err := purchases.Order(ctx, strings.TrimPrefix(path, orderPrefix))
		if err != nil {
			writeMiniAppPurchaseError(w, err)
			return true
		}
		_ = json.NewEncoder(w).Encode(order)
	}
	return true
}

// Exactly one field, once, in exactly one JSON object. A regular struct decoder
// accepts duplicate keys; explicitly reject those rather than applying last-wins.
func decodePurchaseOffer(w http.ResponseWriter, r *http.Request) (string, error) {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	first, err := decoder.Token()
	if err != nil {
		return "", err
	}
	if first != json.Delim('{') {
		return "", service.ErrInvalidPurchase
	}
	var offer string
	seen := false
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return "", err
		}
		if key != "offer_id" || seen {
			return "", service.ErrInvalidPurchase
		}
		seen = true
		if err := decoder.Decode(&offer); err != nil {
			return "", err
		}
	}
	if _, err := decoder.Token(); err != nil {
		return "", err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err != nil {
			return "", err
		}
		return "", service.ErrInvalidPurchase
	}
	if !seen || offer == "" {
		return "", service.ErrInvalidPurchase
	}
	return offer, nil
}

func writeMiniAppPurchaseError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrPurchaseAccessDenied):
		writeMiniAppError(w, http.StatusForbidden, "forbidden")
	case errors.Is(err, service.ErrInvalidPurchase):
		writeMiniAppError(w, http.StatusBadRequest, "invalid_request")
	case errors.Is(err, service.ErrOfferUnavailable):
		writeMiniAppError(w, http.StatusConflict, "offer_unavailable")
	case errors.Is(err, database.ErrPurchaseKeyConflict):
		writeMiniAppError(w, http.StatusConflict, "idempotency_conflict")
	case errors.Is(err, database.ErrPurchasePending):
		writeMiniAppError(w, http.StatusConflict, "purchase_pending")
	case errors.Is(err, database.ErrOrderNotFound):
		writeMiniAppError(w, http.StatusNotFound, "order_not_found")
	default:
		writeMiniAppError(w, http.StatusServiceUnavailable, "service_unavailable")
	}
}
