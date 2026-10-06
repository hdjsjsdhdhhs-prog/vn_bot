package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
)

// Delivery contract of the warm-up: the worker (scheduler) asks the service
// what is due and whom it may be sent to, the Telegram adapter (bot) sends,
// and the service records the outcome in the delivery journal.

// WarmupSender sends one rendered warm-up message to a Telegram chat and
// returns the Telegram message ID. Failures are *WarmupSendError, except a
// cancelled context, which is returned as is.
type WarmupSender interface {
	SendWarmup(ctx context.Context, chatID int64, msg *WarmupMessage) (int, error)
}

// WarmupSendErrorKind classifies a Telegram send failure.
type WarmupSendErrorKind int

const (
	// WarmupSendTransient: network error, Telegram unavailable (5xx): retry.
	WarmupSendTransient WarmupSendErrorKind = iota + 1
	// WarmupSendFlood: HTTP 429; retry, and stop sending for this tick.
	WarmupSendFlood
	// WarmupSendBlocked: the user blocked the bot.
	WarmupSendBlocked
	// WarmupSendUnreachable: deactivated account, chat not found, no rights.
	WarmupSendUnreachable
	// WarmupSendPermanent: Telegram rejected the request itself (400/403):
	// retrying the same message cannot succeed.
	WarmupSendPermanent
)

func (k WarmupSendErrorKind) String() string {
	switch k {
	case WarmupSendTransient:
		return "transient"
	case WarmupSendFlood:
		return "flood"
	case WarmupSendBlocked:
		return "blocked"
	case WarmupSendUnreachable:
		return "unreachable"
	case WarmupSendPermanent:
		return "permanent"
	default:
		return "unknown"
	}
}

// WarmupSendError is a classified send failure.
type WarmupSendError struct {
	Kind WarmupSendErrorKind
	// RetryAfter is Telegram's retry_after hint of a flood error.
	RetryAfter time.Duration
	Err        error
}

func (e *WarmupSendError) Error() string {
	return fmt.Sprintf("warmup send %s: %v", e.Kind, e.Err)
}

func (e *WarmupSendError) Unwrap() error { return e.Err }

// WarmupSendErrorKindOf returns the kind of a send error. An unclassified
// error (e.g. the image could not be loaded) is transient.
func WarmupSendErrorKindOf(err error) WarmupSendErrorKind {
	var sendErr *WarmupSendError
	if errors.As(err, &sendErr) {
		return sendErr.Kind
	}
	return WarmupSendTransient
}

// Retry policy of warm-up deliveries: three sends at most, the retries after
// 10 and 20 minutes (one and two worker ticks), capped at an hour. All of it
// fits the 6-hour catch-up window; a retry that leaves the window is failed.
const (
	warmupMaxAttempts     = 3
	warmupRetryBackoff    = 10 * time.Minute
	warmupRetryMaxBackoff = time.Hour
)

// WarmupRetryPolicy is the retry policy of warm-up deliveries.
func WarmupRetryPolicy() database.WarmupRetryPolicy {
	return database.WarmupRetryPolicy{MaxAttempts: warmupMaxAttempts, Backoff: warmupRetryBackoff, MaxBackoff: warmupRetryMaxBackoff}
}

// EnabledScenarios returns the scenarios the worker runs, in admin order.
func (s *WarmupService) EnabledScenarios(ctx context.Context) ([]database.WarmupScenario, error) {
	return s.repo.ListEnabledWarmupScenarios(ctx)
}

// DeliveryStatus returns the journal status of a scenario for an event ("":
// never claimed).
func (s *WarmupService) DeliveryStatus(ctx context.Context, sc *database.WarmupScenario, due WarmupDue) (string, error) {
	return s.repo.WarmupDeliveryStatus(ctx, sc.ID, due.Anchor.TelegramID, due.Anchor.AnchorKey)
}

// WarmupOutcome is the recorded result of one send.
type WarmupOutcome string

const (
	WarmupOutcomeSent        WarmupOutcome = database.WarmupDeliverySent
	WarmupOutcomeRetry       WarmupOutcome = database.WarmupDeliveryRetry
	WarmupOutcomeFailed      WarmupOutcome = database.WarmupDeliveryFailed
	WarmupOutcomeBlocked     WarmupOutcome = database.WarmupDeliveryBlocked
	WarmupOutcomeUnreachable WarmupOutcome = "unreachable"
)

// RecordSendResult moves a claimed delivery to its outcome:
//
//   - nil error           → sent (sent_at = now);
//   - blocked             → blocked, coupon revoked, user excluded;
//   - unreachable         → failed, coupon revoked, user excluded;
//   - permanent           → failed, coupon revoked;
//   - transient / flood   → retry while attempts are left, else failed;
//   - cancelled context   → retry (the send was interrupted by shutdown).
//
// The journal write uses a context that survives cancellation, so a message
// sent during shutdown is still recorded as sent.
func (s *WarmupService) RecordSendResult(ctx context.Context, d *database.WarmupDelivery, messageID int, sendErr error, now time.Time) (WarmupOutcome, error) {
	ctx = context.WithoutCancel(ctx)
	if sendErr == nil {
		return WarmupOutcomeSent, s.repo.MarkWarmupDeliverySent(ctx, d.ID, messageID, now)
	}
	text := sendErr.Error()
	if errors.Is(sendErr, context.Canceled) || errors.Is(sendErr, context.DeadlineExceeded) {
		return WarmupOutcomeRetry, s.repo.ReleaseWarmupDelivery(ctx, d.ID, "interrupted: "+text, false, now)
	}
	switch WarmupSendErrorKindOf(sendErr) {
	case WarmupSendBlocked:
		return WarmupOutcomeBlocked, s.repo.MarkWarmupDeliveryBlocked(ctx, d.ID, text, now)
	case WarmupSendUnreachable:
		return WarmupOutcomeUnreachable, s.repo.MarkWarmupDeliveryUnreachable(ctx, d.ID, text, now)
	case WarmupSendPermanent:
		return WarmupOutcomeFailed, s.repo.ReleaseWarmupDelivery(ctx, d.ID, text, true, now)
	default:
		final := d.Attempts >= warmupMaxAttempts
		outcome := WarmupOutcomeRetry
		if final {
			outcome = WarmupOutcomeFailed
		}
		return outcome, s.repo.ReleaseWarmupDelivery(ctx, d.ID, text, final, now)
	}
}

// FailDelivery ends a claimed delivery that cannot be sent at all (e.g. the
// scenario no longer renders): failed, coupon revoked.
func (s *WarmupService) FailDelivery(ctx context.Context, d *database.WarmupDelivery, reason string, now time.Time) error {
	return s.repo.ReleaseWarmupDelivery(context.WithoutCancel(ctx), d.ID, reason, true, now)
}

// warmupStaleSending is how long a delivery may stay "sending" before it is
// considered interrupted (the process died between claim and outcome).
const warmupStaleSending = 30 * time.Minute

// Housekeeping runs before each tick: interrupted sends return to retry, and
// retries whose event left the catch-up window are failed.
func (s *WarmupService) Housekeeping(ctx context.Context, settings *database.WarmupSettings, now time.Time) (recovered, expired int64, err error) {
	recovered, err = s.repo.ReleaseStaleWarmupDeliveries(ctx, now.Add(-warmupStaleSending), now)
	if err != nil {
		return 0, 0, err
	}
	expired, err = s.repo.ExpireWarmupRetries(ctx, now.Add(-time.Duration(settings.CatchupHours)*time.Hour), now)
	if err != nil {
		return recovered, 0, err
	}
	return recovered, expired, nil
}

// RecipientFor builds the render input of a claimed delivery.
func RecipientFor(due WarmupDue, facts *database.WarmupUserFacts, claim *database.WarmupClaimResult) WarmupRecipient {
	r := WarmupRecipient{
		TelegramID: due.Anchor.TelegramID, Username: due.Anchor.Username,
		PlanName: due.Anchor.PlanName, ExpiresAt: due.Anchor.ExpiresAt,
	}
	if facts != nil && facts.Username != "" {
		r.Username = facts.Username
	}
	if claim != nil {
		r.Coupon = claim.Coupon
	}
	return r
}
