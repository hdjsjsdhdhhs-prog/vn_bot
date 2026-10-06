package scheduler

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/service"

	"go.uber.org/zap"
)

const (
	// warmupWorkerInterval is the tick of the customer warm-up.
	warmupWorkerInterval = 10 * time.Minute
	// warmupBatchSize is the page size of due events per scenario.
	warmupBatchSize = 200
	// warmupTickBudget bounds one tick so it never overlaps the next one.
	warmupTickBudget = 8 * time.Minute
)

// WarmupTickStats summarizes one tick (logged, asserted by tests).
type WarmupTickStats struct {
	Due         int
	Handled     int // already sent/failed/blocked/being sent
	Skipped     map[string]int
	Claimed     int
	Sent        int
	Retry       int
	Failed      int
	Blocked     int
	Unreachable int
	Errors      int
	Recovered   int64
	Expired     int64
	FloodPaused bool
}

// WarmupWorker sends the customer warm-up messages. Each tick it asks the
// WarmupService which enabled scenarios are due for which events, checks
// conditions and limits, claims the delivery (UNIQUE scenario/user/anchor),
// renders, sends through the Telegram adapter and records the outcome.
//
// It is safe to run repeatedly and concurrently with itself: a tick that
// finds the previous one still running is skipped, and every send is
// guarded by the delivery claim in the database.
type WarmupWorker struct {
	svc      *service.WarmupService
	sender   service.WarmupSender
	interval time.Duration
	batch    int
	now      func() time.Time
	running  sync.Mutex
}

// NewWarmupWorker creates the worker.
func NewWarmupWorker(svc *service.WarmupService, sender service.WarmupSender) *WarmupWorker {
	return &WarmupWorker{svc: svc, sender: sender, interval: warmupWorkerInterval, batch: warmupBatchSize,
		now: func() time.Time { return time.Now().UTC() }}
}

// Run starts the periodic loop. It blocks until ctx is cancelled.
func (w *WarmupWorker) Run(ctx context.Context) {
	logger.Info("Warmup worker started", zap.Duration("interval", w.interval))

	w.tick(ctx)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			w.tick(ctx)
		case <-ctx.Done():
			logger.Info("Warmup worker stopped")
			return
		}
	}
}

func (w *WarmupWorker) tick(ctx context.Context) {
	stats, ran := w.process(ctx)
	if !ran {
		return
	}
	if stats.Claimed == 0 && stats.Errors == 0 && stats.Recovered == 0 && stats.Expired == 0 {
		logger.Debug("Warmup tick: nothing to send", zap.Int("due", stats.Due))
		return
	}
	logger.Info("Warmup tick finished",
		zap.Int("due", stats.Due), zap.Int("claimed", stats.Claimed), zap.Int("sent", stats.Sent),
		zap.Int("retry", stats.Retry), zap.Int("failed", stats.Failed), zap.Int("blocked", stats.Blocked),
		zap.Int("unreachable", stats.Unreachable), zap.Int("errors", stats.Errors), zap.Any("skipped", stats.Skipped),
		zap.Int64("recovered", stats.Recovered), zap.Int64("expired_retries", stats.Expired), zap.Bool("flood_paused", stats.FloodPaused))
}

// errWarmupStopTick ends a tick early (flood control or shutdown).
var errWarmupStopTick = errors.New("warmup tick stopped")

// process runs one tick. ran is false when another tick is still running.
func (w *WarmupWorker) process(ctx context.Context) (stats WarmupTickStats, ran bool) {
	if !w.running.TryLock() {
		logger.Warn("Warmup tick skipped: the previous one is still running")
		return stats, false
	}
	defer w.running.Unlock()
	stats.Skipped = map[string]int{}

	ctx, cancel := context.WithTimeout(ctx, warmupTickBudget)
	defer cancel()
	now := w.now()

	settings, err := w.svc.Settings(ctx)
	if err != nil {
		logger.Warn("Warmup tick: failed to load settings", zap.Error(err))
		stats.Errors++
		return stats, true
	}
	stats.Recovered, stats.Expired, err = w.svc.Housekeeping(ctx, settings, now)
	if err != nil {
		logger.Warn("Warmup tick: housekeeping failed", zap.Error(err))
		stats.Errors++
	}
	if !settings.Enabled {
		return stats, true
	}
	scenarios, err := w.svc.EnabledScenarios(ctx)
	if err != nil {
		logger.Warn("Warmup tick: failed to load scenarios", zap.Error(err))
		stats.Errors++
		return stats, true
	}

	// One warm-up message per user per tick, whatever the limits allow.
	messaged := map[int64]bool{}
	for i := range scenarios {
		err := w.processScenario(ctx, &scenarios[i], settings, now, messaged, &stats)
		if err != nil {
			if !errors.Is(err, errWarmupStopTick) {
				logger.Warn("Warmup tick stopped", zap.Error(err))
			}
			return stats, true
		}
	}
	return stats, true
}

func (w *WarmupWorker) processScenario(ctx context.Context, sc *database.WarmupScenario, settings *database.WarmupSettings, now time.Time, messaged map[int64]bool, stats *WarmupTickStats) error {
	cursor := int64(0)
	for {
		if ctx.Err() != nil {
			return errWarmupStopTick
		}
		due, next, err := w.svc.DueAnchors(ctx, sc, settings, now, cursor, w.batch)
		if err != nil {
			logger.Warn("Warmup: failed to resolve scenario events", zap.Uint("scenario_id", sc.ID), zap.String("scenario", sc.Key), zap.Error(err))
			stats.Errors++
			return nil
		}
		for _, d := range due {
			stats.Due++
			err := w.deliver(ctx, sc, settings, d, now, messaged, stats)
			if err != nil {
				return err
			}
		}
		if next == 0 {
			return nil
		}
		cursor = next
	}
}

// deliver runs one event through candidate → handled? → conditions → limits
// → claim (+coupon) → render → send → outcome. It returns errWarmupStopTick
// when the tick must stop (flood control, shutdown).
func (w *WarmupWorker) deliver(ctx context.Context, sc *database.WarmupScenario, settings *database.WarmupSettings, d service.WarmupDue, now time.Time, messaged map[int64]bool, stats *WarmupTickStats) error {
	fields := []zap.Field{zap.Uint("scenario_id", sc.ID), zap.String("scenario", sc.Key), zap.Int64("telegram_id", d.Anchor.TelegramID)}
	if messaged[d.Anchor.TelegramID] {
		stats.Skipped["same_tick"]++
		return nil
	}
	// Cheap check first: events already sent, failed, blocked or being sent
	// are not evaluated again. A retry goes on to be re-claimed.
	status, err := w.svc.DeliveryStatus(ctx, sc, d)
	if err != nil {
		logger.Warn("Warmup: failed to read delivery status", append(fields, zap.Error(err))...)
		stats.Errors++
		return nil
	}
	if status != "" && status != database.WarmupDeliveryRetry {
		stats.Handled++
		return nil
	}

	decision, err := w.svc.Evaluate(ctx, sc, settings, d, now)
	if err != nil {
		logger.Warn("Warmup: failed to evaluate event", append(fields, zap.Error(err))...)
		stats.Errors++
		return nil
	}
	if !decision.Send {
		stats.Skipped[decision.Reason]++
		return nil
	}

	claim, err := w.svc.Claim(ctx, sc, d, service.WarmupRetryPolicy(), now)
	if err != nil {
		logger.Warn("Warmup: failed to claim delivery", append(fields, zap.Error(err))...)
		stats.Errors++
		return nil
	}
	if claim == nil {
		stats.Handled++ // claimed meanwhile, or a retry still backing off
		return nil
	}
	stats.Claimed++
	messaged[d.Anchor.TelegramID] = true
	fields = append(fields, zap.Uint("delivery_id", claim.Delivery.ID), zap.Int("attempt", claim.Delivery.Attempts))

	msg, err := w.svc.RenderScenario(ctx, sc, service.RecipientFor(d, decision.Facts, claim))
	if err != nil {
		logger.Warn("Warmup: scenario cannot be rendered, delivery failed", append(fields, zap.Error(err))...)
		stats.Failed++
		failErr := w.svc.FailDelivery(ctx, claim.Delivery, "render: "+err.Error(), w.now())
		if failErr != nil {
			logger.Warn("Warmup: failed to record render failure", append(fields, zap.Error(failErr))...)
			stats.Errors++
		}
		return nil
	}

	messageID, sendErr := w.sender.SendWarmup(ctx, d.Anchor.TelegramID, msg)
	outcome, err := w.svc.RecordSendResult(ctx, claim.Delivery, messageID, sendErr, w.now())
	if err != nil {
		logger.Error("Warmup: failed to record delivery outcome", append(fields, zap.String("outcome", string(outcome)), zap.NamedError("send_error", sendErr), zap.Error(err))...)
		stats.Errors++
	}
	switch outcome {
	case service.WarmupOutcomeSent:
		stats.Sent++
	case service.WarmupOutcomeRetry:
		stats.Retry++
	case service.WarmupOutcomeFailed:
		stats.Failed++
	case service.WarmupOutcomeBlocked:
		stats.Blocked++
	case service.WarmupOutcomeUnreachable:
		stats.Unreachable++
	}
	if sendErr != nil {
		logger.Warn("Warmup: send failed", append(fields, zap.String("outcome", string(outcome)), zap.Error(sendErr))...)
	}

	switch {
	case ctx.Err() != nil:
		return errWarmupStopTick
	case sendErr != nil && service.WarmupSendErrorKindOf(sendErr) == service.WarmupSendFlood:
		// Telegram throttles the bot as a whole: stop this tick, the
		// released delivery is retried after its backoff.
		stats.FloodPaused = true
		return errWarmupStopTick
	}
	return nil
}
