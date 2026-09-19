package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"go.uber.org/zap"
)

const starsPayloadPrefix = "stars:v1:"

// StarsInvoiceClient uses Telegram's official createInvoiceLink method. Amount
// is an integer number of Stars (XTR has exponent zero), not fiat cents.
type StarsInvoiceClient interface {
	CreateInvoiceLink(context.Context, string, string, int64) (string, error)
	AnswerPreCheckout(context.Context, string, bool) error
}

type starsRepository interface {
	PrepareStarsInvoice(context.Context, string, int64, database.PurchaseValidator) (*database.PurchaseRecord, error)
	SaveStarsInvoiceLink(context.Context, uint, string) error
	ApproveStarsCheckout(context.Context, string, int64, string, string, int64, database.PurchaseValidator) error
	RecordTelegramPayment(context.Context, *database.TelegramPaymentReceipt) error
	PendingTelegramPayments(context.Context, uint, int) ([]database.TelegramPaymentReceipt, error)
	SettleStarsPayment(context.Context, string, string, database.PurchaseValidator, database.ApplyPlanInTxFn) (*database.Order, *database.Subscription, bool, error)
}

// StarsPaymentService adapts Telegram payments to Purchase Foundation and the
// existing atomic order/subscription lifecycle. No Mini App settlement API exists.
type StarsPaymentService struct {
	orders   *OrderService
	repo     starsRepository
	telegram StarsInvoiceClient
}

func NewStarsPaymentService(orders *OrderService, telegram StarsInvoiceClient) *StarsPaymentService {
	s := &StarsPaymentService{orders: orders, telegram: telegram}
	if orders != nil {
		s.repo, _ = orders.db.(starsRepository)
	}
	return s
}

func (s *StarsPaymentService) ready() bool {
	return s != nil && s.repo != nil && s.orders != nil && s.orders.syncSvc != nil && s.telegram != nil
}

// Invoice only accepts an existing purchase reference, resolved with the same
// authenticated PurchaseService authority as create/read. No caller-owned terms.
func (s *StarsPaymentService) Invoice(ctx context.Context, purchases *PurchaseService, ref string) (string, error) {
	if !s.ready() {
		return "", ErrPaymentDisabled
	}
	if purchases == nil {
		return "", ErrPurchaseAccessDenied
	}
	payer, err := purchases.customer(ctx)
	if err != nil {
		return "", err
	}
	if _, err = purchases.Order(ctx, ref); err != nil {
		return "", err
	}
	record, err := s.repo.PrepareStarsInvoice(ctx, ref, payer, validatePurchaseOffer)
	if err != nil {
		return "", err
	}
	if record.Order.PaymentURL != "" {
		return record.Order.PaymentURL, nil
	}
	// Fixed, bounded title; the product name/duration belongs in the server-side
	// offer response, not in a mutable UI-supplied invoice description.
	link, err := s.telegram.CreateInvoiceLink(ctx, "VPN subscription", starsPayloadPrefix+ref, record.Order.AmountCents)
	if err != nil {
		return "", fmt.Errorf("create Stars invoice: %w", err)
	}
	if err = s.repo.SaveStarsInvoiceLink(ctx, record.Order.ID, link); err != nil {
		return "", err
	}
	return link, nil
}

func starsPurchaseReference(payload string) (string, error) {
	ref, ok := strings.CutPrefix(payload, starsPayloadPrefix)
	if !ok || !validPurchaseReference(ref) {
		return "", database.ErrStarsPurchaseInvalid
	}
	return ref, nil
}

// PreCheckout always answers Telegram (including negative answers on DB errors),
// within its ten-second deadline. A positive answer reserves only, never settles.
func (s *StarsPaymentService) PreCheckout(ctx context.Context, query *tgbotapi.PreCheckoutQuery) error {
	if query == nil || query.ID == "" {
		return database.ErrStarsPurchaseInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	validation := ErrPaymentDisabled
	if s.ready() {
		ref, err := starsPurchaseReference(query.InvoicePayload)
		validation = err
		if validation == nil {
			if query.From == nil || query.From.ID <= 0 {
				validation = database.ErrStarsPurchaseInvalid
			} else {
				// Leave time for answerPreCheckoutQuery even on a slow DB.
				dbCtx, dbCancel := context.WithTimeout(ctx, 4*time.Second)
				validation = s.repo.ApproveStarsCheckout(dbCtx, ref, query.From.ID, query.ID, query.Currency, int64(query.TotalAmount), validatePurchaseOffer)
				dbCancel()
			}
		}
	}
	if s == nil || s.telegram == nil {
		return ErrPaymentDisabled
	}
	if err := s.telegram.AnswerPreCheckout(ctx, query.ID, validation == nil); err != nil {
		return fmt.Errorf("answer Stars pre-checkout: %w", err)
	}
	return validation
}

// CaptureUpdate persists payment delivery BEFORE the polling offset advances.
// The handler repeats capture with strict error reporting (safe replay). Only
// official successful_payment messages enter the inbox; browser callbacks and
// pre-checkout cannot fabricate settlement. Permanent invalid updates are
// rejected without blocking polling; infrastructure failures must be retried.
func (s *StarsPaymentService) CaptureUpdate(ctx context.Context, update tgbotapi.Update) error {
	err := s.capturePayment(ctx, update)
	if errors.Is(err, database.ErrStarsPurchaseInvalid) {
		// A malformed/conflicting update cannot become valid by retrying it. Keep
		// the original receipt on conflict, but do not stall ALL bot polling forever.
		// The handler still rejects this update; only infrastructure errors block ACK.
		logger.Warn("Invalid Telegram payment update rejected", zap.Int("update_id", update.UpdateID))
		return nil
	}
	return err
}

func (s *StarsPaymentService) capturePayment(ctx context.Context, update tgbotapi.Update) error {
	m := update.Message
	if m == nil || m.SuccessfulPayment == nil {
		return nil
	}
	if s == nil || s.repo == nil {
		return ErrPaymentDisabled
	}
	p := m.SuccessfulPayment
	if m.From == nil || m.From.ID <= 0 || p.TelegramPaymentChargeID == "" || p.TotalAmount <= 0 || p.Currency != "XTR" {
		return database.ErrStarsPurchaseInvalid
	}
	if _, err := starsPurchaseReference(p.InvoicePayload); err != nil {
		return err
	}
	payer := m.From.ID
	return s.repo.RecordTelegramPayment(ctx, &database.TelegramPaymentReceipt{
		TelegramChargeID: p.TelegramPaymentChargeID, ProviderChargeID: p.ProviderPaymentChargeID,
		PayerTelegramID: payer, InvoicePayload: p.InvoicePayload, Currency: p.Currency,
		TotalAmount: int64(p.TotalAmount), TelegramDate: m.Date,
	})
}

// SuccessfulPayment has no HTTP/Mini App route. Call only from trusted Telegram
// updates delivered by getUpdates with the bot token (or an authenticated webhook).
func (s *StarsPaymentService) SuccessfulPayment(ctx context.Context, update tgbotapi.Update) (*PaymentConfirmation, error) {
	if update.Message == nil || update.Message.SuccessfulPayment == nil {
		return nil, database.ErrStarsPurchaseInvalid
	}
	if err := s.capturePayment(ctx, update); err != nil {
		return nil, err
	}
	p := update.Message.SuccessfulPayment
	return s.settle(ctx, p.InvoicePayload, p.TelegramPaymentChargeID)
}

// Fulfillment honours immutable terms already approved; product deactivation or
// offer expiry must not lose a paid entitlement. Ownership, subscription status,
// ProviderSource validity and same-plan restrictions still fail closed.
func validateStarsFulfillment(c *database.PurchaseCatalog, p *database.Product) (time.Time, error) {
	copy := *p
	copy.IsActive, copy.OfferEndsAt = true, nil
	if p.Plan != nil {
		plan := *p.Plan
		plan.IsActive = true
		copy.Plan = &plan
	}
	return validatePurchaseOffer(c, &copy)
}

func (s *StarsPaymentService) settle(ctx context.Context, payload, charge string) (*PaymentConfirmation, error) {
	if !s.ready() {
		return nil, ErrPaymentDisabled
	}
	ref, err := starsPurchaseReference(payload)
	if err != nil {
		return nil, err
	}
	order, sub, activated, err := s.repo.SettleStarsPayment(ctx, ref, charge, validateStarsFulfillment, s.orders.syncSvc.ApplyPlanToSubscriptionInTx)
	if err != nil {
		return nil, fmt.Errorf("settle Stars payment: %w", err)
	}
	if activated {
		recordPaymentAmount("confirmed", order.AmountCents, order.Currency)
		if s.orders.subSvc != nil {
			s.orders.subSvc.InvalidateSubscription(ctx, sub.TelegramID)
			s.orders.subSvc.InvalidateBySubID(ctx, sub.SubscriptionID)
		}
		// Durable node prerequisites already committed; external sync is best effort.
		syncCtx, cancel := context.WithTimeout(ctx, paymentSyncTimeout)
		defer cancel()
		if err := s.orders.syncSvc.SyncSubscription(syncCtx, sub.ID); err != nil {
			logger.Warn("Stars post-commit sync failed", zap.Uint("order_id", order.ID), zap.Error(err))
		}
	}
	return &PaymentConfirmation{Order: order, Activated: activated}, nil
}

// RetryPayments recovers after a process crash or DB-setup failure. Keyset
// pagination ensures an unfulfillable receipt cannot starve subsequent payments.
func (s *StarsPaymentService) RetryPayments(ctx context.Context) error {
	if !s.ready() {
		return ErrPaymentDisabled
	}
	var after uint
	var failures []error
	for {
		receipts, err := s.repo.PendingTelegramPayments(ctx, after, 50)
		if err != nil {
			return err
		}
		if len(receipts) == 0 {
			return errors.Join(failures...)
		}
		for _, receipt := range receipts {
			if err := ctx.Err(); err != nil {
				return err
			}
			if _, err := s.settle(ctx, receipt.InvoicePayload, receipt.TelegramChargeID); err != nil {
				failures = append(failures, fmt.Errorf("Telegram receipt %d: %w", receipt.ID, err))
			}
			after = receipt.ID
		}
	}
}
