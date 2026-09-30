package database

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/logger"

	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// JournalEventType enumerates the user, subscription and payment events of
// the admin journal (migration 047). Values are the stable wire/DB form.
type JournalEventType string

const (
	JournalUserRegistered          JournalEventType = "user_registered"
	JournalTrialStarted            JournalEventType = "trial_started"
	JournalTrialBound              JournalEventType = "trial_bound"
	JournalTrialExpired            JournalEventType = "trial_expired"
	JournalSubscriptionReconnected JournalEventType = "subscription_reconnected"
	JournalFreeActivated           JournalEventType = "free_activated"
	JournalPaidActivated           JournalEventType = "paid_activated"
	JournalPlanChanged             JournalEventType = "plan_changed"
	JournalRenewed                 JournalEventType = "subscription_renewed"
	JournalExpiryChanged           JournalEventType = "expiry_changed"
	JournalExpired                 JournalEventType = "subscription_expired"
	JournalDisabled                JournalEventType = "subscription_disabled"
	JournalEnabled                 JournalEventType = "subscription_enabled"
	JournalRevoked                 JournalEventType = "subscription_revoked"
	JournalPaymentSucceeded        JournalEventType = "payment_succeeded"
	JournalPaymentFailed           JournalEventType = "payment_failed"
	JournalPaymentRefunded         JournalEventType = "payment_refunded"
)

// JournalEventTypes lists every known type (filter validation).
var JournalEventTypes = []JournalEventType{
	JournalUserRegistered, JournalTrialStarted, JournalTrialBound, JournalTrialExpired,
	JournalSubscriptionReconnected, JournalFreeActivated, JournalPaidActivated, JournalPlanChanged,
	JournalRenewed, JournalExpiryChanged, JournalExpired, JournalDisabled, JournalEnabled, JournalRevoked,
	JournalPaymentSucceeded, JournalPaymentFailed, JournalPaymentRefunded,
}

// IsKnownJournalEventType reports whether t is one of JournalEventTypes.
func IsKnownJournalEventType(t string) bool {
	for _, known := range JournalEventTypes {
		if string(known) == t {
			return true
		}
	}
	return false
}

// Journal actors: who initiated the event.
const (
	JournalActorUser   = "user"
	JournalActorAdmin  = "admin"
	JournalActorSystem = "system"
)

// Journal outcomes.
const (
	JournalOutcomeSuccess  = "success"
	JournalOutcomeFailed   = "failed"
	JournalOutcomeRejected = "rejected"
)

// Plan kinds (subscription type filter).
const (
	PlanKindFree  = "free"
	PlanKindTrial = "trial"
	PlanKindPaid  = "paid"
)

// IsKnownPlanKind reports whether k is a plan kind filter value.
func IsKnownPlanKind(k string) bool {
	return k == PlanKindFree || k == PlanKindTrial || k == PlanKindPaid
}

// PlanKindOf classifies a plan by the system plan names; everything else is
// a purchasable (paid) plan. A nil plan has no kind.
func PlanKindOf(plan *Plan) string {
	switch {
	case plan == nil:
		return ""
	case plan.Name == TrialPlanName:
		return PlanKindTrial
	case plan.Name == FreePlanName:
		return PlanKindFree
	default:
		return PlanKindPaid
	}
}

// JournalEvent is one append-only row of journal_events. It is written only by
// backend code, inside the transaction of the state change it describes.
type JournalEvent struct {
	ID             uint      `gorm:"primaryKey;column:id" json:"id"`
	CreatedAt      time.Time `gorm:"not null;column:created_at" json:"created_at"`
	EventType      string    `gorm:"not null;column:event_type" json:"event_type"`
	Outcome        string    `gorm:"not null;column:outcome" json:"outcome"`
	Actor          string    `gorm:"not null;column:actor" json:"actor"`
	ActorName      string    `gorm:"not null;column:actor_name" json:"actor_name"`
	TelegramID     int64     `gorm:"not null;column:telegram_id" json:"telegram_id"`
	Username       string    `gorm:"not null;column:username" json:"username"`
	SubscriptionID *uint     `gorm:"column:subscription_id" json:"subscription_id"`
	PlanID         *uint     `gorm:"column:plan_id" json:"plan_id"`
	PlanName       string    `gorm:"not null;column:plan_name" json:"plan_name"`
	PlanKind       string    `gorm:"not null;column:plan_kind" json:"plan_kind"`
	OrderID        *uint     `gorm:"column:order_id" json:"order_id"`
	AmountCents    *int64    `gorm:"column:amount_cents" json:"amount_cents"`
	Currency       *string   `gorm:"column:currency" json:"currency"`
	Description    string    `gorm:"not null;column:description" json:"description"`
	Details        string    `gorm:"not null;column:details" json:"-"`
	DedupKey       *string   `gorm:"column:dedup_key" json:"-"`
}

func (JournalEvent) TableName() string { return "journal_events" }

// JournalState is the lifecycle snapshot stored as details.before/after.
type JournalState struct {
	Status    string     `json:"status"`
	ExpiresAt *time.Time `json:"expires_at"`
	PlanID    uint       `json:"plan_id"`
	PlanName  string     `json:"plan_name,omitempty"`
}

// JournalRecord describes an event to record for a subscription. Fields left
// empty are derived: subject (user, subscription, plan) from Subscription,
// Outcome defaults to success, CreatedAt to now (UTC).
type JournalRecord struct {
	Type        JournalEventType
	Outcome     string
	Actor       string
	ActorName   string
	Description string
	// Subscription is the subject. Its user fields and PlanID are snapshotted.
	Subscription *Subscription
	// PlanID overrides the subject's plan (e.g. the plan that just expired).
	PlanID  uint
	OrderID uint
	// AmountCents and Currency are set for payment events.
	AmountCents *int64
	Currency    string
	Before      *JournalState
	After       *JournalState
	Extra       map[string]any
	DedupKey    string
	At          time.Time
}

// JournalStateOf snapshots the lifecycle fields of sub.
func JournalStateOf(sub *Subscription) *JournalState {
	if sub == nil {
		return nil
	}
	state := &JournalState{Status: sub.Status, PlanID: sub.PlanID}
	if sub.ExpiresAt != nil {
		expiry := sub.ExpiresAt.UTC()
		state.ExpiresAt = &expiry
	}
	return state
}

// recordJournal inserts one journal row using tx (the caller's transaction).
// It is best-effort by design: the business change must never be rolled back
// or rejected because of the journal. The insert runs in a SAVEPOINT, so a
// failed statement is undone without poisoning the enclosing transaction; the
// failure is logged. A repeated dedup key is a silent no-op.
func recordJournal(ctx context.Context, tx *gorm.DB, rec JournalRecord) {
	event, err := buildJournalEvent(tx, rec)
	if err == nil {
		err = tx.WithContext(ctx).Transaction(func(sp *gorm.DB) error {
			return sp.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "dedup_key"}}, DoNothing: true}).Create(event).Error
		})
	}
	if err != nil {
		fields := []zap.Field{zap.String("event_type", string(rec.Type)), zap.Error(err)}
		if rec.Subscription != nil {
			fields = append(fields, zap.Uint("subscription_id", rec.Subscription.ID))
		}
		logger.Warn("failed to record journal event", fields...)
	}
}

func buildJournalEvent(tx *gorm.DB, rec JournalRecord) (*JournalEvent, error) {
	if rec.Type == "" || rec.Description == "" {
		return nil, errors.New("journal event requires type and description")
	}
	at := rec.At
	if at.IsZero() {
		at = time.Now()
	}
	event := &JournalEvent{
		CreatedAt: at.UTC(), EventType: string(rec.Type), Outcome: rec.Outcome, Actor: rec.Actor,
		ActorName: rec.ActorName, Description: rec.Description, AmountCents: rec.AmountCents,
	}
	if event.Outcome == "" {
		event.Outcome = JournalOutcomeSuccess
	}
	if event.Actor == "" {
		event.Actor = JournalActorSystem
	}
	if rec.Currency != "" {
		currency := rec.Currency
		event.Currency = &currency
	}
	if rec.OrderID != 0 {
		orderID := rec.OrderID
		event.OrderID = &orderID
	}
	if rec.DedupKey != "" {
		key := rec.DedupKey
		event.DedupKey = &key
	}
	planID := rec.PlanID
	if sub := rec.Subscription; sub != nil {
		if sub.ID != 0 {
			subID := sub.ID
			event.SubscriptionID = &subID
		}
		event.TelegramID = sub.TelegramID
		event.Username = sub.Username
		if planID == 0 {
			planID = sub.PlanID
		}
	}
	if planID != 0 {
		event.PlanID = &planID
		var plan Plan
		switch err := tx.Select("id", "name").First(&plan, planID).Error; {
		case err == nil:
			event.PlanName = plan.Name
			event.PlanKind = PlanKindOf(&plan)
		case !errors.Is(err, gorm.ErrRecordNotFound):
			return nil, fmt.Errorf("load journal plan: %w", err)
		}
	}
	details := map[string]any{}
	for key, value := range rec.Extra {
		details[key] = value
	}
	if rec.Before != nil {
		details["before"] = withPlanName(tx, rec.Before)
	}
	if rec.After != nil {
		details["after"] = withPlanName(tx, rec.After)
	}
	encoded, err := json.Marshal(details)
	if err != nil {
		return nil, fmt.Errorf("encode journal details: %w", err)
	}
	event.Details = string(encoded)
	return event, nil
}

// withPlanName resolves the plan name of a state snapshot (best-effort).
func withPlanName(tx *gorm.DB, state *JournalState) *JournalState {
	if state.PlanName != "" || state.PlanID == 0 {
		return state
	}
	copied := *state
	var plan Plan
	if err := tx.Select("id", "name").First(&plan, state.PlanID).Error; err == nil {
		copied.PlanName = plan.Name
	}
	return &copied
}

// UpdateSubscriptionWithJournal is UpdateSubscription plus its journal event in
// one transaction. Service-layer lifecycle changes use it so the event is
// recorded exactly when the change commits.
func (s *Service) UpdateSubscriptionWithJournal(ctx context.Context, sub *Subscription, rec JournalRecord) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := (&Service{db: tx}).UpdateSubscription(ctx, sub); err != nil {
			return err
		}
		if rec.Subscription == nil {
			rec.Subscription = sub
		}
		recordJournal(ctx, tx, rec)
		return nil
	})
}

// JournalFilter narrows ListJournal. Query matches a numeric Telegram ID or
// subscription ID exactly, otherwise a username substring (leading @ ignored).
// From is inclusive, To exclusive. Newest events come first.
type JournalFilter struct {
	Query    string
	Type     string
	PlanKind string
	From     *time.Time
	To       *time.Time
	Limit    int
	Offset   int
}

// ListJournal pages journal events newest-first (created_at, then id) with
// the total count of the filtered set.
func (s *Service) ListJournal(ctx context.Context, f JournalFilter) ([]JournalEvent, int64, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = AdminDefaultPageSize
	}
	if limit > AdminMaxPageSize {
		limit = AdminMaxPageSize
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}
	var (
		events []JournalEvent
		total  int64
	)
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		base := tx.Model(&JournalEvent{})
		if f.Type != "" {
			base = base.Where("event_type = ?", f.Type)
		}
		if f.PlanKind != "" {
			base = base.Where("plan_kind = ?", f.PlanKind)
		}
		if f.From != nil {
			base = base.Where("created_at >= ?", f.From.UTC())
		}
		if f.To != nil {
			base = base.Where("created_at < ?", f.To.UTC())
		}
		if q := strings.TrimSpace(f.Query); q != "" {
			if id, err := strconv.ParseInt(q, 10, 64); err == nil && id > 0 {
				base = base.Where("telegram_id = ? OR subscription_id = ?", id, id)
			} else {
				pattern := "%" + escapeLike(strings.TrimPrefix(q, "@")) + "%"
				base = base.Where("username LIKE ? ESCAPE '\\'", pattern)
			}
		}
		if err := base.Session(&gorm.Session{}).Count(&total).Error; err != nil {
			return fmt.Errorf("count journal events: %w", err)
		}
		if err := base.Session(&gorm.Session{}).Order("created_at DESC, id DESC").Limit(limit).Offset(offset).Find(&events).Error; err != nil {
			return fmt.Errorf("list journal events: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	if events == nil {
		events = []JournalEvent{}
	}
	return events, total, nil
}

// ErrJournalEventNotFound is returned by GetJournalEvent for an unknown ID.
var ErrJournalEventNotFound = errors.New("journal event not found")

// GetJournalEvent loads one journal event.
func (s *Service) GetJournalEvent(ctx context.Context, id uint) (*JournalEvent, error) {
	var event JournalEvent
	if err := s.db.WithContext(ctx).First(&event, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrJournalEventNotFound
		}
		return nil, fmt.Errorf("get journal event: %w", err)
	}
	return &event, nil
}

// journalDays formats a day count for descriptions ("30 дн.").
func journalDays(days int) string {
	return strconv.Itoa(days) + " дн."
}

// journalExpiryKey identifies one expiry of a subscription so a repeated
// expiry scan of the same term cannot journal it twice.
func journalExpiryKey(prefix string, id uint, expiresAt *time.Time) string {
	key := prefix + ":" + strconv.FormatUint(uint64(id), 10)
	if expiresAt != nil {
		key += ":" + strconv.FormatInt(expiresAt.UTC().Unix(), 10)
	}
	return key
}

func journalKey(prefix string, id uint) string {
	return prefix + ":" + strconv.FormatUint(uint64(id), 10)
}
