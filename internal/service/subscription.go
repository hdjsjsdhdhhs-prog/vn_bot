// Package service contains subscription, payment, trial and admin business logic.
package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/interfaces"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/metrics"
	"github.com/kereal/rs8kvn_bot/internal/utils"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// SubscriptionService coordinates subscription persistence, cache
// invalidation, metrics, and referral/trial lifecycle operations.
//
// Access is purely local state: a subscription row (status, plan, expiry,
// token) is everything the /sub endpoint needs. The content is assembled on
// request by the SubscriptionBuilder of the subscription or its plan from the
// ProviderSource catalogues — no client is registered on any VPN panel.
type SubscriptionService struct {
	db                interfaces.DatabaseService
	reminderRepo      interfaces.SubscriptionReminderRepository
	cfg               *config.Config
	invalidate        func(telegramID int64)
	invalidateBySubID func(subID string)
	bot               interfaces.BotAPI
	trialPolicy       TrialPolicy
}

// TrialPolicy is the admin-editable trial configuration (database.Service).
// Without one the trial keeps the environment configuration: always enabled,
// TRIAL_DURATION_HOURS, TRIAL_RATE_LIMIT.
type TrialPolicy interface {
	GetTrialEffective(ctx context.Context, d database.TrialDefaults) (*database.TrialEffective, error)
	CheckTrialIssuance(ctx context.Context, d database.TrialDefaults) (*database.TrialIssuance, error)
}

var _ TrialPolicy = (*database.Service)(nil)

// SetTrialPolicy links the admin trial configuration. Set before serving.
func (s *SubscriptionService) SetTrialPolicy(p TrialPolicy) {
	s.trialPolicy = p
}

// TrialDefaults are the environment trial values (used while nothing is stored).
func (s *SubscriptionService) TrialDefaults() database.TrialDefaults {
	if s.cfg == nil {
		return database.TrialDefaults{}
	}
	return database.TrialDefaults{DurationHours: s.cfg.TrialDurationHours, RateLimitPerHour: s.cfg.TrialRateLimit}
}

func (s *SubscriptionService) defaultTrialOffer() database.TrialEffective {
	d := s.TrialDefaults()
	return database.TrialEffective{Enabled: true, DurationHours: d.DurationHours, RateLimitPerHour: d.RateLimitPerHour, Features: []string{}}
}

// TrialOffer returns the trial offer in force (switch, duration, IP limit and
// landing-page texts) without checking whether a trial can be issued.
func (s *SubscriptionService) TrialOffer(ctx context.Context) (database.TrialEffective, error) {
	if s.trialPolicy == nil {
		return s.defaultTrialOffer(), nil
	}
	eff, err := s.trialPolicy.GetTrialEffective(ctx, s.TrialDefaults())
	if err != nil {
		return s.defaultTrialOffer(), fmt.Errorf("load trial settings: %w", err)
	}
	return *eff, nil
}

// CheckTrialIssuance returns the offer in force and whether a new trial may be
// issued now: database.ErrTrialDisabled or database.ErrTrialUnavailable
// otherwise. It is the same evaluation as the admin preview.
func (s *SubscriptionService) CheckTrialIssuance(ctx context.Context) (database.TrialEffective, error) {
	if s.trialPolicy == nil {
		return s.defaultTrialOffer(), nil
	}
	issuance, err := s.trialPolicy.CheckTrialIssuance(ctx, s.TrialDefaults())
	if issuance == nil {
		if err == nil {
			err = errors.New("trial issuance check returned no result")
		}
		return s.defaultTrialOffer(), err
	}
	return issuance.Settings, err
}

// CreateResult contains the subscription and referral information produced by
// creating or recovering a user subscription.
type CreateResult struct {
	Subscription    *database.Subscription
	SubscriptionURL string
	ReferrerTGID    int64
}

// SubscriptionUsername returns the stored display username of a subscription:
// the Telegram username when it is a real one, otherwise "tgId_<id>".
func SubscriptionUsername(username string, telegramID int64) string {
	if utils.IsRealUsername(username) {
		return username
	}

	return fmt.Sprintf("tgId_%d", telegramID)
}

// NewSubscriptionService creates a SubscriptionService over the given database
// and application configuration.
func NewSubscriptionService(db interfaces.DatabaseService, cfg *config.Config) *SubscriptionService {
	return &SubscriptionService{
		db:           db,
		reminderRepo: db,
		cfg:          cfg,
	}
}

// SetBot links the Telegram bot client to the subscription service.
func (s *SubscriptionService) SetBot(bot interfaces.BotAPI) {
	s.bot = bot
}

// Create is the application creation entry point. CustomerSubscriptionTerms
// creates finite ProviderSource-backed access; omitting terms keeps the
// free-plan get-or-create flow used by the bot and existing orders.
// inviteCode, when non-empty, is resolved atomically inside the DB transaction
// and persisted in sub.InviteCode / sub.ReferredBy. The resolved ReferrerTGID
// (0 if unset) is returned in CreateResult so callers can update aggregate
// referral state. The subscription is served by its plan builder on /sub.
func (s *SubscriptionService) Create(ctx context.Context, telegramID int64, username, inviteCode string, terms ...CustomerSubscriptionTerms) (*CreateResult, error) {
	if len(terms) > 1 {
		return nil, errors.New("only one set of customer subscription terms is allowed")
	}
	if len(terms) == 1 {
		return s.createCustomer(ctx, telegramID, username, inviteCode, terms[0])
	}
	username = SubscriptionUsername(username, telegramID)

	existing, err := s.db.GetByTelegramID(ctx, telegramID)
	if err == nil {
		if existing.ProviderSourceID != nil && !existing.IsActive() {
			return nil, ErrSubscriptionInactive
		}

		return &CreateResult{
			Subscription:    existing,
			SubscriptionURL: s.cfg.SubURL(existing.Token),
			ReferrerTGID:    referrerOf(existing),
		}, nil
	}

	if !errors.Is(err, database.ErrSubscriptionNotFound) && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("lookup subscription: %w", err)
	}

	// No active subscription. If a non-active one exists (e.g. left "revoked"
	// by an admin action), reanimate it instead of inserting a duplicate row
	// that would violate telegram_id uniqueness.
	existingAny, anyErr := s.db.GetAnyByTelegramID(ctx, telegramID)
	if anyErr == nil {
		reanimated, reErr := s.reanimateRevokedSubscription(ctx, existingAny, inviteCode)
		if reErr != nil {
			return nil, fmt.Errorf("reanimate subscription: %w", reErr)
		}

		return &CreateResult{
			Subscription:    reanimated,
			SubscriptionURL: s.cfg.SubURL(reanimated.Token),
			ReferrerTGID:    referrerOf(reanimated),
		}, nil
	}

	if !errors.Is(anyErr, database.ErrSubscriptionNotFound) && !errors.Is(anyErr, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("lookup subscription (any status): %w", anyErr)
	}

	plan, err := s.db.GetPlanByName(ctx, database.FreePlanName)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve free plan: %w", err)
	}

	return s.createNewSubscription(ctx, telegramID, username, inviteCode, plan.ID, nil, nil, nil)
}

func referrerOf(sub *database.Subscription) int64 {
	if sub == nil || sub.ReferredBy == nil {
		return 0
	}
	return *sub.ReferredBy
}

// reanimateRevokedSubscription recovers a subscription left in a non-active
// state (e.g. "revoked"), turning it back into an active free-plan
// subscription. This avoids creating a second row for the same telegram_id
// (which would violate the telegram_id uniqueness expectation and could
// otherwise permanently block the user from re-subscribing).
func (s *SubscriptionService) reanimateRevokedSubscription(ctx context.Context, sub *database.Subscription, inviteCode string) (*database.Subscription, error) {
	// Opening the free flow must not revive paid provider access.
	if sub.ProviderSourceID != nil {
		return nil, ErrSubscriptionInactive
	}
	freePlan, err := s.db.GetPlanByName(ctx, database.FreePlanName)
	if err != nil {
		return nil, fmt.Errorf("resolve free plan: %w", err)
	}

	if inviteCode != "" {
		inv, err := s.db.GetInviteByCode(ctx, inviteCode)
		if err == nil {
			inviteVal := inviteCode
			sub.InviteCode = &inviteVal
			referredBy := inv.ReferrerTGID
			sub.ReferredBy = &referredBy
		} else if !errors.Is(err, database.ErrInviteNotFound) {
			return nil, fmt.Errorf("resolve invite: %w", err)
		}
	} else {
		sub.InviteCode = nil
		sub.ReferredBy = nil
	}

	before := database.JournalStateOf(sub)
	sub.PlanID = freePlan.ID
	sub.Status = string(database.SubscriptionStatusActive)
	sub.ExpiresAt = nil
	sub.ProductID = nil
	sub.StartedAt = nil
	sub.PricePaidCents = 0
	sub.Currency = nil
	sub.Devices = "[]"
	sub.Ips = "[]"

	err = s.updateSubscriptionJournaled(ctx, sub, database.JournalRecord{
		Type: database.JournalSubscriptionReconnected, Actor: database.JournalActorUser,
		Description: "Подписка подключена повторно на бесплатном тарифе",
		Before:      before, After: database.JournalStateOf(sub),
	})
	if err != nil {
		return nil, fmt.Errorf("reanimate subscription: %w", err)
	}

	if s.invalidateBySubID != nil && sub.SubscriptionID != "" {
		s.invalidateBySubID(sub.SubscriptionID)
	}

	metrics.SubscriptionCreatesTotal.Inc()

	s.RefreshActiveSubscriptionsMetric(ctx)

	return sub, nil
}

// journalUpdater is implemented by *database.Service: the subscription update
// and its journal event commit in one transaction.
type journalUpdater interface {
	UpdateSubscriptionWithJournal(ctx context.Context, sub *database.Subscription, rec database.JournalRecord) error
}

// updateSubscriptionJournaled persists a lifecycle change together with its
// journal event. Repositories without journal support (test doubles) fall back
// to the plain update, so the lifecycle behaviour is identical either way.
func (s *SubscriptionService) updateSubscriptionJournaled(ctx context.Context, sub *database.Subscription, rec database.JournalRecord) error {
	if repo, ok := s.db.(journalUpdater); ok {
		return repo.UpdateSubscriptionWithJournal(ctx, sub, rec)
	}
	return s.db.UpdateSubscription(ctx, sub)
}

// DowngradeToFreePlan resets a subscription to the free plan. Used when a paid
// order is chargebacked: the user loses premium access but retains the free
// tier. From the next /sub request on the free plan's builder serves it.
// Returns the updated subscription.
func (s *SubscriptionService) DowngradeToFreePlan(ctx context.Context, sub *database.Subscription) (*database.Subscription, error) {
	if sub == nil {
		return nil, errors.New("downgrade: subscription is nil")
	}
	if sub.ProviderSourceID != nil {
		return nil, errors.New("provider subscriptions cannot be downgraded to perpetual free access")
	}

	freePlan, err := s.db.GetPlanByName(ctx, database.FreePlanName)
	if err != nil {
		return nil, fmt.Errorf("downgrade: resolve free plan: %w", err)
	}

	// Reset subscription to free-plan defaults.
	before := database.JournalStateOf(sub)
	sub.PlanID = freePlan.ID
	sub.Status = string(database.SubscriptionStatusActive)
	sub.ExpiresAt = nil
	sub.ProductID = nil
	sub.StartedAt = nil
	sub.PricePaidCents = 0
	sub.Currency = nil

	err = s.updateSubscriptionJournaled(ctx, sub, database.JournalRecord{
		Type: database.JournalFreeActivated, Actor: database.JournalActorSystem,
		Description: "Подписка переведена на бесплатный тариф",
		Before:      before, After: database.JournalStateOf(sub),
	})
	if err != nil {
		return nil, fmt.Errorf("downgrade: update subscription: %w", err)
	}

	if s.invalidateBySubID != nil && sub.SubscriptionID != "" {
		s.invalidateBySubID(sub.SubscriptionID)
	}

	s.RefreshActiveSubscriptionsMetric(ctx)

	return sub, nil
}

// revokeThenDelete runs the two-phase subscription teardown used by DeleteByID:
// mark revoked (journaled; /sub answers 404 from that moment) → physically
// delete the DB row → invalidate the caches. There is no external VPN state to
// deprovision: access exists only as this row. Returns the deleted
// subscription (nil on error).
func (s *SubscriptionService) revokeThenDelete(ctx context.Context, sub *database.Subscription) (*database.Subscription, error) {
	// Phase 1: mark revoked before the delete.
	before := database.JournalStateOf(sub)
	sub.Status = string(database.SubscriptionStatusRevoked)

	// The journal row outlives the physical delete below.
	err := s.updateSubscriptionJournaled(ctx, sub, database.JournalRecord{
		Type: database.JournalRevoked, Actor: database.JournalActorAdmin, ActorName: "bot",
		Description: "Подписка отозвана и удалена администратором",
		Before:      before, After: database.JournalStateOf(sub),
	})
	if err != nil {
		return nil, fmt.Errorf("mark revoked: %w", err)
	}

	// Phase 2: physical delete. On failure the row stays revoked (no access).
	_, err = s.db.DeleteSubscriptionByID(ctx, sub.ID)
	if err != nil {
		return nil, fmt.Errorf("db delete: %w", err)
	}

	if s.invalidateBySubID != nil && sub.SubscriptionID != "" {
		s.InvalidateBySubID(ctx, sub.SubscriptionID)
	}

	s.RefreshActiveSubscriptionsMetric(ctx)

	return sub, nil
}

// DeleteByID is the ONLY real (hard) subscription deletion in the product. It is
// used by the admin /del command. The two-phase teardown is owned by
// revokeThenDelete. Everything else only changes subscription status.
func (s *SubscriptionService) DeleteByID(ctx context.Context, id uint) (*database.Subscription, error) {
	sub, err := s.db.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get subscription: %w", err)
	}

	return s.revokeThenDelete(ctx, sub)
}

// adminSetPlanDefaultDays is the default expiry window (in days) applied when an
// admin changes a plan without an explicit duration and the subscription has no
// future expiry to preserve.
const adminSetPlanDefaultDays = 30

// AdminSetPlanMaxDays is the upper bound for the explicit duration in /setplan.
// It guards against typos (e.g. an extra zero) silently extending a
// subscription for centuries.
const AdminSetPlanMaxDays = 3650

// AdminSetPlan changes a subscription's plan through the service layer
// (admin override, no payment involved). It mirrors the payment activation
// flow: update the subscription row (plan, status, expiry, reminder state),
// invalidate caches and refresh metrics. The new plan's builder serves the
// subscription from the next /sub request on. Returns the updated subscription.
//
// days, when > 0, sets ExpiresAt = now + days. When 0, an existing future
// expiry is preserved; otherwise the subscription is given a fresh
// adminSetPlanDefaultDays window. Setting the free plan clears ExpiresAt
// (бессрочная), mirroring DowngradeToFreePlan.
func (s *SubscriptionService) AdminSetPlan(ctx context.Context, subscriptionID, planID uint, days int) (*database.Subscription, error) {
	if days > AdminSetPlanMaxDays {
		return nil, fmt.Errorf("admin set plan: days %d exceeds maximum %d", days, AdminSetPlanMaxDays)
	}

	sub, err := s.db.GetByID(ctx, subscriptionID)
	if err != nil {
		return nil, fmt.Errorf("admin set plan: load subscription: %w", err)
	}

	plan, err := s.db.GetPlanByID(ctx, planID)
	if err != nil {
		return nil, fmt.Errorf("admin set plan: load plan: %w", err)
	}

	if sub.ProviderSourceID != nil && plan.Name == database.FreePlanName {
		return nil, errors.New("provider subscriptions require a finite expiry; free downgrade is unsupported")
	}
	now := time.Now().UTC()
	before := database.JournalStateOf(sub)
	if plan.Name == database.FreePlanName {
		// Free plan: clear expiry and paid state, mirroring DowngradeToFreePlan.
		// Leaving ProductID/PricePaidCents behind would keep the subscription
		// looking "paid" (renewal vs first-purchase detection in the payment
		// flow) while it is on the free tier.
		sub.ExpiresAt = nil
		sub.ProductID = nil
		sub.StartedAt = nil
		sub.PricePaidCents = 0
		sub.Currency = nil
	} else {
		switch {
		case days > 0:
			expiry := now.AddDate(0, 0, days)
			sub.ExpiresAt = &expiry
		case sub.ExpiresAt == nil || !sub.ExpiresAt.After(now):
			expiry := now.AddDate(0, 0, adminSetPlanDefaultDays)
			sub.ExpiresAt = &expiry
		}

		if sub.StartedAt == nil {
			started := now
			sub.StartedAt = &started
		}
	}

	sub.PlanID = plan.ID
	sub.Status = string(database.SubscriptionStatusActive)

	// Reset the reminder bitmask so the expiry-reminder cycle restarts for the
	// new expiry (mirrors ConfirmOrderPaidCAS).
	sub.RemindersSent = 0

	journal := database.JournalRecord{
		Type: database.JournalPlanChanged, Actor: database.JournalActorAdmin, ActorName: "bot",
		Description: fmt.Sprintf("Администратор назначил тариф «%s»", plan.Name),
		Before:      before, After: database.JournalStateOf(sub), Extra: map[string]any{"days": days},
	}
	if plan.Name == database.FreePlanName {
		journal.Type = database.JournalFreeActivated
		journal.Description = "Администратор перевёл подписку на бесплатный тариф"
	}
	err = s.updateSubscriptionJournaled(ctx, sub, journal)
	if err != nil {
		return nil, fmt.Errorf("admin set plan: update subscription: %w", err)
	}

	if s.invalidate != nil && sub.TelegramID > 0 {
		s.invalidate(sub.TelegramID)
	}
	if s.invalidateBySubID != nil && sub.SubscriptionID != "" {
		s.invalidateBySubID(sub.SubscriptionID)
	}

	s.RefreshActiveSubscriptionsMetric(ctx)

	return sub, nil
}

// TrialCreateResult holds the outcome of a trial creation, including the
// generated identifiers and public subscription URL.
type TrialCreateResult struct {
	Subscription    *database.Subscription
	SubscriptionURL string
	SubID           string
	ClientID        string
}

// CreateTrial issues a new anonymous trial subscription: the issuance check of
// the admin trial configuration (switch and trial plan builder), then one DB
// row on the trial plan with a negative telegram_id (unactivated), its token
// and expiry. The trial plan builder serves it on /sub; nothing is registered
// on a VPN panel.
func (s *SubscriptionService) CreateTrial(ctx context.Context, inviteCode string) (*TrialCreateResult, error) {
	// A switched-off or broken trial configuration never issues a subscription
	// (no row). Already issued trials are not affected.
	offer, err := s.CheckTrialIssuance(ctx)
	if err != nil {
		return nil, fmt.Errorf("trial issuance: %w", err)
	}

	subID, err := utils.GenerateSubID()
	if err != nil {
		return nil, fmt.Errorf("generate sub id: %w", err)
	}

	clientID, err := utils.GenerateUUID()
	if err != nil {
		return nil, fmt.Errorf("generate client id: %w", err)
	}

	expiryTime := time.Now().UTC().Add(time.Duration(offer.DurationHours) * time.Hour)

	sub, err := s.db.CreateTrialSubscription(ctx, inviteCode, subID, clientID, expiryTime)
	if err != nil {
		return nil, fmt.Errorf("create trial subscription: %w", err)
	}

	result := &TrialCreateResult{
		Subscription:    sub,
		SubscriptionURL: s.cfg.SubURL(sub.Token),
		SubID:           subID,
		ClientID:        clientID,
	}

	s.RefreshActiveSubscriptionsMetric(ctx)

	return result, nil
}

// BindTrial binds an anonymous trial subscription to a Telegram user. The DB
// binding (race-safe on telegram_id < 0) is the whole operation: it moves the
// row onto the free plan, so the free plan builder serves it from then on.
func (s *SubscriptionService) BindTrial(ctx context.Context, subscriptionID string, telegramID int64, username string) (*database.Subscription, error) {
	username = SubscriptionUsername(username, telegramID)

	sub, err := s.db.BindTrialSubscription(ctx, subscriptionID, telegramID, username)
	if err != nil {
		return nil, fmt.Errorf("bind trial subscription: %w", err)
	}

	if s.invalidateBySubID != nil {
		s.invalidateBySubID(subscriptionID)
	}
	s.RefreshActiveSubscriptionsMetric(ctx)

	return sub, nil
}

// RefreshActiveSubscriptionsMetric updates active, premium, and trial subscription gauges.
func (s *SubscriptionService) RefreshActiveSubscriptionsMetric(ctx context.Context) {
	count, err := s.db.CountActiveSubscriptions(ctx)

	if err != nil {
		logger.Warn("failed to refresh active subscriptions metric", zap.Error(err))
	} else {
		metrics.ActiveSubscriptions.Set(float64(count))
	}

	premiumCount, err := s.db.CountPremiumSubscriptions(ctx)
	if err != nil {
		logger.Warn("failed to refresh premium subscriptions metric", zap.Error(err))
	} else {
		metrics.PremiumSubscriptions.Set(float64(premiumCount))
	}

	freeCount, err := s.db.CountFreeSubscriptions(ctx)
	if err != nil {
		logger.Warn("failed to refresh free subscriptions metric", zap.Error(err))
	} else {
		metrics.FreeSubscriptions.Set(float64(freeCount))
	}

	trialCount, err := s.db.CountTrialSubscriptions(ctx)
	if err != nil {
		logger.Warn("failed to refresh trial subscriptions metric", zap.Error(err))
	} else {
		metrics.TrialSubscriptions.Set(float64(trialCount))
	}
}

// SetInvalidateFunc sets the cache invalidation callback.
func (s *SubscriptionService) SetInvalidateFunc(fn func(telegramID int64)) {
	s.invalidate = fn
}

// InvalidateSubscription clears cached subscription data for the given Telegram ID.
// It is safe to call from any goroutine.
func (s *SubscriptionService) InvalidateSubscription(ctx context.Context, telegramID int64) {
	if s.invalidate != nil {
		s.invalidate(telegramID)
	}
}

// SetInvalidateBySubIDFunc sets the cache invalidation callback keyed by subscription ID.
// Used for trial and other subscriptions where TelegramID may be unavailable.
func (s *SubscriptionService) SetInvalidateBySubIDFunc(fn func(subID string)) {
	s.invalidateBySubID = fn
}

// InvalidateBySubID clears cached subscription data for the given subscription ID.
// It is safe to call from any goroutine.
func (s *SubscriptionService) InvalidateBySubID(ctx context.Context, subID string) {
	if s.invalidateBySubID != nil {
		s.invalidateBySubID(subID)
	}
}

// CleanupExpiredTrials claims expired anonymous trials and deletes their rows
// (anonymous trials are not real user subscriptions, see the deletion policy).
// A failed delete leaves the claimed row for the next run.
func (s *SubscriptionService) CleanupExpiredTrials(ctx context.Context) (int64, error) {
	subs, err := s.db.ClaimExpiredTrials(ctx, s.cfg.TrialDurationHours)
	if err != nil {
		return 0, err
	}

	var successCount int64
	var errs []error

	for _, sub := range subs {
		if sub.SubscriptionID == "" {
			continue
		}

		err = s.db.DeleteClaimedTrial(ctx, sub.ID)
		if err != nil {
			errs = append(errs, fmt.Errorf("delete expired trial %s: %w", sub.SubscriptionID, err))
			continue
		}

		successCount++
		if s.invalidateBySubID != nil {
			s.invalidateBySubID(sub.SubscriptionID)
		}
	}

	if len(errs) > 0 {
		return successCount, errors.Join(errs...)
	}

	return successCount, nil
}

// GetOrCreateSubscription returns an existing subscription or creates a new free-plan one.
func (s *SubscriptionService) GetOrCreateSubscription(ctx context.Context, telegramID int64, username, inviteCode string) (*database.Subscription, error) {
	result, err := s.Create(ctx, telegramID, username, inviteCode)
	if err != nil {
		return nil, err
	}
	return result.Subscription, nil
}

type expiryRepository interface {
	ExpireSubscriptionWithPlanCAS(ctx context.Context, subscriptionID, planID uint) error
}

// ExpireSubscription expires provider access or downgrades plan access to Free.
func (s *SubscriptionService) ExpireSubscription(ctx context.Context, subscriptionID uint) error {
	sub, err := s.db.GetByID(ctx, subscriptionID)
	if err != nil {
		return fmt.Errorf("get subscription: %w", err)
	}
	if sub.ProviderSourceID != nil {
		repo, ok := s.db.(interface {
			ExpireProviderSubscription(context.Context, uint) error
		})
		if !ok {
			return errors.New("provider expiry repository is not configured")
		}
		if err := repo.ExpireProviderSubscription(ctx, sub.ID); err != nil {
			return err
		}
		s.InvalidateSubscription(ctx, sub.TelegramID)
		s.InvalidateBySubID(ctx, sub.SubscriptionID)
		return nil
	}

	freePlan, err := s.db.GetPlanByName(ctx, database.FreePlanName)
	if err != nil {
		return fmt.Errorf("resolve free plan: %w", err)
	}

	if repo, ok := s.db.(expiryRepository); ok {
		// The current expiry is checked atomically: a queued scan must never
		// undo a committed renewal.
		err = repo.ExpireSubscriptionWithPlanCAS(ctx, sub.ID, freePlan.ID)
	} else {
		err = s.db.ExpireSubscription(ctx, sub.ID, freePlan.ID)
	}
	if err != nil {
		return fmt.Errorf("expire subscription: %w", err)
	}

	if s.invalidateBySubID != nil && sub.SubscriptionID != "" {
		s.invalidateBySubID(sub.SubscriptionID)
	}

	return nil
}
