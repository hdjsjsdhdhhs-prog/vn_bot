package service

import (
	"context"
	"errors"
	"net/url"

	"github.com/kereal/rs8kvn_bot/internal/database"
)

// SubscriptionManagementAction separates reading from granting access days.
type SubscriptionManagementAction string

const (
	SubscriptionManagementRead  SubscriptionManagementAction = "read"
	SubscriptionManagementRenew SubscriptionManagementAction = "renew"
)

var (
	ErrSubscriptionAccessDenied          = errors.New("subscription access denied")
	ErrSubscriptionManagementUnavailable = errors.New("subscription management unavailable")
)

// SubscriptionAuthorizer is a trusted application adapter, not a credential
// parser. It resolves an authenticated/authorized positive Telegram identity
// from its caller context. For renew it must also authorize the requested days
// (e.g. a trusted admin grant), not merely authenticate the customer. Read uses
// days=0. Never derive this identity/permission from a public subscription token
// or an unverified request field. No default authorization is provided.
type SubscriptionAuthorizer func(ctx context.Context, action SubscriptionManagementAction, days int) (int64, error)

// CustomerSubscriptionInfo contains only customer-facing data. Both URLs carry
// bearer credentials: do not log this value or use it as renewal authorization.
type CustomerSubscriptionInfo struct {
	PublicSubscriptionInfo
	ConnectionURL string `json:"connection_url"`
}

// SubscriptionRenewalCapability describes lifecycle eligibility, NOT authority
// to renew. Eligible previews a one-day extension; the requested duration and
// current state are revalidated atomically during renewal. MaxDays is the input
// bound, not a guarantee that every duration fits the public timestamp range.
type SubscriptionRenewalCapability struct {
	Eligible  bool   `json:"eligible"`
	Operation string `json:"operation,omitempty"`
	MinDays   int    `json:"min_days,omitempty"`
	MaxDays   int    `json:"max_days,omitempty"`
}

// SubscriptionManagementInfo is a fresh database view, never a second model or
// cache. A missing subscription returns ErrSubscriptionNotFound instead.
type SubscriptionManagementInfo struct {
	CustomerSubscriptionInfo
	Renewal SubscriptionRenewalCapability `json:"renewal"`
}

type customerSubscriptionRepository interface {
	GetCustomerSubscription(context.Context, int64) (*database.Subscription, bool, error)
}

// SubscriptionManagement is an application-only boundary for future bot, Mini
// App, admin and confirmation adapters. It accepts no target ID or bearer token.
// Creation stays at SubscriptionService.Create with explicitly trusted terms.
type SubscriptionManagement struct {
	subscriptions *SubscriptionService
	authorize     SubscriptionAuthorizer
}

// NewSubscriptionManagement binds lifecycle services to a trusted caller policy.
// A nil policy denies all access rather than granting implicit privileges.
func NewSubscriptionManagement(subscriptions *SubscriptionService, authorize SubscriptionAuthorizer) *SubscriptionManagement {
	return &SubscriptionManagement{subscriptions: subscriptions, authorize: authorize}
}

// Current reads the customer's unique row in any status. It does not create or
// revive subscriptions and does not consult the bot or subscription-feed cache.
func (m *SubscriptionManagement) Current(ctx context.Context) (*SubscriptionManagementInfo, error) {
	telegramID, err := m.customer(ctx, SubscriptionManagementRead, 0)
	if err != nil {
		return nil, err
	}
	repo, err := m.repository()
	if err != nil {
		return nil, err
	}
	sub, eligible, err := repo.GetCustomerSubscription(ctx, telegramID)
	if err != nil {
		return nil, safeSubscriptionManagementError(err)
	}
	if sub == nil || sub.TelegramID != telegramID {
		return nil, ErrSubscriptionAccessDenied
	}
	capability := SubscriptionRenewalCapability{Eligible: eligible}
	if eligible {
		capability.Operation = "extend_days"
		capability.MinDays = 1
		capability.MaxDays = database.MaxSubscriptionRenewalDays
	}
	return &SubscriptionManagementInfo{CustomerSubscriptionInfo: m.info(sub), Renewal: capability}, nil
}

// Renew grants authorized days using the existing renewal transaction and
// post-commit cache invalidation. The response is the committed safe snapshot;
// no fallible follow-up read can misreport a successful grant as a failed one.
// Each success adds days. This is not an idempotent payment-confirmation API.
func (m *SubscriptionManagement) Renew(ctx context.Context, days int) (*CustomerSubscriptionInfo, error) {
	telegramID, err := m.customer(ctx, SubscriptionManagementRenew, days)
	if err != nil {
		return nil, err
	}
	if m.subscriptions == nil {
		return nil, ErrSubscriptionManagementUnavailable
	}
	result, err := m.subscriptions.renewCustomerSubscription(ctx, telegramID, days)
	if err != nil {
		return nil, safeSubscriptionManagementError(err)
	}
	info := m.info(result.Subscription)
	return &info, nil
}

func (m *SubscriptionManagement) customer(ctx context.Context, action SubscriptionManagementAction, days int) (int64, error) {
	if m.authorize == nil {
		return 0, ErrSubscriptionAccessDenied
	}
	id, err := m.authorize(ctx, action, days)
	if err != nil {
		return 0, &subscriptionManagementError{kind: ErrSubscriptionAccessDenied, cause: err}
	}
	if id <= 0 {
		return 0, ErrSubscriptionAccessDenied
	}
	return id, nil
}

func (m *SubscriptionManagement) repository() (customerSubscriptionRepository, error) {
	if m.subscriptions == nil {
		return nil, ErrSubscriptionManagementUnavailable
	}
	if err := m.subscriptions.validatePublicSubscriptionURL(); err != nil {
		return nil, safeSubscriptionManagementError(err)
	}
	repo, ok := m.subscriptions.db.(customerSubscriptionRepository)
	if !ok {
		return nil, ErrSubscriptionManagementUnavailable
	}
	return repo, nil
}

func (m *SubscriptionManagement) info(sub *database.Subscription) CustomerSubscriptionInfo {
	info := m.subscriptions.publicSubscriptionInfo(sub)
	// The existing web router exposes /connect/ at the public origin root.
	// Parse cannot fail after validatePublicSubscriptionURL; never use an
	// upstream/provider URL or request Host to construct customer links.
	u, _ := url.Parse(m.subscriptions.cfg.SubURL(""))
	u.Path, u.RawPath = "/connect/"+sub.Token, ""
	return CustomerSubscriptionInfo{PublicSubscriptionInfo: *info, ConnectionURL: u.String()}
}

// Keep errors.Is/As diagnostics for trusted callers, but never reflect raw DB
// errors, IDs, URLs or credentials through Error(). Adapters must not serialize
// or log unwrapped causes as customer responses.
type subscriptionManagementError struct {
	kind  error
	cause error
}

func (e *subscriptionManagementError) Error() string        { return e.kind.Error() }
func (e *subscriptionManagementError) Unwrap() error        { return e.cause }
func (e *subscriptionManagementError) Is(target error) bool { return target == e.kind }

func safeSubscriptionManagementError(err error) error {
	kind := ErrSubscriptionManagementUnavailable
	switch {
	case errors.Is(err, database.ErrSubscriptionNotFound):
		kind = database.ErrSubscriptionNotFound
	case errors.Is(err, database.ErrInvalidRenewalDays):
		kind = database.ErrInvalidRenewalDays
	case errors.Is(err, database.ErrSubscriptionNotRenewable), errors.Is(err, database.ErrProviderSourceNotFound), errors.Is(err, database.ErrProviderSourceUnusable):
		kind = database.ErrSubscriptionNotRenewable
	case errors.Is(err, context.Canceled):
		kind = context.Canceled
	case errors.Is(err, context.DeadlineExceeded):
		kind = context.DeadlineExceeded
	}
	return &subscriptionManagementError{kind: kind, cause: err}
}
