package database

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Warm-up runtime persistence: trigger anchors (the lifecycle events a
// scenario is scheduled from), user facts for scenario conditions, the
// delivery journal (also the duplicate guard and the worker state) and the
// personal coupons issued with a message.

// Delivery statuses (schema CHECK mirrors them).
const (
	WarmupDeliverySending = "sending"
	WarmupDeliverySent    = "sent"
	WarmupDeliveryRetry   = "retry"
	WarmupDeliveryFailed  = "failed"
	WarmupDeliveryBlocked = "blocked"
)

// Coupon statuses. "expired" is never stored: it is derived from expires_at.
const (
	WarmupCouponIssued   = "issued"
	WarmupCouponRedeemed = "redeemed"
	WarmupCouponRevoked  = "revoked"
	WarmupCouponExpired  = "expired"
)

// warmupTrialMaxDuration bounds how long after its bind a trial can end: a
// trial is bound before it expires and lasts at most TrialMaxDurationHours.
const warmupTrialMaxDuration = TrialMaxDurationHours * time.Hour

// warmupErrorMaxLen bounds the stored Telegram error text.
const warmupErrorMaxLen = 500

var (
	ErrWarmupDeliveryNotFound = errors.New("warmup delivery not found")
	ErrWarmupDeliveryStale    = errors.New("warmup delivery is not being sent")
	ErrWarmupCouponCodeTaken  = errors.New("warmup coupon code is already used")
)

// ---------------------------------------------------------------------------
// Trigger anchors
// ---------------------------------------------------------------------------

// WarmupAnchor is one lifecycle event of one Telegram user that a scenario
// can be scheduled from. AnchorKey identifies the event per user, so the
// delivery UNIQUE(scenario_id, telegram_id, anchor_key) sends each scenario
// at most once per event.
type WarmupAnchor struct {
	TelegramID     int64
	Username       string
	SubscriptionID uint
	AnchorKey      string
	AnchorAt       time.Time
	// PlanName and ExpiresAt describe the subscription term of the event
	// (expiry triggers) or the trial end (trial triggers).
	PlanName  string
	ExpiresAt *time.Time
}

// WarmupAnchorPage is one page of anchors. Next is the cursor of the next
// page (0: no more pages).
type WarmupAnchorPage struct {
	Anchors []WarmupAnchor
	Next    int64
}

func warmupPageLimit(limit int) int {
	if limit <= 0 || limit > 1000 {
		return 500
	}
	return limit
}

// WarmupContactsStartedBetween returns the users whose first live /start is in
// [from, to]. Backfilled (historical) and blocked contacts are excluded. The
// cursor is the Telegram ID.
func (s *Service) WarmupContactsStartedBetween(ctx context.Context, from, to time.Time, cursor int64, limit int) (*WarmupAnchorPage, error) {
	limit = warmupPageLimit(limit)
	var contacts []WarmupContact
	err := s.db.WithContext(ctx).
		Where("source = ? AND blocked_at IS NULL AND first_seen_at >= ? AND first_seen_at <= ? AND telegram_id > ?",
			WarmupContactSourceStart, from.UTC(), to.UTC(), cursor).
		Order("telegram_id ASC").Limit(limit).Find(&contacts).Error
	if err != nil {
		return nil, fmt.Errorf("list started warmup contacts: %w", err)
	}
	page := &WarmupAnchorPage{Anchors: make([]WarmupAnchor, 0, len(contacts))}
	for _, c := range contacts {
		page.Anchors = append(page.Anchors, WarmupAnchor{
			TelegramID: c.TelegramID, Username: c.Username, AnchorKey: "start", AnchorAt: c.FirstSeenAt.UTC(),
		})
	}
	if len(contacts) == limit {
		page.Next = contacts[len(contacts)-1].TelegramID
	}
	return page, nil
}

// warmupJournalEvents pages journal events of one type with created_at in
// [from, to] and a linked Telegram user. The cursor is the event ID.
func (s *Service) warmupJournalEvents(ctx context.Context, eventType JournalEventType, from, to time.Time, cursor int64, limit int) ([]JournalEvent, int64, error) {
	var events []JournalEvent
	err := s.db.WithContext(ctx).
		Where("event_type = ? AND outcome = ? AND telegram_id > 0 AND created_at >= ? AND created_at <= ? AND id > ?",
			string(eventType), JournalOutcomeSuccess, from.UTC(), to.UTC(), cursor).
		Order("id ASC").Limit(limit).Find(&events).Error
	if err != nil {
		return nil, 0, fmt.Errorf("list %s journal events: %w", eventType, err)
	}
	var next int64
	if len(events) == limit {
		next = int64(events[len(events)-1].ID) // #nosec G115 -- SQLite row ids are positive int64 values
	}
	return events, next, nil
}

// journalBeforeExpiry reads details.before.expires_at of a journal event.
func journalBeforeExpiry(e *JournalEvent) *time.Time {
	var details struct {
		Before *JournalState `json:"before"`
	}
	err := json.Unmarshal([]byte(e.Details), &details)
	if err != nil || details.Before == nil || details.Before.ExpiresAt == nil {
		return nil
	}
	at := details.Before.ExpiresAt.UTC()
	return &at
}

func warmupSubscriptionID(e *JournalEvent) uint {
	if e.SubscriptionID == nil {
		return 0
	}
	return *e.SubscriptionID
}

// WarmupTrialsActivatedBetween returns trials activated in Telegram (bound to
// a user, journal trial_bound) in [from, to]. ExpiresAt is the trial end the
// trial had when it was bound.
func (s *Service) WarmupTrialsActivatedBetween(ctx context.Context, from, to time.Time, cursor int64, limit int) (*WarmupAnchorPage, error) {
	events, next, err := s.warmupJournalEvents(ctx, JournalTrialBound, from, to, cursor, warmupPageLimit(limit))
	if err != nil {
		return nil, err
	}
	page := &WarmupAnchorPage{Anchors: make([]WarmupAnchor, 0, len(events)), Next: next}
	for i := range events {
		e := &events[i]
		subID := warmupSubscriptionID(e)
		page.Anchors = append(page.Anchors, WarmupAnchor{
			TelegramID: e.TelegramID, Username: e.Username, SubscriptionID: subID,
			AnchorKey: "trial:" + strconv.FormatUint(uint64(subID), 10), AnchorAt: e.CreatedAt.UTC(),
			ExpiresAt: journalBeforeExpiry(e),
		})
	}
	return page, nil
}

// WarmupTrialPeriodEndsBetween returns Telegram trials whose trial period ends
// in [from, to]. Binding converts the trial into the free plan, so the end is
// the expiry the trial had when it was bound (details.before.expires_at of
// trial_bound). A bind happens before the trial expires and a trial lasts at
// most TrialMaxDurationHours, which bounds the scanned binds.
func (s *Service) WarmupTrialPeriodEndsBetween(ctx context.Context, from, to time.Time, cursor int64, limit int) (*WarmupAnchorPage, error) {
	from, to = from.UTC(), to.UTC()
	events, next, err := s.warmupJournalEvents(ctx, JournalTrialBound, from.Add(-warmupTrialMaxDuration), to, cursor, warmupPageLimit(limit))
	if err != nil {
		return nil, err
	}
	page := &WarmupAnchorPage{Anchors: []WarmupAnchor{}, Next: next}
	for i := range events {
		e := &events[i]
		end := journalBeforeExpiry(e)
		if end == nil || end.Before(from) || end.After(to) {
			continue
		}
		subID := warmupSubscriptionID(e)
		page.Anchors = append(page.Anchors, WarmupAnchor{
			TelegramID: e.TelegramID, Username: e.Username, SubscriptionID: subID,
			AnchorKey: "trial_end:" + strconv.FormatUint(uint64(subID), 10), AnchorAt: *end, ExpiresAt: end,
		})
	}
	return page, nil
}

// warmupExpiryKey identifies one paid term of a subscription: a renewal moves
// the expiry and therefore starts a new key.
func warmupExpiryKey(subID uint, expiresAt time.Time) string {
	return "expiry:" + strconv.FormatUint(uint64(subID), 10) + ":" + strconv.FormatInt(expiresAt.UTC().Unix(), 10)
}

// WarmupPaidExpiringBetween returns active paid subscriptions of linked users
// whose expiry is in [from, to]. The cursor is the subscription ID.
func (s *Service) WarmupPaidExpiringBetween(ctx context.Context, from, to time.Time, cursor int64, limit int) (*WarmupAnchorPage, error) {
	limit = warmupPageLimit(limit)
	type row struct {
		ID         uint
		TelegramID int64
		Username   string
		ExpiresAt  time.Time
		PlanName   string
	}
	var rows []row
	err := s.db.WithContext(ctx).Table("subscriptions s").
		Select("s.id, s.telegram_id, s.username, s.expires_at, p.name AS plan_name").
		Joins("JOIN plans p ON p.id = s.plan_id").
		Where("s.status = ? AND s.telegram_id > 0 AND s.expires_at IS NOT NULL AND s.expires_at >= ? AND s.expires_at <= ? AND p.name NOT IN ? AND s.id > ?",
			string(SubscriptionStatusActive), from.UTC(), to.UTC(), []string{FreePlanName, TrialPlanName}, cursor).
		Order("s.id ASC").Limit(limit).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list expiring paid subscriptions: %w", err)
	}
	page := &WarmupAnchorPage{Anchors: make([]WarmupAnchor, 0, len(rows))}
	for _, r := range rows {
		expires := r.ExpiresAt.UTC()
		page.Anchors = append(page.Anchors, WarmupAnchor{
			TelegramID: r.TelegramID, Username: r.Username, SubscriptionID: r.ID,
			AnchorKey: warmupExpiryKey(r.ID, expires), AnchorAt: expires, PlanName: r.PlanName, ExpiresAt: &expires,
		})
	}
	if len(rows) == limit {
		page.Next = int64(rows[len(rows)-1].ID) // #nosec G115 -- SQLite row ids are positive int64 values
	}
	return page, nil
}

// WarmupPaidExpiredBetween returns paid terms that ended (journal
// subscription_expired) in [from, to]. The anchor is the moment access ended;
// ExpiresAt is the term's expiry and PlanName the plan that expired.
func (s *Service) WarmupPaidExpiredBetween(ctx context.Context, from, to time.Time, cursor int64, limit int) (*WarmupAnchorPage, error) {
	events, next, err := s.warmupJournalEvents(ctx, JournalExpired, from, to, cursor, warmupPageLimit(limit))
	if err != nil {
		return nil, err
	}
	page := &WarmupAnchorPage{Anchors: []WarmupAnchor{}, Next: next}
	for i := range events {
		e := &events[i]
		if e.PlanKind != PlanKindPaid {
			continue
		}
		subID := warmupSubscriptionID(e)
		expires := journalBeforeExpiry(e)
		key := "expired:" + strconv.FormatUint(uint64(e.ID), 10)
		if expires != nil {
			key = warmupExpiryKey(subID, *expires)
		}
		page.Anchors = append(page.Anchors, WarmupAnchor{
			TelegramID: e.TelegramID, Username: e.Username, SubscriptionID: subID, AnchorKey: key,
			AnchorAt: e.CreatedAt.UTC(), PlanName: e.PlanName, ExpiresAt: expires,
		})
	}
	return page, nil
}

// ---------------------------------------------------------------------------
// User facts (scenario conditions)
// ---------------------------------------------------------------------------

// WarmupUserFacts is what scenario conditions are evaluated against.
type WarmupUserFacts struct {
	TelegramID   int64
	Username     string
	LanguageCode string
	// Blocked: the user blocked the bot (excluded from every scenario).
	Blocked bool
	// HasSubscription: any subscription row in the bot (any status). Such a
	// user cannot bind a trial: telegram_id is unique.
	HasSubscription bool
	// HadTrial: the user activated a trial in Telegram (trial_bound).
	HadTrial bool
	// HasActivePaid: an active paid plan that has not expired yet.
	HasActivePaid bool
	// EverPaid: at least one successful payment.
	EverPaid bool
	// Connected: the subscription was fetched by a VPN client at least once.
	Connected bool
	// PlanName and ExpiresAt describe the current subscription, if any.
	PlanName  string
	ExpiresAt *time.Time
}

// GetWarmupUserFacts collects the facts of one Telegram user at now.
func (s *Service) GetWarmupUserFacts(ctx context.Context, telegramID int64, now time.Time) (*WarmupUserFacts, error) {
	db := s.db.WithContext(ctx)
	facts := &WarmupUserFacts{TelegramID: telegramID}

	contact, err := s.GetWarmupContact(ctx, telegramID)
	if err != nil {
		return nil, err
	}
	if contact != nil {
		facts.Username, facts.LanguageCode, facts.Blocked = contact.Username, contact.LanguageCode, contact.BlockedAt != nil
	}

	var sub Subscription
	switch err := db.Preload("Plan").Where("telegram_id = ?", telegramID).Order("id DESC").First(&sub).Error; {
	case err == nil:
		facts.HasSubscription = true
		facts.Connected = sub.LastRequest != nil
		if sub.Username != "" {
			facts.Username = sub.Username
		}
		if sub.Plan != nil {
			facts.PlanName = sub.Plan.Name
		}
		if sub.ExpiresAt != nil {
			expires := sub.ExpiresAt.UTC()
			facts.ExpiresAt = &expires
		}
		facts.HasActivePaid = sub.Status == string(SubscriptionStatusActive) && PlanKindOf(sub.Plan) == PlanKindPaid &&
			sub.ExpiresAt != nil && sub.ExpiresAt.After(now)
	case !errors.Is(err, gorm.ErrRecordNotFound):
		return nil, fmt.Errorf("load warmup user subscription: %w", err)
	}

	var counts struct {
		Trials   int64
		Payments int64
	}
	err = db.Model(&JournalEvent{}).
		Select("COALESCE(SUM(CASE WHEN event_type = ? THEN 1 ELSE 0 END), 0) AS trials, COALESCE(SUM(CASE WHEN event_type = ? THEN 1 ELSE 0 END), 0) AS payments",
			string(JournalTrialBound), string(JournalPaymentSucceeded)).
		Where("telegram_id = ? AND outcome = ? AND event_type IN ?", telegramID, JournalOutcomeSuccess,
			[]string{string(JournalTrialBound), string(JournalPaymentSucceeded)}).
		Scan(&counts).Error
	if err != nil {
		return nil, fmt.Errorf("load warmup user history: %w", err)
	}
	facts.HadTrial, facts.EverPaid = counts.Trials > 0, counts.Payments > 0
	return facts, nil
}

// ---------------------------------------------------------------------------
// Delivery journal
// ---------------------------------------------------------------------------

// WarmupDelivery is one row of warmup_deliveries.
type WarmupDelivery struct {
	ID           uint       `gorm:"primaryKey;column:id"          json:"id"`
	ScenarioID   uint       `gorm:"not null;column:scenario_id"   json:"scenario_id"`
	ScenarioName string     `gorm:"not null;column:scenario_name" json:"scenario_name"`
	TelegramID   int64      `gorm:"not null;column:telegram_id"   json:"telegram_id"`
	Username     string     `gorm:"not null;column:username"      json:"username"`
	AnchorKey    string     `gorm:"not null;column:anchor_key"    json:"anchor_key"`
	AnchorAt     time.Time  `gorm:"not null;column:anchor_at"     json:"anchor_at"`
	DueAt        time.Time  `gorm:"not null;column:due_at"        json:"due_at"`
	Status       string     `gorm:"not null;column:status"        json:"status"`
	Attempts     int        `gorm:"not null;column:attempts"      json:"attempts"`
	Error        string     `gorm:"not null;column:error"         json:"error"`
	MessageID    *int       `gorm:"column:message_id"             json:"message_id"`
	CouponID     *uint      `gorm:"column:coupon_id"              json:"coupon_id"`
	CreatedAt    time.Time  `gorm:"not null;column:created_at"    json:"created_at"`
	UpdatedAt    time.Time  `gorm:"not null;column:updated_at"    json:"updated_at"`
	StartedAt    time.Time  `gorm:"not null;column:started_at"    json:"started_at"`
	SentAt       *time.Time `gorm:"column:sent_at"                json:"sent_at"`
}

func (WarmupDelivery) TableName() string { return "warmup_deliveries" }

// WarmupCoupon is a personal discount code. Not applied to payments yet.
type WarmupCoupon struct {
	ID         uint       `gorm:"primaryKey;column:id"        json:"id"`
	Code       string     `gorm:"not null;column:code"        json:"code"`
	TelegramID int64      `gorm:"not null;column:telegram_id" json:"telegram_id"`
	ScenarioID uint       `gorm:"not null;column:scenario_id" json:"scenario_id"`
	DeliveryID *uint      `gorm:"column:delivery_id"          json:"delivery_id"`
	Percent    int        `gorm:"not null;column:percent"     json:"percent"`
	Status     string     `gorm:"not null;column:status"      json:"status"`
	CreatedAt  time.Time  `gorm:"not null;column:created_at"  json:"created_at"`
	ExpiresAt  time.Time  `gorm:"not null;column:expires_at"  json:"expires_at"`
	RedeemedAt *time.Time `gorm:"column:redeemed_at"          json:"redeemed_at"`
	OrderID    *uint      `gorm:"column:order_id"             json:"order_id"`
}

func (WarmupCoupon) TableName() string { return "warmup_coupons" }

// EffectiveStatus is the stored status, with an issued coupon past its expiry
// reported as expired.
func (c *WarmupCoupon) EffectiveStatus(now time.Time) string {
	if c.Status == WarmupCouponIssued && !c.ExpiresAt.After(now) {
		return WarmupCouponExpired
	}
	return c.Status
}

// WarmupCouponDraft is the coupon to issue with a delivery.
type WarmupCouponDraft struct {
	Code      string
	Percent   int
	ExpiresAt time.Time
}

// WarmupClaim asks to send one scenario to one user for one anchor event.
type WarmupClaim struct {
	ScenarioID   uint
	ScenarioName string
	TelegramID   int64
	Username     string
	AnchorKey    string
	AnchorAt     time.Time
	DueAt        time.Time
	// Coupon is issued with a first claim; a retried claim keeps its coupon.
	Coupon *WarmupCouponDraft
}

// WarmupClaimResult is a successful claim: the caller owns the delivery
// (status sending) until it marks it sent, retry, failed or blocked.
type WarmupClaimResult struct {
	Delivery *WarmupDelivery
	Coupon   *WarmupCoupon
}

// WarmupRetryPolicy decides when a delivery in status retry may be claimed
// again: at most MaxAttempts sends; after attempt n the next one waits
// Backoff·2^(n-1), capped by MaxBackoff (0: no cap) — the exponential,
// capped backoff the broadcast campaigns use.
type WarmupRetryPolicy struct {
	MaxAttempts int
	Backoff     time.Duration
	MaxBackoff  time.Duration
}

// BackoffAfter is the wait before the attempt that follows attempt n.
func (p WarmupRetryPolicy) BackoffAfter(attempts int) time.Duration {
	delay := p.Backoff
	for i := 1; i < attempts && (p.MaxBackoff == 0 || delay < p.MaxBackoff); i++ {
		delay *= 2
	}
	if p.MaxBackoff > 0 && delay > p.MaxBackoff {
		delay = p.MaxBackoff
	}
	return delay
}

// ClaimWarmupDelivery atomically takes the right to send. A new (scenario,
// user, anchor) inserts a sending row (and its coupon); a row in status retry
// whose backoff passed and that has attempts left is taken again with its
// coupon. Anything else — sending, sent, failed, blocked, or a retry still
// backing off — returns nil: the message is not (re)sent. A coupon code
// collision returns ErrWarmupCouponCodeTaken and nothing is written; the
// caller retries with a new code.
func (s *Service) ClaimWarmupDelivery(ctx context.Context, c WarmupClaim, policy WarmupRetryPolicy, now time.Time) (*WarmupClaimResult, error) {
	now = now.UTC()
	var result *WarmupClaimResult
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		d := WarmupDelivery{
			ScenarioID: c.ScenarioID, ScenarioName: c.ScenarioName, TelegramID: c.TelegramID, Username: c.Username,
			AnchorKey: c.AnchorKey, AnchorAt: c.AnchorAt.UTC(), DueAt: c.DueAt.UTC(), Status: WarmupDeliverySending,
			Attempts: 1, CreatedAt: now, UpdatedAt: now, StartedAt: now,
		}
		res := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "scenario_id"}, {Name: "telegram_id"}, {Name: "anchor_key"}},
			DoNothing: true,
		}).Create(&d)
		if res.Error != nil {
			return fmt.Errorf("claim warmup delivery: %w", res.Error)
		}
		if res.RowsAffected == 1 {
			out := &WarmupClaimResult{Delivery: &d}
			if c.Coupon != nil {
				coupon := WarmupCoupon{
					Code: c.Coupon.Code, TelegramID: c.TelegramID, ScenarioID: c.ScenarioID, DeliveryID: &d.ID,
					Percent: c.Coupon.Percent, Status: WarmupCouponIssued, CreatedAt: now, ExpiresAt: c.Coupon.ExpiresAt.UTC(),
				}
				err := tx.Create(&coupon).Error
				if err != nil {
					if isUniqueConstraintError(err, "warmup_coupons.code") {
						return ErrWarmupCouponCodeTaken
					}
					return fmt.Errorf("issue warmup coupon: %w", err)
				}
				err = tx.Model(&WarmupDelivery{}).Where("id = ?", d.ID).Update("coupon_id", coupon.ID).Error
				if err != nil {
					return fmt.Errorf("link warmup coupon: %w", err)
				}
				d.CouponID = &coupon.ID
				out.Coupon = &coupon
			}
			result = out
			return nil
		}

		var existing WarmupDelivery
		err := tx.Where("scenario_id = ? AND telegram_id = ? AND anchor_key = ?", c.ScenarioID, c.TelegramID, c.AnchorKey).
			First(&existing).Error
		if err != nil {
			return fmt.Errorf("load warmup delivery: %w", err)
		}
		if existing.Status != WarmupDeliveryRetry || existing.Attempts >= policy.MaxAttempts ||
			existing.UpdatedAt.After(now.Add(-policy.BackoffAfter(existing.Attempts))) {
			return nil
		}
		res = tx.Model(&WarmupDelivery{}).
			Where("id = ? AND status = ? AND attempts = ?", existing.ID, WarmupDeliveryRetry, existing.Attempts).
			Updates(map[string]any{"status": WarmupDeliverySending, "attempts": existing.Attempts + 1, "started_at": now, "updated_at": now})
		if res.Error != nil {
			return fmt.Errorf("reclaim warmup delivery: %w", res.Error)
		}
		if res.RowsAffected == 0 {
			return nil
		}
		existing.Status, existing.Attempts, existing.StartedAt, existing.UpdatedAt = WarmupDeliverySending, existing.Attempts+1, now, now
		out := &WarmupClaimResult{Delivery: &existing}
		if existing.CouponID != nil {
			var coupon WarmupCoupon
			err := tx.First(&coupon, *existing.CouponID).Error
			if err != nil {
				return fmt.Errorf("load warmup coupon: %w", err)
			}
			out.Coupon = &coupon
		}
		result = out
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func truncateWarmupError(msg string) string {
	runes := []rune(msg)
	if len(runes) > warmupErrorMaxLen {
		return string(runes[:warmupErrorMaxLen])
	}
	return msg
}

// finishWarmupDelivery moves a sending delivery to its outcome. Only the
// owner of the claim (status sending) can finish it.
func finishWarmupDelivery(tx *gorm.DB, id uint, cols map[string]any) error {
	res := tx.Model(&WarmupDelivery{}).Where("id = ? AND status = ?", id, WarmupDeliverySending).Updates(cols)
	if res.Error != nil {
		return fmt.Errorf("finish warmup delivery: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		var n int64
		err := tx.Model(&WarmupDelivery{}).Where("id = ?", id).Count(&n).Error
		if err != nil {
			return fmt.Errorf("check warmup delivery: %w", err)
		}
		if n == 0 {
			return ErrWarmupDeliveryNotFound
		}
		return ErrWarmupDeliveryStale
	}
	return nil
}

// MarkWarmupDeliverySent records a delivered message.
func (s *Service) MarkWarmupDeliverySent(ctx context.Context, id uint, messageID int, now time.Time) error {
	now = now.UTC()
	return finishWarmupDelivery(s.db.WithContext(ctx), id, map[string]any{
		"status": WarmupDeliverySent, "message_id": messageID, "error": "", "sent_at": now, "updated_at": now,
	})
}

// ReleaseWarmupDelivery returns a delivery after a transient send error: it is
// claimed again after the retry backoff while attempts are left. With no
// attempts left (final) it fails for good and its coupon is revoked.
func (s *Service) ReleaseWarmupDelivery(ctx context.Context, id uint, sendErr string, final bool, now time.Time) error {
	now = now.UTC()
	status := WarmupDeliveryRetry
	if final {
		status = WarmupDeliveryFailed
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		err := finishWarmupDelivery(tx, id, map[string]any{
			"status": status, "error": truncateWarmupError(sendErr), "updated_at": now,
		})
		if err != nil {
			return err
		}
		if final {
			return revokeWarmupCoupon(tx, id)
		}
		return nil
	})
}

// MarkWarmupDeliveryBlocked records that the user blocked the bot: the
// delivery ends, its coupon is revoked and the contact is excluded from every
// further warm-up message.
func (s *Service) MarkWarmupDeliveryBlocked(ctx context.Context, id uint, sendErr string, now time.Time) error {
	return s.excludeWarmupRecipient(ctx, id, WarmupDeliveryBlocked, sendErr, now)
}

// MarkWarmupDeliveryUnreachable records a permanent "chat unavailable" error
// (deactivated account, chat not found): the delivery fails, its coupon is
// revoked and the contact is excluded like a blocked one. A later /start of
// the user lifts the exclusion (TouchWarmupContact).
func (s *Service) MarkWarmupDeliveryUnreachable(ctx context.Context, id uint, sendErr string, now time.Time) error {
	return s.excludeWarmupRecipient(ctx, id, WarmupDeliveryFailed, sendErr, now)
}

// excludeWarmupRecipient ends a delivery, revokes its coupon and excludes the
// user. The transaction starts with the write: a deferred SQLite transaction
// that reads first and upgrades to write later can be refused immediately
// (SQLITE_BUSY) when another connection writes meanwhile.
func (s *Service) excludeWarmupRecipient(ctx context.Context, id uint, status, sendErr string, now time.Time) error {
	now = now.UTC()
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		err := finishWarmupDelivery(tx, id, map[string]any{
			"status": status, "error": truncateWarmupError(sendErr), "updated_at": now,
		})
		if err != nil {
			return err
		}
		var d WarmupDelivery
		err = tx.Select("id", "telegram_id").First(&d, id).Error
		if err != nil {
			return fmt.Errorf("load warmup delivery: %w", err)
		}
		err = revokeWarmupCoupon(tx, id)
		if err != nil {
			return err
		}
		return markWarmupContactBlocked(tx, d.TelegramID, now)
	})
}

// WarmupDeliveryStatus returns the status of the delivery of a scenario for
// one anchor event ("" when there is none). The worker uses it to skip events
// that are already handled without evaluating them again.
func (s *Service) WarmupDeliveryStatus(ctx context.Context, scenarioID uint, telegramID int64, anchorKey string) (string, error) {
	var statuses []string
	err := s.db.WithContext(ctx).Model(&WarmupDelivery{}).
		Where("scenario_id = ? AND telegram_id = ? AND anchor_key = ?", scenarioID, telegramID, anchorKey).
		Limit(1).Pluck("status", &statuses).Error
	if err != nil {
		return "", fmt.Errorf("warmup delivery status: %w", err)
	}
	if len(statuses) == 0 {
		return "", nil
	}
	return statuses[0], nil
}

// ExpireWarmupRetries fails deliveries still waiting for a retry whose due
// time left the catch-up window (dueBefore): the event is no longer scanned,
// so they would otherwise wait forever. Their coupons are revoked.
func (s *Service) ExpireWarmupRetries(ctx context.Context, dueBefore, now time.Time) (int64, error) {
	var expired int64
	dueBefore = dueBefore.UTC()
	// Both statements are writes (no read-then-upgrade, see
	// excludeWarmupRecipient); coupons first, while the rows are still retry.
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		expiring := tx.Model(&WarmupDelivery{}).Select("id").Where("status = ? AND due_at < ?", WarmupDeliveryRetry, dueBefore)
		err := tx.Model(&WarmupCoupon{}).Where("status = ? AND delivery_id IN (?)", WarmupCouponIssued, expiring).
			Update("status", WarmupCouponRevoked).Error
		if err != nil {
			return fmt.Errorf("revoke expired warmup coupons: %w", err)
		}
		res := tx.Model(&WarmupDelivery{}).Where("status = ? AND due_at < ?", WarmupDeliveryRetry, dueBefore).Updates(map[string]any{
			"status": WarmupDeliveryFailed, "error": gorm.Expr("CASE WHEN error = '' THEN 'retry window expired' ELSE 'retry window expired: ' || error END"),
			"updated_at": now.UTC(),
		})
		if res.Error != nil {
			return fmt.Errorf("expire warmup retries: %w", res.Error)
		}
		expired = res.RowsAffected
		return nil
	})
	if err != nil {
		return 0, err
	}
	return expired, nil
}

func revokeWarmupCoupon(tx *gorm.DB, deliveryID uint) error {
	err := tx.Model(&WarmupCoupon{}).Where("delivery_id = ? AND status = ?", deliveryID, WarmupCouponIssued).
		Update("status", WarmupCouponRevoked).Error
	if err != nil {
		return fmt.Errorf("revoke warmup coupon: %w", err)
	}
	return nil
}

// ReleaseStaleWarmupDeliveries returns deliveries stuck in sending since
// before olderThan (the process died mid-send) to retry. Returns the count.
func (s *Service) ReleaseStaleWarmupDeliveries(ctx context.Context, olderThan, now time.Time) (int64, error) {
	res := s.db.WithContext(ctx).Model(&WarmupDelivery{}).
		Where("status = ? AND started_at < ?", WarmupDeliverySending, olderThan.UTC()).
		Updates(map[string]any{"status": WarmupDeliveryRetry, "error": "interrupted", "updated_at": now.UTC()})
	if res.Error != nil {
		return 0, fmt.Errorf("release stale warmup deliveries: %w", res.Error)
	}
	return res.RowsAffected, nil
}

// ---------------------------------------------------------------------------
// Limits
// ---------------------------------------------------------------------------

// warmupCountedStatuses are the deliveries that count against limits: sent
// ones and the ones being sent right now.
var warmupCountedStatuses = []string{WarmupDeliverySent, WarmupDeliverySending}

// CountWarmupUserDeliveriesSince counts the user's messages started since.
func (s *Service) CountWarmupUserDeliveriesSince(ctx context.Context, telegramID int64, since time.Time) (int64, error) {
	var n int64
	err := s.db.WithContext(ctx).Model(&WarmupDelivery{}).
		Where("telegram_id = ? AND status IN ? AND started_at >= ?", telegramID, warmupCountedStatuses, since.UTC()).Count(&n).Error
	if err != nil {
		return 0, fmt.Errorf("count warmup user deliveries: %w", err)
	}
	return n, nil
}

// LastWarmupUserDeliveryAt returns when the user's last message was started
// (nil: never).
func (s *Service) LastWarmupUserDeliveryAt(ctx context.Context, telegramID int64) (*time.Time, error) {
	var d WarmupDelivery
	err := s.db.WithContext(ctx).Select("started_at").
		Where("telegram_id = ? AND status IN ?", telegramID, warmupCountedStatuses).
		Order("started_at DESC").First(&d).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("last warmup user delivery: %w", err)
	}
	at := d.StartedAt.UTC()
	return &at, nil
}

// CountWarmupScenarioDeliveriesSince counts a scenario's messages started since.
func (s *Service) CountWarmupScenarioDeliveriesSince(ctx context.Context, scenarioID uint, since time.Time) (int64, error) {
	var n int64
	err := s.db.WithContext(ctx).Model(&WarmupDelivery{}).
		Where("scenario_id = ? AND status IN ? AND started_at >= ?", scenarioID, warmupCountedStatuses, since.UTC()).Count(&n).Error
	if err != nil {
		return 0, fmt.Errorf("count warmup scenario deliveries: %w", err)
	}
	return n, nil
}

// ---------------------------------------------------------------------------
// Admin lists
// ---------------------------------------------------------------------------

// WarmupDeliveryRow is a journal row with its coupon code.
type WarmupDeliveryRow struct {
	WarmupDelivery

	CouponCode *string `gorm:"column:coupon_code" json:"coupon_code"`
}

// WarmupDeliveryFilter narrows ListWarmupDeliveries; zero values match all.
// From is inclusive and To exclusive (created_at); CouponCode matches the
// coupon issued with the delivery; Category the scenario's category.
type WarmupDeliveryFilter struct {
	ScenarioID uint
	Status     string
	TelegramID int64
	From       *time.Time
	To         *time.Time
	CouponCode string
	Category   string
	Limit      int
	Offset     int
}

func adminPage(limit, offset int) (int, int) {
	if limit <= 0 {
		limit = AdminDefaultPageSize
	}
	if limit > AdminMaxPageSize {
		limit = AdminMaxPageSize
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

// ListWarmupDeliveries pages the delivery journal newest first with the total
// count of the filtered set.
func (s *Service) ListWarmupDeliveries(ctx context.Context, f WarmupDeliveryFilter) ([]WarmupDeliveryRow, int64, error) {
	limit, offset := adminPage(f.Limit, f.Offset)
	base := s.db.WithContext(ctx).Table("warmup_deliveries d").
		Joins("LEFT JOIN warmup_coupons c ON c.id = d.coupon_id")
	if f.ScenarioID != 0 {
		base = base.Where("d.scenario_id = ?", f.ScenarioID)
	}
	if f.Status != "" {
		base = base.Where("d.status = ?", f.Status)
	}
	if f.TelegramID != 0 {
		base = base.Where("d.telegram_id = ?", f.TelegramID)
	}
	if f.From != nil {
		base = base.Where("d.created_at >= ?", f.From.UTC())
	}
	if f.To != nil {
		base = base.Where("d.created_at < ?", f.To.UTC())
	}
	if f.CouponCode != "" {
		base = base.Where("c.code = ?", f.CouponCode)
	}
	if f.Category != "" {
		base = base.Where("d.scenario_id IN (?)", s.db.WithContext(ctx).Model(&WarmupScenario{}).Select("id").Where("category = ?", f.Category))
	}
	var total int64
	err := base.Session(&gorm.Session{}).Count(&total).Error
	if err != nil {
		return nil, 0, fmt.Errorf("count warmup deliveries: %w", err)
	}
	rows := []WarmupDeliveryRow{}
	err = base.Session(&gorm.Session{}).Select("d.*, c.code AS coupon_code").
		Order("d.created_at DESC, d.id DESC").Limit(limit).Offset(offset).Scan(&rows).Error
	if err != nil {
		return nil, 0, fmt.Errorf("list warmup deliveries: %w", err)
	}
	return rows, total, nil
}

// WarmupCouponFilter narrows ListWarmupCoupons; zero values match all.
// Status is the effective status: "issued" is an issued coupon still valid at
// Now, "expired" an issued coupon past its expiry, "revoked"/"redeemed" the
// stored ones.
type WarmupCouponFilter struct {
	ScenarioID uint
	TelegramID int64
	Code       string
	Status     string
	Now        time.Time
	Limit      int
	Offset     int
}

// ListWarmupCoupons pages the issued coupons newest first.
func (s *Service) ListWarmupCoupons(ctx context.Context, f WarmupCouponFilter) ([]WarmupCoupon, int64, error) {
	limit, offset := adminPage(f.Limit, f.Offset)
	now := f.Now
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()
	base := s.db.WithContext(ctx).Model(&WarmupCoupon{})
	if f.ScenarioID != 0 {
		base = base.Where("scenario_id = ?", f.ScenarioID)
	}
	if f.TelegramID != 0 {
		base = base.Where("telegram_id = ?", f.TelegramID)
	}
	if f.Code != "" {
		base = base.Where("code = ?", f.Code)
	}
	switch f.Status {
	case "":
	case WarmupCouponIssued:
		base = base.Where("status = ? AND expires_at > ?", WarmupCouponIssued, now)
	case WarmupCouponExpired:
		base = base.Where("status = ? AND expires_at <= ?", WarmupCouponIssued, now)
	default:
		base = base.Where("status = ?", f.Status)
	}
	var total int64
	err := base.Session(&gorm.Session{}).Count(&total).Error
	if err != nil {
		return nil, 0, fmt.Errorf("count warmup coupons: %w", err)
	}
	coupons := []WarmupCoupon{}
	err = base.Session(&gorm.Session{}).Order("created_at DESC, id DESC").Limit(limit).Offset(offset).Find(&coupons).Error
	if err != nil {
		return nil, 0, fmt.Errorf("list warmup coupons: %w", err)
	}
	return coupons, total, nil
}
