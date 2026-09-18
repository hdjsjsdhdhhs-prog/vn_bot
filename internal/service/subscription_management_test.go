package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// This test adapter models already verified application identity and a separate
// grant decision. Production transports must supply their own trusted adapter.
type managementIdentityKey struct{}
type managementBearerKey struct{}
type managementTestIdentity struct {
	telegramID int64
	grantDays  int
}

func managementTestAuthorization(ctx context.Context, action SubscriptionManagementAction, days int) (int64, error) {
	identity, ok := ctx.Value(managementIdentityKey{}).(managementTestIdentity)
	if !ok || (action == SubscriptionManagementRenew && (identity.grantDays == 0 || days > identity.grantDays)) {
		return 0, errors.New("not authorized")
	}
	return identity.telegramID, nil
}

func managementContext(id int64, days int) context.Context {
	return context.WithValue(context.Background(), managementIdentityKey{}, managementTestIdentity{id, days})
}

func TestSubscriptionManagement_RepresentationAndEligibility(t *testing.T) {
	for _, name := range []string{
		"provider active", "provider expired before worker", "provider expired", "revoked", "paused", "canceled",
		"perpetual", "trial", "disabled source", "unusable source", "missing source", "inactive provider plan", "timestamp bound",
		"legacy free", "legacy active", "legacy expired", "legacy inactive plan",
	} {
		t.Run(name, func(t *testing.T) {
			db, svc, terms := customerLifecycleFixture(t)
			ctx := managementContext(90101, database.MaxSubscriptionRenewalDays)
			legacy := name == "legacy free" || name == "legacy active" || name == "legacy expired" || name == "legacy inactive plan"
			var created *CreateResult
			var err error
			if legacy {
				created, err = svc.Create(ctx, 90101, "customer", "")
			} else {
				created, err = svc.Create(ctx, 90101, "customer", "", terms)
			}
			require.NoError(t, err)
			sub := created.Subscription
			eligible, status := false, "active"
			switch name {
			case "provider active":
				eligible = true
			case "provider expired before worker", "provider expired", "legacy expired":
				past := time.Now().UTC().Add(-time.Hour)
				sub.ExpiresAt = &past
				sub.PlanID = terms.PlanID
				if name != "provider expired before worker" {
					sub.Status = "expired"
				}
				eligible, status = true, "expired"
			case "legacy active", "legacy inactive plan":
				sub.PlanID, sub.ExpiresAt = terms.PlanID, &terms.ExpiresAt
				eligible = true
				if name == "legacy inactive plan" {
					require.NoError(t, db.GetDB().Model(&database.Plan{}).Where("id = ?", terms.PlanID).Update("is_active", false).Error)
				}
			case "revoked", "paused", "canceled":
				sub.Status, status = name, name
			case "perpetual":
				sub.ExpiresAt = nil
			case "trial":
				plan, err := db.GetPlanByName(ctx, database.TrialPlanName)
				require.NoError(t, err)
				sub.PlanID = plan.ID
			case "disabled source":
				require.NoError(t, db.GetDB().Model(&database.ProviderSource{}).Where("id = ?", terms.ProviderSourceID).Update("enabled", false).Error)
			case "unusable source":
				require.NoError(t, db.GetDB().Model(&database.ProviderSource{}).Where("id = ?", terms.ProviderSourceID).Update("headers", `{"Authorization":42}`).Error)
			case "missing source":
				require.NoError(t, db.GetDB().Exec("PRAGMA foreign_keys=OFF").Error)
				require.NoError(t, db.GetDB().Model(sub).Update("provider_source_id", 999999).Error)
				require.NoError(t, db.GetDB().Exec("PRAGMA foreign_keys=ON").Error)
			case "inactive provider plan":
				require.NoError(t, db.GetDB().Model(&database.Plan{}).Where("id = ?", terms.PlanID).Update("is_active", false).Error)
			case "timestamp bound":
				expiry := time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)
				sub.ExpiresAt = &expiry
			}
			require.NoError(t, db.UpdateSubscription(ctx, sub))
			before, err := db.GetByID(ctx, sub.ID)
			require.NoError(t, err)
			manager := NewSubscriptionManagement(svc, managementTestAuthorization)
			info, err := manager.Current(ctx)
			require.NoError(t, err)
			assert.Equal(t, status, info.Status)
			assert.Equal(t, before.ExpiresAt, info.ExpiresAt)
			assert.Equal(t, created.SubscriptionURL, info.SubscriptionURL)
			assert.Equal(t, "https://customer.example/connect/"+sub.Token, info.ConnectionURL)
			assert.Equal(t, eligible, info.Renewal.Eligible)
			if eligible {
				assert.Equal(t, "extend_days", info.Renewal.Operation)
				assert.Equal(t, 1, info.Renewal.MinDays)
				assert.Equal(t, database.MaxSubscriptionRenewalDays, info.Renewal.MaxDays)
			} else {
				assert.Empty(t, info.Renewal.Operation)
			}
			assertManagementJSON(t, info, sub)
			afterRead, err := db.GetByID(ctx, sub.ID)
			require.NoError(t, err)
			assert.Equal(t, before, afterRead, "management reads must never revive/downgrade or otherwise mutate")

			var invalidTG int64
			var invalidSub string
			svc.SetInvalidateFunc(func(id int64) { invalidTG = id })
			svc.SetInvalidateBySubIDFunc(func(id string) { invalidSub = id })
			renewed, err := manager.Renew(ctx, 1)
			stored, readErr := db.GetByID(ctx, sub.ID)
			require.NoError(t, readErr)
			if eligible {
				require.NoError(t, err)
				assert.Equal(t, "active", renewed.Status)
				assert.True(t, renewed.ExpiresAt.After(*before.ExpiresAt))
				assert.Equal(t, info.SubscriptionURL, renewed.SubscriptionURL)
				assert.Equal(t, info.ConnectionURL, renewed.ConnectionURL)
				assertRenewalIdentity(t, before, stored)
				assert.Equal(t, sub.TelegramID, invalidTG)
				assert.Equal(t, sub.SubscriptionID, invalidSub)
				fresh, err := manager.Current(ctx)
				require.NoError(t, err)
				assert.Equal(t, *renewed, fresh.CustomerSubscriptionInfo)
			} else {
				require.ErrorIs(t, err, database.ErrSubscriptionNotRenewable)
				assert.Nil(t, renewed)
				assert.Equal(t, before, stored)
				assert.Zero(t, invalidTG)
				assert.Empty(t, invalidSub)
			}
		})
	}
}

func assertManagementJSON(t *testing.T, info *SubscriptionManagementInfo, sub *database.Subscription) {
	t.Helper()
	data, err := json.Marshal(info)
	require.NoError(t, err)
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &fields))
	assert.Len(t, fields, 5)
	for _, key := range []string{"status", "expires_at", "subscription_url", "connection_url", "renewal"} {
		assert.Contains(t, fields, key)
	}
	for _, secret := range []string{sub.SubscriptionID, sub.ClientID, "provider_source", "private-hwid", "private-agent", "upstream.example", "Authorization"} {
		assert.NotContains(t, string(data), secret)
	}
}

func TestSubscriptionManagement_AuthorizationAndCustomerIsolation(t *testing.T) {
	db, svc, terms := customerLifecycleFixture(t)
	ctx := context.Background()
	a, err := svc.Create(ctx, 90201, "a", "", terms)
	require.NoError(t, err)
	b, err := svc.Create(ctx, 90202, "b", "", terms)
	require.NoError(t, err)
	manager := NewSubscriptionManagement(svc, managementTestAuthorization)
	for _, identity := range []int64{90201, 90202} {
		info, err := manager.Current(managementContext(identity, 0))
		require.NoError(t, err)
		want, other := a, b
		if identity == 90202 {
			want, other = b, a
		}
		assert.Equal(t, want.SubscriptionURL, info.SubscriptionURL)
		assert.NotContains(t, info.SubscriptionURL, other.Subscription.Token)
		_, err = manager.Renew(managementContext(identity, 0), 1)
		require.ErrorIs(t, err, ErrSubscriptionAccessDenied, "read identity is not a renewal grant")
	}
	for _, badCtx := range []context.Context{ctx, managementContext(0, 7), managementContext(-90201, 7), context.WithValue(ctx, managementBearerKey{}, a.Subscription.Token)} {
		info, err := manager.Current(badCtx)
		require.ErrorIs(t, err, ErrSubscriptionAccessDenied)
		assert.Nil(t, info)
		renewed, err := manager.Renew(badCtx, 7)
		require.ErrorIs(t, err, ErrSubscriptionAccessDenied)
		assert.Nil(t, renewed)
		assert.NotContains(t, err.Error(), a.Subscription.Token)
	}
	_, err = manager.Renew(managementContext(90201, 7), 8)
	require.ErrorIs(t, err, ErrSubscriptionAccessDenied, "authorization must cover the specific duration")
	for _, candidate := range []*SubscriptionManagement{NewSubscriptionManagement(svc, nil), NewSubscriptionManagement(svc, func(context.Context, SubscriptionManagementAction, int) (int64, error) {
		return 90201, fmt.Errorf("deny internal ID %d token %s", a.Subscription.ID, a.Subscription.Token)
	})} {
		_, err = candidate.Renew(ctx, 7)
		require.ErrorIs(t, err, ErrSubscriptionAccessDenied)
		assert.Equal(t, "subscription access denied", err.Error())
	}
	_, err = manager.Current(managementContext(99999, 7))
	require.ErrorIs(t, err, database.ErrSubscriptionNotFound)
	_, err = manager.Renew(managementContext(99999, 7), 7)
	require.ErrorIs(t, err, database.ErrSubscriptionNotFound)
	beforeB, err := db.GetByID(ctx, b.Subscription.ID)
	require.NoError(t, err)
	_, err = manager.Renew(managementContext(90201, 7), 7)
	require.NoError(t, err)
	afterB, err := db.GetByID(ctx, b.Subscription.ID)
	require.NoError(t, err)
	assert.Equal(t, beforeB, afterB, "a grant for A must not mutate B")

	// A former owner cannot renew by holding an old read snapshot or token.
	require.NoError(t, db.GetDB().Model(&database.Subscription{}).Where("id = ?", a.Subscription.ID).Update("telegram_id", 90203).Error)
	before, err := db.GetByID(ctx, a.Subscription.ID)
	require.NoError(t, err)
	_, err = manager.Renew(managementContext(90201, 7), 7)
	require.ErrorIs(t, err, database.ErrSubscriptionNotFound)
	after, err := db.GetByID(ctx, a.Subscription.ID)
	require.NoError(t, err)
	assert.Equal(t, before, after)
}

func TestSubscriptionManagement_FailureRollbackAndSafeErrors(t *testing.T) {
	db, svc, terms := customerLifecycleFixture(t)
	ctx := managementContext(90301, database.MaxSubscriptionRenewalDays+1)
	created, err := svc.Create(ctx, 90301, "customer", "", terms)
	require.NoError(t, err)
	manager := NewSubscriptionManagement(svc, managementTestAuthorization)
	before, err := db.GetByID(ctx, created.Subscription.ID)
	require.NoError(t, err)
	svc.SetInvalidateFunc(func(int64) { t.Error("failed grant invalidated cache") })
	svc.SetInvalidateBySubIDFunc(func(string) { t.Error("failed grant invalidated feed cache") })
	for _, days := range []int{-1, 0, database.MaxSubscriptionRenewalDays + 1} {
		_, err := manager.Renew(ctx, days)
		require.ErrorIs(t, err, database.ErrInvalidRenewalDays)
	}
	require.NoError(t, db.GetDB().Exec(`CREATE TRIGGER block_management_renewal AFTER UPDATE OF expires_at ON subscriptions BEGIN SELECT RAISE(ABORT, 'private-provider internal ID 90301'); END`).Error)
	_, err = manager.Renew(ctx, 7)
	require.ErrorIs(t, err, ErrSubscriptionManagementUnavailable)
	assert.Equal(t, "subscription management unavailable", err.Error())
	assert.NotNil(t, errors.Unwrap(err), "preserve diagnostics without exposing cause text")
	after, err := db.GetByID(ctx, created.Subscription.ID)
	require.NoError(t, err)
	assert.Equal(t, before, after)
	// Configuration errors must fail BEFORE any grant or customer URL is exposed.
	svc.cfg.GlobalSubURL = "https://customer.example/sub/?private=credential"
	_, err = manager.Current(ctx)
	require.ErrorIs(t, err, ErrSubscriptionManagementUnavailable)
	_, err = manager.Renew(ctx, 7)
	require.ErrorIs(t, err, ErrSubscriptionManagementUnavailable)
	assert.NotContains(t, err.Error(), "credential")
}

func TestSubscriptionManagement_UnavailableAndCancellation(t *testing.T) {
	db, svc, terms := customerLifecycleFixture(t)
	ctx := managementContext(90401, 7)
	_, err := svc.Create(ctx, 90401, "customer", "", terms)
	require.NoError(t, err)
	manager := NewSubscriptionManagement(svc, managementTestAuthorization)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = manager.Current(cancelled)
	require.ErrorIs(t, err, context.Canceled)
	_, err = manager.Renew(cancelled, 7)
	require.ErrorIs(t, err, context.Canceled)
	_, err = NewSubscriptionManagement(nil, managementTestAuthorization).Current(ctx)
	require.ErrorIs(t, err, ErrSubscriptionManagementUnavailable)
	// An infrastructure error is not silently represented as not renewable.
	require.NoError(t, db.GetDB().Callback().Query().Before("gorm:query").Register("management_source_failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "provider_sources" {
			tx.AddError(errors.New("private database query details"))
		}
	}))
	_, err = manager.Current(ctx)
	require.ErrorIs(t, err, ErrSubscriptionManagementUnavailable)
}

func TestSubscriptionManagement_FreshEligibilityAndRevalidation(t *testing.T) {
	db, svc, terms := customerLifecycleFixture(t)
	ctx := managementContext(90601, 7)
	created, err := svc.Create(ctx, 90601, "customer", "", terms)
	require.NoError(t, err)
	manager := NewSubscriptionManagement(svc, managementTestAuthorization)
	info, err := manager.Current(ctx)
	require.NoError(t, err)
	require.True(t, info.Renewal.Eligible)
	require.NoError(t, db.GetDB().Model(&database.ProviderSource{}).Where("id = ?", terms.ProviderSourceID).Update("enabled", false).Error)
	fresh, err := manager.Current(ctx)
	require.NoError(t, err)
	assert.False(t, fresh.Renewal.Eligible, "no management cache may hide a source change")
	_, err = manager.Renew(ctx, 7)
	require.ErrorIs(t, err, database.ErrSubscriptionNotRenewable, "previous eligibility must never authorize stale state")
	stored, err := db.GetByID(ctx, created.Subscription.ID)
	require.NoError(t, err)
	assert.True(t, terms.ExpiresAt.Equal(*stored.ExpiresAt))
}

func TestSubscriptionManagement_ConcurrentWithExistingRenewal(t *testing.T) {
	db, svc, terms := customerLifecycleFixture(t)
	ctx, cancel := context.WithTimeout(managementContext(90701, 1), 20*time.Second)
	defer cancel()
	created, err := svc.Create(ctx, 90701, "customer", "", terms)
	require.NoError(t, err)
	sqlDB, err := db.GetDB().DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(8)
	require.NoError(t, db.GetDB().Exec("PRAGMA journal_mode=WAL").Error)
	manager := NewSubscriptionManagement(svc, managementTestAuthorization)
	const requests = 8
	start := make(chan struct{})
	errs := make(chan error, requests)
	var wg sync.WaitGroup
	for i := range requests {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			var err error
			if i%2 == 0 {
				_, err = manager.Renew(ctx, 1)
			} else {
				_, err = svc.RenewSubscription(ctx, created.Subscription.ID, 1)
			}
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	stored, err := db.GetByID(ctx, created.Subscription.ID)
	require.NoError(t, err)
	assertRenewalIdentity(t, created.Subscription, stored)
	assert.True(t, terms.ExpiresAt.UTC().AddDate(0, 0, requests).Equal(*stored.ExpiresAt))
}

type mismatchedCustomerRepository struct {
	*database.Service
	sub *database.Subscription
}

func (r *mismatchedCustomerRepository) GetCustomerSubscription(context.Context, int64) (*database.Subscription, bool, error) {
	return r.sub, true, nil
}

func TestSubscriptionManagement_RejectsMismatchedRepositoryIdentity(t *testing.T) {
	db, svc, terms := customerLifecycleFixture(t)
	created, err := svc.Create(context.Background(), 90801, "customer", "", terms)
	require.NoError(t, err)
	svc.db = &mismatchedCustomerRepository{Service: db, sub: created.Subscription}
	manager := NewSubscriptionManagement(svc, managementTestAuthorization)
	info, err := manager.Current(managementContext(90802, 0))
	require.ErrorIs(t, err, ErrSubscriptionAccessDenied)
	assert.Nil(t, info)
	assert.NotContains(t, err.Error(), created.Subscription.Token)
}
