package scheduler

import (
	"context"
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/logger"

	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	code := m.Run()
	os.Exit(code)
}

func init() {
	_, _ = logger.Init("", "error")
}

type fakeFeeSyncer struct {
	calls atomic.Int32
	limit atomic.Int32
	err   error
}

func (f *fakeFeeSyncer) SyncProviderFees(_ context.Context, limit int) error {
	f.calls.Add(1)
	f.limit.Store(int32(limit))
	return f.err
}

func runFeeWorkerBriefly(t *testing.T, w *ProviderFeeWorker) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		w.Run(ctx)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ProviderFeeWorker.Run should return after context cancellation")
	}
}

func TestProviderFeeWorker_RunsImmediatelyWithBatchLimit(t *testing.T) {
	t.Parallel()

	syncer := &fakeFeeSyncer{}
	runFeeWorkerBriefly(t, &ProviderFeeWorker{orders: syncer})

	require.Equal(t, int32(1), syncer.calls.Load(), "the first pass runs at start, the next one only after the interval")
	require.Equal(t, int32(providerFeeBatch), syncer.limit.Load())
}

func TestProviderFeeWorker_ErrorIsBestEffort(t *testing.T) {
	t.Parallel()

	syncer := &fakeFeeSyncer{err: errors.New("provider down")}
	runFeeWorkerBriefly(t, &ProviderFeeWorker{orders: syncer})

	require.Equal(t, int32(1), syncer.calls.Load())
}

func TestProviderFeeWorker_NilOrderServiceOnlyWaitsForShutdown(t *testing.T) {
	t.Parallel()

	runFeeWorkerBriefly(t, NewProviderFeeWorker(nil))
}
