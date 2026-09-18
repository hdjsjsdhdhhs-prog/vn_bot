package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/service"
	"github.com/kereal/rs8kvn_bot/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Keep the real scan and expiry implementation, inserting a committed renewal
// exactly between the worker's scan and its execution of the queued operation.
type renewAfterExpiryScan struct {
	*database.Service
	afterScan func([]database.Subscription)
}

func (r *renewAfterExpiryScan) GetExpiredPaidSubscriptions(ctx context.Context, now time.Time) ([]database.Subscription, error) {
	subs, err := r.Service.GetExpiredPaidSubscriptions(ctx, now)
	if err == nil {
		r.afterScan(subs)
	}
	return subs, err
}

func TestSubscriptionRenewal_ExpiryWorkerStaleScan(t *testing.T) {
	for _, name := range []string{"provider", "legacy without sync", "legacy with sync"} {
		t.Run(name, func(t *testing.T) {
			db, err := testutil.NewTestDatabaseService(t)
			require.NoError(t, err)
			ctx := context.Background()
			plan := &database.Plan{Name: "renewal-worker", IsActive: true}
			require.NoError(t, db.GetDB().Create(plan).Error)
			past := time.Now().UTC().Add(-time.Hour)
			sub := &database.Subscription{TelegramID: 82001, ClientID: "renewal-client", SubscriptionID: "renewal-worker", Status: "active", PlanID: plan.ID, ExpiresAt: &past}
			if name == "provider" {
				source := &database.ProviderSource{Name: "renewal", Type: "external", Enabled: true, SubscriptionURL: "https://provider.example/feed"}
				require.NoError(t, db.CreateProviderSource(ctx, source))
				sub.ProviderSourceID = &source.ID
			}
			require.NoError(t, db.CreateSubscription(ctx, sub, ""))
			svc := service.NewSubscriptionService(db, nil, nil, nil, &config.Config{GlobalSubURL: "https://customer.example/sub/"})
			if name == "legacy with sync" {
				svc.SetSyncService(service.NewSyncService(db, nil, nil))
			}
			var renewed *service.RenewalResult
			repo := &renewAfterExpiryScan{Service: db, afterScan: func(queued []database.Subscription) {
				require.Len(t, queued, 1)
				require.Equal(t, sub.ID, queued[0].ID)
				require.True(t, queued[0].ExpiresAt.Before(time.Now()))
				renewed, err = svc.RenewSubscription(ctx, sub.ID, 7)
				require.NoError(t, err)
			}}
			NewSubscriptionExpireWorker(repo, svc).process(ctx)
			require.NotNil(t, renewed, "the worker must scan and renewal must commit before expiry")
			stored, err := db.GetByID(ctx, sub.ID)
			require.NoError(t, err)
			assert.Equal(t, renewed.Subscription, stored, "stale expiry must not alter any field of the renewed row")
			due, err := db.GetExpiredPaidSubscriptions(ctx, time.Now().UTC())
			require.NoError(t, err)
			assert.Empty(t, due)
		})
	}
}
