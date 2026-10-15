package scheduler

import (
	"context"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/service"

	"go.uber.org/zap"
)

// providerFeeWorkerInterval is how often paid orders without a recorded
// provider fee are retried.
const providerFeeWorkerInterval = 5 * time.Minute

// providerFeeBatch bounds the paid orders inspected per run.
const providerFeeBatch = 50

// providerFeeSyncer is the narrow contract the worker needs from OrderService.
type providerFeeSyncer interface {
	SyncProviderFees(ctx context.Context, limit int) error
}

var _ providerFeeSyncer = (*service.OrderService)(nil)

// ProviderFeeWorker periodically retries best-effort provider fee lookups for
// paid orders (orders.provider_fee_cents is the durable retry queue). It is the
// only periodic payment reconciliation: subscription access is local state and
// needs no background synchronization.
type ProviderFeeWorker struct {
	orders providerFeeSyncer
}

// NewProviderFeeWorker creates a ProviderFeeWorker. A nil order service makes
// Run a no-op loop that only waits for shutdown.
func NewProviderFeeWorker(orders *service.OrderService) *ProviderFeeWorker {
	w := &ProviderFeeWorker{}
	if orders != nil {
		w.orders = orders
	}
	return w
}

// Run starts the periodic loop. It blocks until ctx is cancelled.
func (w *ProviderFeeWorker) Run(ctx context.Context) {
	logger.Info("Provider fee worker started", zap.String("interval", providerFeeWorkerInterval.String()))

	w.process(ctx)

	ticker := time.NewTicker(providerFeeWorkerInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			w.process(ctx)
		case <-ctx.Done():
			logger.Info("Provider fee worker stopped")
			return
		}
	}
}

func (w *ProviderFeeWorker) process(ctx context.Context) {
	if w.orders == nil {
		return
	}

	err := w.orders.SyncProviderFees(ctx, providerFeeBatch)
	if err != nil {
		logger.Warn("Provider fee sync failed", zap.Error(err))
	}
}
