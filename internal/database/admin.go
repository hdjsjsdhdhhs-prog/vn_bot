package database

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
)

// AdminAction enumerates the audited browser-admin subscription mutations.
type AdminAction string

const (
	AdminActionRenew        AdminAction = "renew"
	AdminActionDisable      AdminAction = "disable"
	AdminActionEnable       AdminAction = "enable"
	AdminActionChangeExpiry AdminAction = "change_expiry"
)

// Admin mutation limits. Days bound a single grant, not the lifetime.
const (
	AdminMaxRenewalDays  = MaxSubscriptionRenewalDays
	AdminMaxRequestKey   = 128
	AdminMaxActorLength  = 64
	AdminDefaultPageSize = 50
	AdminMaxPageSize     = 200
)

// Domain rejections of admin mutations. They are recorded in admin_audit_log
// with success=false and replayed verbatim for an idempotent request key.
// Infrastructure failures are never audited so the caller can retry them.
var (
	ErrAdminInvalidRequest        = errors.New("invalid admin request")
	ErrAdminInvalidState          = errors.New("subscription state does not allow this action")
	ErrAdminSubscriptionExpired   = errors.New("paused subscription has expired")
	ErrAdminPerpetualSubscription = errors.New("subscription has no expiry to extend")
	ErrAdminInvalidExpiry         = errors.New("resulting expiry is out of range")
	ErrAdminRequestKeyConflict    = errors.New("request key was already used for a different request")
)

// Audit error codes are the stable wire/DB representation of the sentinels.
const (
	AdminErrorCodeInvalidState        = "invalid_state"
	AdminErrorCodeSubscriptionExpired = "subscription_expired"
	AdminErrorCodePerpetual           = "perpetual_subscription"
	AdminErrorCodeInvalidExpiry       = "invalid_expiry"
)

var adminErrorCodes = map[string]error{
	AdminErrorCodeInvalidState:        ErrAdminInvalidState,
	AdminErrorCodeSubscriptionExpired: ErrAdminSubscriptionExpired,
	AdminErrorCodePerpetual:           ErrAdminPerpetualSubscription,
	AdminErrorCodeInvalidExpiry:       ErrAdminInvalidExpiry,
}

// AdminErrorCode maps a domain rejection to its audit/wire code. Unknown or
// nil errors produce an empty string, which the audit row stores for success.
func AdminErrorCode(err error) string {
	for code, sentinel := range adminErrorCodes {
		if errors.Is(err, sentinel) {
			return code
		}
	}
	return ""
}

// AdminAuditLog is one row of admin_audit_log (migration 044). UNIQUE(actor,
// request_key) makes every mutation request idempotent per actor.
type AdminAuditLog struct {
	ID             uint      `gorm:"primaryKey;column:id" json:"id"`
	Actor          string    `gorm:"not null;column:actor" json:"actor"`
	Action         string    `gorm:"not null;column:action" json:"action"`
	SubscriptionID uint      `gorm:"not null;column:subscription_id" json:"subscription_id"`
	CreatedAt      time.Time `gorm:"not null;column:created_at" json:"created_at"`
	OldValue       string    `gorm:"not null;default:'{}';column:old_value" json:"old_value"`
	NewValue       string    `gorm:"not null;default:'{}';column:new_value" json:"new_value"`
	Success        bool      `gorm:"not null;column:success" json:"success"`
	ErrorCode      string    `gorm:"not null;default:'';column:error_code" json:"error_code"`
	RequestKey     string    `gorm:"not null;column:request_key" json:"request_key"`
	RequestHash    string    `gorm:"not null;column:request_hash" json:"-"`
}

func (AdminAuditLog) TableName() string {
	return "admin_audit_log"
}

// AdminSubscriptionState is the audited projection of the mutable lifecycle
// fields. It is what admin mutations may change; nothing else is touched.
type AdminSubscriptionState struct {
	Status        string     `json:"status"`
	ExpiresAt     *time.Time `json:"expires_at"`
	RemindersSent int        `json:"reminders_sent"`
}

func adminStateOf(sub *Subscription) AdminSubscriptionState {
	state := AdminSubscriptionState{Status: sub.Status, RemindersSent: sub.RemindersSent}
	if sub.ExpiresAt != nil {
		expiry := sub.ExpiresAt.UTC()
		state.ExpiresAt = &expiry
	}
	return state
}

// AdminMutation is a single audited request. RequestKey is chosen by the
// client; repeating it with identical parameters replays the recorded outcome,
// repeating it with different parameters is rejected.
type AdminMutation struct {
	Actor          string
	RequestKey     string
	Action         AdminAction
	SubscriptionID uint
	// Days is used by renew only.
	Days int
	// ExpiresAt is used by change_expiry only and must be non-nil there.
	ExpiresAt *time.Time
}

// Validate checks request shape only; lifecycle rules are applied in the
// transaction. Shape errors are not audited.
func (m AdminMutation) Validate() error {
	if m.Actor == "" || len(m.Actor) > AdminMaxActorLength {
		return fmt.Errorf("%w: actor", ErrAdminInvalidRequest)
	}
	if m.RequestKey == "" || len(m.RequestKey) > AdminMaxRequestKey || !isPrintableASCII(m.RequestKey) {
		return fmt.Errorf("%w: request_key", ErrAdminInvalidRequest)
	}
	if m.SubscriptionID == 0 {
		return fmt.Errorf("%w: subscription_id", ErrAdminInvalidRequest)
	}
	switch m.Action {
	case AdminActionRenew:
		if m.Days <= 0 || m.Days > AdminMaxRenewalDays {
			return fmt.Errorf("%w: days must be between 1 and %d", ErrAdminInvalidRequest, AdminMaxRenewalDays)
		}
	case AdminActionChangeExpiry:
		if m.ExpiresAt == nil || m.ExpiresAt.IsZero() || m.ExpiresAt.Year() > 9999 {
			return fmt.Errorf("%w: expires_at", ErrAdminInvalidRequest)
		}
	case AdminActionDisable, AdminActionEnable:
	default:
		return fmt.Errorf("%w: action", ErrAdminInvalidRequest)
	}
	return nil
}

func isPrintableASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x21 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

// hash canonicalizes the mutation parameters so a replayed request key can be
// matched against the original payload independently of JSON formatting. Only
// the parameters the action actually consumes take part: a stray Days on
// disable or ExpiresAt on renew must not turn a retry into a key conflict.
func (m AdminMutation) hash() string {
	var days, expiry string
	switch m.Action {
	case AdminActionRenew:
		days = strconv.Itoa(m.Days)
	case AdminActionChangeExpiry:
		if m.ExpiresAt != nil {
			expiry = m.ExpiresAt.UTC().Format(time.RFC3339Nano)
		}
	}
	canonical := strings.Join([]string{
		string(m.Action), strconv.FormatUint(uint64(m.SubscriptionID), 10), days, expiry,
	}, "|")
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}

// AdminMutationResult is the committed outcome of AdminMutateSubscription.
// Subscription is the row as committed (or the current row on a replay).
type AdminMutationResult struct {
	Subscription *Subscription
	Audit        AdminAuditLog
	Replayed     bool
}

// AdminMutateSubscription applies one lifecycle mutation and its audit row in a
// single transaction. The subscription row is claimed for writing before any
// read (SQLite writer reservation, mirroring renewSubscription), so concurrent
// admin requests observe each other's committed state.
//
// Outcomes:
//   - success: subscription updated, audit row success=true, returns (result, nil);
//   - domain rejection: subscription untouched, audit row success=false with
//     error_code, returns (result, ErrAdmin*) so the caller can still show the
//     recorded audit entry;
//   - replay of the same request key with the same payload: no changes, returns
//     the original audit row, Replayed=true, and the original error (if any);
//   - request key reuse with a different payload: ErrAdminRequestKeyConflict;
//   - infrastructure failure: everything rolls back, nothing is audited.
//
// Paused policy: disable pauses; renew/change_expiry keep paused; only enable
// leaves paused, and only while the subscription has not expired.
func (s *Service) AdminMutateSubscription(ctx context.Context, m AdminMutation) (*AdminMutationResult, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	hash := m.hash()
	var (
		result    AdminMutationResult
		domainErr error
	)
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		claim := tx.Model(&Subscription{}).Where("id = ?", m.SubscriptionID).UpdateColumn("id", gorm.Expr("id"))
		if claim.Error != nil {
			return fmt.Errorf("lock subscription for admin mutation: %w", claim.Error)
		}
		if claim.RowsAffected == 0 {
			return ErrSubscriptionNotFound
		}

		var prior AdminAuditLog
		err := tx.Where("actor = ? AND request_key = ?", m.Actor, m.RequestKey).First(&prior).Error
		switch {
		case err == nil:
			if prior.RequestHash != hash {
				return ErrAdminRequestKeyConflict
			}
			var current Subscription
			if err := tx.First(&current, m.SubscriptionID).Error; err != nil {
				return fmt.Errorf("load subscription for admin replay: %w", err)
			}
			result = AdminMutationResult{Subscription: &current, Audit: prior, Replayed: true}
			domainErr = adminErrorCodes[prior.ErrorCode]
			return nil
		case !errors.Is(err, gorm.ErrRecordNotFound):
			return fmt.Errorf("lookup admin request key: %w", err)
		}

		var sub Subscription
		if err := tx.First(&sub, m.SubscriptionID).Error; err != nil {
			return fmt.Errorf("load subscription for admin mutation: %w", err)
		}
		now := time.Now().UTC()
		before := adminStateOf(&sub)
		after, applyErr := applyAdminAction(before, m, now)
		oldJSON, err := json.Marshal(before)
		if err != nil {
			return fmt.Errorf("encode audit old value: %w", err)
		}
		newState := before
		if applyErr == nil {
			newState = after
		}
		newJSON, err := json.Marshal(newState)
		if err != nil {
			return fmt.Errorf("encode audit new value: %w", err)
		}
		entry := AdminAuditLog{
			Actor: m.Actor, Action: string(m.Action), SubscriptionID: sub.ID, CreatedAt: now,
			OldValue: string(oldJSON), NewValue: string(newJSON),
			Success: applyErr == nil, ErrorCode: AdminErrorCode(applyErr),
			RequestKey: m.RequestKey, RequestHash: hash,
		}
		if applyErr == nil {
			update := tx.Model(&Subscription{}).Where("id = ?", sub.ID).Updates(map[string]any{
				"expires_at":     after.ExpiresAt,
				"status":         after.Status,
				"reminders_sent": after.RemindersSent,
			})
			if update.Error != nil {
				return fmt.Errorf("apply admin mutation: %w", update.Error)
			}
			if update.RowsAffected == 0 {
				return ErrSubscriptionNotFound
			}
		}
		if err := tx.Create(&entry).Error; err != nil {
			return fmt.Errorf("record admin audit: %w", err)
		}
		if err := tx.First(&sub, sub.ID).Error; err != nil {
			return fmt.Errorf("reload subscription after admin mutation: %w", err)
		}
		result = AdminMutationResult{Subscription: &sub, Audit: entry}
		domainErr = applyErr
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &result, domainErr
}

// applyAdminAction is the single lifecycle policy for admin mutations. It is a
// pure function of the current state so it can be tested without a database.
func applyAdminAction(current AdminSubscriptionState, m AdminMutation, now time.Time) (AdminSubscriptionState, error) {
	status := SubscriptionStatus(current.Status)
	terminal := status == SubscriptionStatusRevoked || status == SubscriptionStatusCanceled
	switch m.Action {
	case AdminActionRenew:
		if terminal {
			return current, ErrAdminInvalidState
		}
		if current.ExpiresAt == nil {
			return current, ErrAdminPerpetualSubscription
		}
		base := now
		if current.ExpiresAt.After(now) {
			base = current.ExpiresAt.UTC()
		}
		expiry := base.AddDate(0, 0, m.Days)
		if !expiry.After(base) || expiry.Year() > 9999 {
			return current, ErrAdminInvalidExpiry
		}
		next := AdminSubscriptionState{Status: current.Status, ExpiresAt: &expiry}
		if status == SubscriptionStatusExpired {
			next.Status = string(SubscriptionStatusActive)
		}
		return next, nil
	case AdminActionChangeExpiry:
		if terminal {
			return current, ErrAdminInvalidState
		}
		expiry := m.ExpiresAt.UTC()
		next := AdminSubscriptionState{Status: current.Status, ExpiresAt: &expiry}
		if status == SubscriptionStatusExpired && expiry.After(now) {
			next.Status = string(SubscriptionStatusActive)
		}
		return next, nil
	case AdminActionDisable:
		if status != SubscriptionStatusActive && status != SubscriptionStatusExpired {
			return current, ErrAdminInvalidState
		}
		next := current
		next.Status = string(SubscriptionStatusPaused)
		return next, nil
	case AdminActionEnable:
		if status != SubscriptionStatusPaused {
			return current, ErrAdminInvalidState
		}
		if current.ExpiresAt != nil && !current.ExpiresAt.After(now) {
			return current, ErrAdminSubscriptionExpired
		}
		next := current
		next.Status = string(SubscriptionStatusActive)
		return next, nil
	}
	return current, fmt.Errorf("%w: action", ErrAdminInvalidRequest)
}

// AdminDashboard aggregates the numbers shown on the admin landing page.
type AdminDashboard struct {
	TotalSubscriptions int64 `json:"total_subscriptions"`
	Users              int64 `json:"users"`
	Trials             int64 `json:"trials"`
	Active             int64 `json:"active"`
	Paused             int64 `json:"paused"`
	Revoked            int64 `json:"revoked"`
	Expired            int64 `json:"expired"`
	Canceled           int64 `json:"canceled"`
	// ActiveExpired counts status=active rows whose expiry already passed and
	// that the expiry worker has not downgraded yet.
	ActiveExpired int64 `json:"active_expired"`
	Paid          int64 `json:"paid"`
	ExpiringIn7d  int64 `json:"expiring_in_7d"`
	AuditLast24h  int64 `json:"audit_last_24h"`
}

// GetAdminDashboard reads all counters in one snapshot transaction.
func (s *Service) GetAdminDashboard(ctx context.Context) (*AdminDashboard, error) {
	now := time.Now().UTC()
	var dashboard AdminDashboard
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var counts []struct {
			Status string
			Count  int64
		}
		if err := tx.Model(&Subscription{}).Select("status, COUNT(*) AS count").Group("status").Scan(&counts).Error; err != nil {
			return fmt.Errorf("count subscriptions by status: %w", err)
		}
		for _, row := range counts {
			dashboard.TotalSubscriptions += row.Count
			switch SubscriptionStatus(row.Status) {
			case SubscriptionStatusActive:
				dashboard.Active = row.Count
			case SubscriptionStatusPaused:
				dashboard.Paused = row.Count
			case SubscriptionStatusRevoked:
				dashboard.Revoked = row.Count
			case SubscriptionStatusExpired:
				dashboard.Expired = row.Count
			case SubscriptionStatusCanceled:
				dashboard.Canceled = row.Count
			}
		}
		count := func(dst *int64, query string, args ...any) error {
			return tx.Model(&Subscription{}).Where(query, args...).Count(dst).Error
		}
		if err := count(&dashboard.Users, "telegram_id > 0"); err != nil {
			return fmt.Errorf("count users: %w", err)
		}
		if err := count(&dashboard.Trials, "telegram_id < 0"); err != nil {
			return fmt.Errorf("count trials: %w", err)
		}
		if err := count(&dashboard.ActiveExpired, "status = ? AND expires_at IS NOT NULL AND expires_at <= ?", string(SubscriptionStatusActive), now); err != nil {
			return fmt.Errorf("count active expired: %w", err)
		}
		if err := count(&dashboard.Paid, "product_id IS NOT NULL OR price_paid_cents > 0"); err != nil {
			return fmt.Errorf("count paid: %w", err)
		}
		if err := count(&dashboard.ExpiringIn7d, "status = ? AND expires_at > ? AND expires_at <= ?", string(SubscriptionStatusActive), now, now.AddDate(0, 0, 7)); err != nil {
			return fmt.Errorf("count expiring: %w", err)
		}
		if err := tx.Model(&AdminAuditLog{}).Where("created_at >= ?", now.Add(-24*time.Hour)).Count(&dashboard.AuditLast24h).Error; err != nil {
			return fmt.Errorf("count recent audit: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &dashboard, nil
}

// AdminUserFilter narrows ListAdminUsers. Query matches a numeric database or
// Telegram ID exactly, or a username substring. Status is an exact match.
type AdminUserFilter struct {
	Query  string
	Status string
	Limit  int
	Offset int
}

// AdminUserRow is a subscription with its plan name, for the Users table.
type AdminUserRow struct {
	Subscription
	PlanName string
}

// ListAdminUsers lists linked customer subscriptions (telegram_id > 0), newest
// first, with the total count for pagination. Trials are not users.
func (s *Service) ListAdminUsers(ctx context.Context, f AdminUserFilter) ([]AdminUserRow, int64, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = AdminDefaultPageSize
	}
	if limit > AdminMaxPageSize {
		limit = AdminMaxPageSize
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	var (
		rows  []AdminUserRow
		total int64
	)
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		base := tx.Table("subscriptions").Where("subscriptions.telegram_id > 0")
		if f.Status != "" {
			base = base.Where("subscriptions.status = ?", f.Status)
		}
		if q := strings.TrimSpace(f.Query); q != "" {
			if id, err := strconv.ParseInt(q, 10, 64); err == nil && id > 0 {
				base = base.Where("subscriptions.telegram_id = ? OR subscriptions.id = ?", id, id)
			} else {
				pattern := "%" + escapeLike(strings.TrimPrefix(q, "@")) + "%"
				base = base.Where("subscriptions.username LIKE ? ESCAPE '\\'", pattern)
			}
		}
		if err := base.Session(&gorm.Session{}).Count(&total).Error; err != nil {
			return fmt.Errorf("count admin users: %w", err)
		}
		err := base.Session(&gorm.Session{}).
			Select("subscriptions.*, plans.name AS plan_name").
			Joins("LEFT JOIN plans ON plans.id = subscriptions.plan_id").
			Order("subscriptions.id DESC").
			Limit(limit).Offset(f.Offset).
			Scan(&rows).Error
		if err != nil {
			return fmt.Errorf("list admin users: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	if rows == nil {
		rows = []AdminUserRow{}
	}
	return rows, total, nil
}

func escapeLike(s string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(s)
}

// AdminSubscriptionNode is a node binding with its display name.
type AdminSubscriptionNode struct {
	NodeID     uint       `json:"node_id"`
	NodeName   string     `json:"node_name"`
	Status     SyncStatus `json:"status"`
	RetryCount int        `json:"retry_count"`
	RetryAt    *time.Time `json:"retry_at"`
	LastError  string     `json:"last_error"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// AdminSubscriptionDetail is everything the subscription page shows.
type AdminSubscriptionDetail struct {
	Subscription Subscription
	Plan         *Plan
	Nodes        []AdminSubscriptionNode
	Audit        []AdminAuditLog
}

// GetAdminSubscription loads one subscription with plan, node bindings and the
// most recent audit entries in one snapshot.
func (s *Service) GetAdminSubscription(ctx context.Context, id uint, auditLimit int) (*AdminSubscriptionDetail, error) {
	if auditLimit <= 0 {
		auditLimit = AdminDefaultPageSize
	}
	if auditLimit > AdminMaxPageSize {
		auditLimit = AdminMaxPageSize
	}
	detail := &AdminSubscriptionDetail{Nodes: []AdminSubscriptionNode{}, Audit: []AdminAuditLog{}}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&detail.Subscription, id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrSubscriptionNotFound
			}
			return fmt.Errorf("load admin subscription: %w", err)
		}
		var plan Plan
		switch err := tx.First(&plan, detail.Subscription.PlanID).Error; {
		case err == nil:
			detail.Plan = &plan
		case !errors.Is(err, gorm.ErrRecordNotFound):
			return fmt.Errorf("load admin subscription plan: %w", err)
		}
		var nodes []struct {
			SubscriptionNode
			NodeName string
		}
		err := tx.Table("subscription_nodes").
			Select("subscription_nodes.*, nodes.name AS node_name").
			Joins("LEFT JOIN nodes ON nodes.id = subscription_nodes.node_id").
			Where("subscription_nodes.subscription_id = ?", id).
			Order("subscription_nodes.node_id ASC").
			Scan(&nodes).Error
		if err != nil {
			return fmt.Errorf("load admin subscription nodes: %w", err)
		}
		for _, n := range nodes {
			item := AdminSubscriptionNode{NodeID: n.NodeID, NodeName: n.NodeName, Status: n.Status, RetryCount: n.RetryCount, RetryAt: n.RetryAt, UpdatedAt: n.UpdatedAt}
			if n.LastError != nil {
				item.LastError = *n.LastError
			}
			detail.Nodes = append(detail.Nodes, item)
		}
		err = tx.Where("subscription_id = ?", id).Order("id DESC").Limit(auditLimit).Find(&detail.Audit).Error
		if err != nil {
			return fmt.Errorf("load admin subscription audit: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return detail, nil
}

// AdminAuditFilter pages the audit log newest-first. BeforeID is an exclusive
// cursor; SubscriptionID zero means all subscriptions.
type AdminAuditFilter struct {
	SubscriptionID uint
	BeforeID       uint
	Limit          int
}

// ListAdminAudit returns audit entries newest-first.
func (s *Service) ListAdminAudit(ctx context.Context, f AdminAuditFilter) ([]AdminAuditLog, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = AdminDefaultPageSize
	}
	if limit > AdminMaxPageSize {
		limit = AdminMaxPageSize
	}
	query := s.db.WithContext(ctx).Model(&AdminAuditLog{})
	if f.SubscriptionID != 0 {
		query = query.Where("subscription_id = ?", f.SubscriptionID)
	}
	if f.BeforeID != 0 {
		query = query.Where("id < ?", f.BeforeID)
	}
	entries := []AdminAuditLog{}
	if err := query.Order("id DESC").Limit(limit).Find(&entries).Error; err != nil {
		return nil, fmt.Errorf("list admin audit: %w", err)
	}
	return entries, nil
}
