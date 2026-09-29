package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
)

// TariffRepository is the persistence seam of TariffService. *database.Service
// implements it; tests may substitute a fake.
type TariffRepository interface {
	ListTariffs(ctx context.Context) ([]database.TariffRow, error)
	GetTariff(ctx context.Context, id uint) (*database.TariffRow, error)
	ListAdminPlans(ctx context.Context) ([]database.AdminPlanRow, error)
	CreateTariff(ctx context.Context, meta database.AdminConfigMeta, in database.TariffInput) (*database.AdminConfigResult, error)
	UpdateTariff(ctx context.Context, meta database.AdminConfigMeta, in database.TariffUpdateInput) (*database.AdminConfigResult, error)
	SetTariffActive(ctx context.Context, meta database.AdminConfigMeta, id uint, version int, active bool) (*database.AdminConfigResult, error)
	ReorderTariffs(ctx context.Context, meta database.AdminConfigMeta, ids []uint) (*database.AdminConfigResult, error)
	DeleteTariff(ctx context.Context, meta database.AdminConfigMeta, id uint, version int) (*database.AdminConfigResult, error)
}

var _ TariffRepository = (*database.Service)(nil)

// TariffService is the browser-admin boundary of the tariff editor. Like
// AdminService it owns no authentication; callers authorize the actor first.
// Tariffs are read live by the bot and the Mini App (no cache), so mutations
// have no post-commit side effects.
type TariffService struct {
	repo TariffRepository
}

// NewTariffService wires the tariff editor boundary.
func NewTariffService(repo TariffRepository) *TariffService {
	return &TariffService{repo: repo}
}

// TariffView is the admin projection of a product.
type TariffView struct {
	ID           uint       `json:"id"`
	OfferID      string     `json:"offer_id"`
	Name         string     `json:"name"`
	PlanID       uint       `json:"plan_id"`
	PlanName     string     `json:"plan_name"`
	PlanActive   bool       `json:"plan_active"`
	BuilderID    *uint      `json:"builder_id"`
	DurationDays int        `json:"duration_days"`
	PriceCents   int64      `json:"price_cents"`
	Currency     string     `json:"currency"`
	IsActive     bool       `json:"is_active"`
	Description  string     `json:"description"`
	Features     []string   `json:"features"`
	Badge        string     `json:"badge"`
	SortOrder    int        `json:"sort_order"`
	Version      int        `json:"version"`
	PreviousID   *uint      `json:"previous_id"`
	ReplacedByID *uint      `json:"replaced_by_id"`
	Orders       int64      `json:"orders"`
	Subscriptions int64     `json:"subscriptions"`
	// InUse: orders or subscriptions reference the product, so changing the
	// purchase terms creates a new version instead of editing in place.
	InUse       bool       `json:"in_use"`
	OfferEndsAt *time.Time `json:"offer_ends_at"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

func tariffViewOf(r *database.TariffRow) TariffView {
	return TariffView{
		ID: r.ID, OfferID: r.OfferID, Name: r.Name, PlanID: r.PlanID, PlanName: r.PlanName, PlanActive: r.PlanActive,
		BuilderID: r.PlanBuilderID, DurationDays: r.DurationDays, PriceCents: r.PriceCents, Currency: r.Currency,
		IsActive: r.IsActive, Description: r.Description, Features: database.DecodeTariffFeatures(r.Features),
		Badge: r.Badge, SortOrder: r.SortOrder, Version: r.Version, PreviousID: r.PreviousID,
		ReplacedByID: r.ReplacedByID, Orders: r.Orders, Subscriptions: r.Subscriptions, InUse: r.InUse(),
		OfferEndsAt: r.OfferEndsAt, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

// TariffPlanView is one entry of the tariff editor's plan picker.
type TariffPlanView struct {
	ID            uint   `json:"id"`
	Name          string `json:"name"`
	IsActive      bool   `json:"is_active"`
	DevicesLimit  int    `json:"devices_limit"`
	TrafficLimit  int64  `json:"traffic_limit"`
	BuilderID     *uint  `json:"subscription_builder_id"`
	BuilderName   string `json:"builder_name"`
	Tariffs       int64  `json:"tariffs"`
	Subscriptions int64  `json:"subscriptions"`
	// Selectable is false for the system trial/free plans.
	Selectable bool `json:"selectable"`
}

// TariffOutcome is returned by every tariff mutation, including replays.
type TariffOutcome struct {
	// Tariff is the resulting product: the successor when Versioned, nil after
	// a delete (or when a replayed create's product was deleted since).
	Tariff *TariffView `json:"tariff"`
	// Previous is the retired original when Versioned.
	Previous  *TariffView            `json:"previous"`
	Versioned bool                   `json:"versioned"`
	Replayed  bool                   `json:"replayed"`
	Audit     database.AdminAuditLog `json:"audit"`
}

// TariffInput is the editor payload before normalization.
type TariffInput struct {
	Name         string
	PlanID       uint
	DurationDays int
	PriceCents   int64
	Currency     string
	Description  string
	Features     []string
	Badge        string
	SortOrder    *int
	IsActive     bool
}

// normalize trims text, unifies line breaks and drops blank feature lines, so
// cosmetic whitespace never counts as a change of the purchase terms.
func (in TariffInput) normalize() database.TariffInput {
	features := make([]string, 0, len(in.Features))
	for _, feature := range in.Features {
		if trimmed := strings.TrimSpace(feature); trimmed != "" {
			features = append(features, trimmed)
		}
	}
	description := strings.TrimSpace(strings.ReplaceAll(in.Description, "\r\n", "\n"))
	return database.TariffInput{
		Name: strings.TrimSpace(in.Name), PlanID: in.PlanID, DurationDays: in.DurationDays,
		PriceCents: in.PriceCents, Currency: strings.ToUpper(strings.TrimSpace(in.Currency)),
		Description: description, Features: features, Badge: strings.TrimSpace(in.Badge),
		SortOrder: in.SortOrder, IsActive: in.IsActive,
	}
}

// ListTariffs returns every tariff in catalogue order, retired versions included.
func (s *TariffService) ListTariffs(ctx context.Context) ([]TariffView, error) {
	rows, err := s.repo.ListTariffs(ctx)
	if err != nil {
		return nil, fmt.Errorf("list tariffs: %w", err)
	}
	views := make([]TariffView, 0, len(rows))
	for i := range rows {
		views = append(views, tariffViewOf(&rows[i]))
	}
	return views, nil
}

// GetTariff returns one tariff; database.ErrProductNotFound when missing.
func (s *TariffService) GetTariff(ctx context.Context, id uint) (*TariffView, error) {
	row, err := s.repo.GetTariff(ctx, id)
	if err != nil {
		return nil, err
	}
	view := tariffViewOf(row)
	return &view, nil
}

// ListPlans returns the plan picker entries.
func (s *TariffService) ListPlans(ctx context.Context) ([]TariffPlanView, error) {
	rows, err := s.repo.ListAdminPlans(ctx)
	if err != nil {
		return nil, fmt.Errorf("list plans: %w", err)
	}
	views := make([]TariffPlanView, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		views = append(views, TariffPlanView{
			ID: r.ID, Name: r.Name, IsActive: r.IsActive, DevicesLimit: r.DevicesLimit, TrafficLimit: r.TrafficLimit,
			BuilderID: r.SubscriptionBuilderID, BuilderName: r.BuilderName, Tariffs: r.Tariffs,
			Subscriptions: r.Subscriptions, Selectable: r.Selectable(),
		})
	}
	return views, nil
}

// CreateTariff creates a tariff.
func (s *TariffService) CreateTariff(ctx context.Context, actor, requestKey string, in TariffInput) (*TariffOutcome, error) {
	result, err := s.repo.CreateTariff(ctx, database.AdminConfigMeta{Actor: actor, RequestKey: requestKey}, in.normalize())
	if err != nil {
		return nil, err
	}
	return s.outcome(ctx, result, 0)
}

// UpdateTariff edits a tariff; see database.Service.UpdateTariff for the
// versioning rule.
func (s *TariffService) UpdateTariff(ctx context.Context, actor, requestKey string, id uint, version int, in TariffInput) (*TariffOutcome, error) {
	result, err := s.repo.UpdateTariff(ctx, database.AdminConfigMeta{Actor: actor, RequestKey: requestKey},
		database.TariffUpdateInput{ID: id, Version: version, TariffInput: in.normalize()})
	if err != nil {
		return nil, err
	}
	return s.outcome(ctx, result, id)
}

// SetTariffActive enables or disables a tariff.
func (s *TariffService) SetTariffActive(ctx context.Context, actor, requestKey string, id uint, version int, active bool) (*TariffOutcome, error) {
	result, err := s.repo.SetTariffActive(ctx, database.AdminConfigMeta{Actor: actor, RequestKey: requestKey}, id, version, active)
	if err != nil {
		return nil, err
	}
	return s.outcome(ctx, result, id)
}

// ReorderTariffs stores the catalogue order and returns the refreshed list.
func (s *TariffService) ReorderTariffs(ctx context.Context, actor, requestKey string, ids []uint) ([]TariffView, *TariffOutcome, error) {
	result, err := s.repo.ReorderTariffs(ctx, database.AdminConfigMeta{Actor: actor, RequestKey: requestKey}, ids)
	if err != nil {
		return nil, nil, err
	}
	views, err := s.ListTariffs(ctx)
	if err != nil {
		return nil, nil, err
	}
	return views, &TariffOutcome{Replayed: result.Replayed, Audit: result.Audit}, nil
}

// DeleteTariff deletes an unused tariff.
func (s *TariffService) DeleteTariff(ctx context.Context, actor, requestKey string, id uint, version int) (*TariffOutcome, error) {
	result, err := s.repo.DeleteTariff(ctx, database.AdminConfigMeta{Actor: actor, RequestKey: requestKey}, id, version)
	if err != nil {
		return nil, err
	}
	return &TariffOutcome{Replayed: result.Replayed, Audit: result.Audit}, nil
}

// outcome reloads the committed state. requestedID is the product the request
// addressed (0 for create): a different TargetID means a successor was created.
func (s *TariffService) outcome(ctx context.Context, result *database.AdminConfigResult, requestedID uint) (*TariffOutcome, error) {
	out := &TariffOutcome{Replayed: result.Replayed, Audit: result.Audit}
	tariff, err := s.GetTariff(ctx, result.TargetID)
	switch {
	case err == nil:
		out.Tariff = tariff
	case errors.Is(err, database.ErrProductNotFound) && result.Replayed:
		// Replay of a request whose product has been deleted since.
	default:
		return out, fmt.Errorf("reload tariff: %w", err)
	}
	if requestedID != 0 && result.TargetID != requestedID {
		out.Versioned = true
		previous, err := s.GetTariff(ctx, requestedID)
		if err != nil {
			return out, fmt.Errorf("reload retired tariff: %w", err)
		}
		out.Previous = previous
	}
	return out, nil
}
