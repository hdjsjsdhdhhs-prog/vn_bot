package web

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/service"
)

// SetStarsPaymentService enables invoice creation only after the real Telegram
// client and subscription synchronization service have been initialized.
func (s *Server) SetStarsPaymentService(stars *service.StarsPaymentService) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.starsService = stars
}

func (s *Server) starsPayments() *service.StarsPaymentService {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.starsService
}

// POST /api/miniapp/orders/{purchase_id}/invoice accepts NO body or query terms.
// Authorization is the enclosing initData middleware, never a subscription URL.
func serveMiniAppStarsInvoice(w http.ResponseWriter, r *http.Request, purchases *service.PurchaseService, providers []func() *service.StarsPaymentService) bool {
	const prefix = "/api/miniapp/orders/"
	if !strings.HasPrefix(r.URL.Path, prefix) || !strings.HasSuffix(r.URL.Path, "/invoice") {
		return false
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeMiniAppError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return true
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1))
	if err != nil || len(body) != 0 || r.URL.RawQuery != "" {
		writeMiniAppError(w, http.StatusBadRequest, "invalid_request")
		return true
	}
	var stars *service.StarsPaymentService
	if len(providers) == 1 && providers[0] != nil {
		stars = providers[0]()
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	ref := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, prefix), "/invoice")
	link, err := stars.Invoice(ctx, purchases, ref)
	if err != nil {
		if errors.Is(err, database.ErrStarsPurchaseInvalid) {
			writeMiniAppError(w, http.StatusConflict, "purchase_not_payable")
		} else {
			writeMiniAppPurchaseError(w, err)
		}
		return true
	}
	_ = json.NewEncoder(w).Encode(struct {
		InvoiceURL string `json:"invoice_url"`
	}{link})
	return true
}
