package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
)

// AdminRepository is the persistence seam of AdminService. *database.Service
// implements it; tests may substitute a fake.
type AdminRepository interface {
	AdminMutateSubscription(ctx context.Context, m database.AdminMutation) (*database.AdminMutationResult, error)
	GetAdminDashboard(ctx context.Context) (*database.AdminDashboard, error)
	ListAdminUsers(ctx context.Context, f database.AdminUserFilter) ([]database.AdminUserRow, int64, error)
	GetAdminSubscription(ctx context.Context, id uint, auditLimit int) (*database.AdminSubscriptionDetail, error)
	ListAdminAudit(ctx context.Context, f database.AdminAuditFilter) ([]database.AdminAuditLog, error)
}

var _ AdminRepository = (*database.Service)(nil)

// AdminService is the browser-admin application boundary. It owns no
// authentication: callers must have authorized the actor (adminauth) before
// invoking it. Mutations are atomic with their audit row in the repository;
// this layer adds validation, safe read models and post-commit side effects
// (cache invalidation, metrics).
type AdminService struct {
	repo AdminRepository
	subs *SubscriptionService
}

// NewAdminService wires the admin boundary. subs may be nil in tests;
// production passes the shared SubscriptionService so cache invalidation
// follows the same paths as the bot.
func NewAdminService(repo AdminRepository, subs *SubscriptionService) *AdminService {
	return &AdminService{repo: repo, subs: subs}
}

// AdminSubscriptionView is the browser-safe projection of a subscription. It
// deliberately omits bearer material (token, subscription_id, client_id) and
// raw device/IP payloads.
type AdminSubscriptionView struct {
	ID                    uint       `json:"id"`
	TelegramID            int64      `json:"telegram_id"`
	Username              string     `json:"username"`
	Status                string     `json:"status"`
	ExpiresAt             *time.Time `json:"expires_at"`
	PlanID                uint       `json:"plan_id"`
	PlanName              string     `json:"plan_name,omitempty"`
	ProviderSourceID      *uint      `json:"provider_source_id"`
	ProductID             *uint      `json:"product_id"`
	IsPaid                bool       `json:"is_paid"`
	PricePaidCents        int64      `json:"price_paid_cents"`
	Currency              *string    `json:"currency"`
	ReferredBy            *int64     `json:"referred_by"`
	StartedAt             *time.Time `json:"started_at"`
	LastRequest           *time.Time `json:"last_request"`
	CreatedAt             time.Time  `json:"created_at"`
	UpdatedAt             time.Time  `json:"updated_at"`
	RemindersSent         int        `json:"reminders_sent"`
	Devices               int        `json:"devices"`
	IPs                   int        `json:"ips"`
	SubscriptionBuilderID *uint      `json:"subscription_builder_id"`
}

func adminViewOf(sub *database.Subscription, planName string) AdminSubscriptionView {
	view := AdminSubscriptionView{
		ID: sub.ID, TelegramID: sub.TelegramID, Username: sub.Username, Status: sub.Status,
		ExpiresAt: sub.ExpiresAt, PlanID: sub.PlanID, PlanName: planName,
		ProviderSourceID: sub.ProviderSourceID, ProductID: sub.ProductID, IsPaid: sub.IsPaid(),
		PricePaidCents: sub.PricePaidCents, Currency: sub.Currency, ReferredBy: sub.ReferredBy,
		StartedAt: sub.StartedAt, LastRequest: sub.LastRequest, CreatedAt: sub.CreatedAt,
		UpdatedAt: sub.UpdatedAt, RemindersSent: sub.RemindersSent,
		SubscriptionBuilderID: sub.SubscriptionBuilderID,
	}
	if devices, err := sub.ParseDevices(); err == nil {
		view.Devices = len(devices)
	}
	if ips, err := sub.ParseIPs(); err == nil {
		view.IPs = len(ips)
	}
	return view
}

// AdminPlanView is the plan summary shown next to a subscription.
type AdminPlanView struct {
	ID                    uint   `json:"id"`
	Name                  string `json:"name"`
	IsActive              bool   `json:"is_active"`
	SubscriptionBuilderID *uint  `json:"subscription_builder_id"`
}

// AdminSubscriptionPage is the subscription detail response.
type AdminSubscriptionPage struct {
	Subscription AdminSubscriptionView    `json:"subscription"`
	Plan         *AdminPlanView           `json:"plan"`
	Audit        []database.AdminAuditLog `json:"audit"`
}

// AdminUsersPage is the paginated users list response.
type AdminUsersPage struct {
	Users  []AdminSubscriptionView `json:"users"`
	Total  int64                   `json:"total"`
	Limit  int                     `json:"limit"`
	Offset int                     `json:"offset"`
}

// AdminMutationOutcome is returned for every recorded mutation, including
// audited rejections and idempotent replays.
type AdminMutationOutcome struct {
	Subscription AdminSubscriptionView  `json:"subscription"`
	Audit        database.AdminAuditLog `json:"audit"`
	Replayed     bool                   `json:"replayed"`
}

// Dashboard returns the landing-page counters.
func (s *AdminService) Dashboard(ctx context.Context) (*database.AdminDashboard, error) {
	dashboard, err := s.repo.GetAdminDashboard(ctx)
	if err != nil {
		return nil, fmt.Errorf("admin dashboard: %w", err)
	}
	return dashboard, nil
}

// ListUsers pages linked customer subscriptions.
func (s *AdminService) ListUsers(ctx context.Context, f database.AdminUserFilter) (*AdminUsersPage, error) {
	if f.Status != "" && !isKnownSubscriptionStatus(f.Status) {
		return nil, fmt.Errorf("%w: status", database.ErrAdminInvalidRequest)
	}
	if f.Limit <= 0 {
		f.Limit = database.AdminDefaultPageSize
	}
	if f.Limit > database.AdminMaxPageSize {
		f.Limit = database.AdminMaxPageSize
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	rows, total, err := s.repo.ListAdminUsers(ctx, f)
	if err != nil {
		return nil, fmt.Errorf("admin users: %w", err)
	}
	page := &AdminUsersPage{Users: make([]AdminSubscriptionView, 0, len(rows)), Total: total, Limit: f.Limit, Offset: f.Offset}
	for i := range rows {
		page.Users = append(page.Users, adminViewOf(&rows[i].Subscription, rows[i].PlanName))
	}
	return page, nil
}

// GetSubscription returns the subscription detail page.
func (s *AdminService) GetSubscription(ctx context.Context, id uint) (*AdminSubscriptionPage, error) {
	detail, err := s.repo.GetAdminSubscription(ctx, id, database.AdminDefaultPageSize)
	if err != nil {
		if errors.Is(err, database.ErrSubscriptionNotFound) {
			return nil, err
		}
		return nil, fmt.Errorf("admin subscription: %w", err)
	}
	planName := ""
	var plan *AdminPlanView
	if detail.Plan != nil {
		planName = detail.Plan.Name
		plan = &AdminPlanView{ID: detail.Plan.ID, Name: detail.Plan.Name, IsActive: detail.Plan.IsActive, SubscriptionBuilderID: detail.Plan.SubscriptionBuilderID}
	}
	return &AdminSubscriptionPage{Subscription: adminViewOf(&detail.Subscription, planName), Plan: plan, Audit: detail.Audit}, nil
}

// ListAudit pages the audit log newest-first.
func (s *AdminService) ListAudit(ctx context.Context, f database.AdminAuditFilter) ([]database.AdminAuditLog, error) {
	entries, err := s.repo.ListAdminAudit(ctx, f)
	if err != nil {
		return nil, fmt.Errorf("admin audit: %w", err)
	}
	return entries, nil
}

// Mutate performs one audited lifecycle mutation.
//
// Return contract:
//   - (outcome, nil): committed success or a replayed success;
//   - (outcome, ErrAdmin*): the rejection was recorded (or replayed) in the
//     audit log — the caller should surface both;
//   - (nil, err): nothing was recorded — invalid request shape, unknown
//     subscription, request-key conflict, or an infrastructure failure.
//
// After a successful commit (including replays) the caches of the
// subscription are invalidated, so the next /sub request is served for the
// committed state (status, expiry, plan builder). Access is local state only:
// there is no external VPN work to line up.
func (s *AdminService) Mutate(ctx context.Context, m database.AdminMutation) (*AdminMutationOutcome, error) {
	// The repository validates too, but the service is the application
	// boundary: a malformed request must never reach the persistence seam.
	if err := m.Validate(); err != nil {
		return nil, err
	}
	result, err := s.repo.AdminMutateSubscription(ctx, m)
	if result == nil {
		return nil, err
	}
	outcome := &AdminMutationOutcome{Subscription: adminViewOf(result.Subscription, ""), Audit: result.Audit, Replayed: result.Replayed}
	if err != nil {
		return outcome, err
	}
	s.afterMutation(ctx, result.Subscription)
	return outcome, nil
}

// afterMutation drops the caches of a committed mutation. It runs detached
// from the request context: the change is committed even if the client left.
func (s *AdminService) afterMutation(ctx context.Context, sub *database.Subscription) {
	if s.subs == nil || sub == nil {
		return
	}
	postCommit := context.WithoutCancel(ctx)
	if sub.TelegramID > 0 {
		s.subs.InvalidateSubscription(postCommit, sub.TelegramID)
	}
	if sub.SubscriptionID != "" {
		s.subs.InvalidateBySubID(postCommit, sub.SubscriptionID)
	}
	s.subs.RefreshActiveSubscriptionsMetric(postCommit)
}

func isKnownSubscriptionStatus(status string) bool {
	switch database.SubscriptionStatus(status) {
	case database.SubscriptionStatusActive, database.SubscriptionStatusExpired, database.SubscriptionStatusPaused,
		database.SubscriptionStatusCanceled, database.SubscriptionStatusRevoked:
		return true
	}
	return false
}
