package database

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"
)

// Tariff editor (migration 046). A tariff is a Product: the purchasable offer
// shown in the Mini App and the bot. Its Plan (limits, nodes, builder) is only
// selected here, never edited.
//
// Purchase terms (name, plan, duration, price, currency) are immutable once an
// order or a subscription references the product. Changing them on such a
// product creates a successor row (new ID, new offer reference) and retires the
// original, so historical orders and subscriptions keep their exact terms.
// Presentation (description, features, badge), catalogue position and the
// enabled flag are edited in place.
//
// Every mutation is audited through runAdminConfigMutation and is idempotent by
// (actor, request_key). Optimistic locking uses products.version.

// Audited tariff actions.
const (
	AdminActionTariffCreated    AdminAction = "tariff_created"
	AdminActionTariffUpdated    AdminAction = "tariff_updated"
	AdminActionTariffEnabled    AdminAction = "tariff_enabled"
	AdminActionTariffDisabled   AdminAction = "tariff_disabled"
	AdminActionTariffsReordered AdminAction = "tariffs_reordered"
	AdminActionTariffDeleted    AdminAction = "tariff_deleted"
)

// AdminTargetTariff is the audit target type of a product.
const AdminTargetTariff = "tariff"

// Tariff rejections. Like the builder rejections they are returned to the
// caller and NOT audited: nothing changed.
var (
	ErrTariffInvalid         = errors.New("tariff is invalid")
	ErrTariffVersionConflict = errors.New("tariff was changed concurrently")
	ErrTariffInUse           = errors.New("tariff is referenced by orders or subscriptions")
	ErrTariffSuperseded      = errors.New("tariff was replaced by a newer version")
	ErrTariffOrderStale      = errors.New("tariff order does not match the catalogue")
)

// Input bounds. Lengths are in characters (runes).
const (
	TariffMaxNameLength        = 64
	TariffMaxDescriptionLength = 500
	TariffMaxFeatures          = 8
	TariffMaxFeatureLength     = 80
	TariffMaxBadgeLength       = 24
	TariffMaxPriceCents        = 100_000_000
	TariffMaxSortOrder         = 100_000
)

var tariffCurrencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)

// TariffInput is the editable content of a tariff. Strings must already be
// trimmed by the caller (the service normalizes); Validate rejects anything
// out of bounds and reports the offending field.
type TariffInput struct {
	Name         string   `json:"name"`
	PlanID       uint     `json:"plan_id"`
	DurationDays int      `json:"duration_days"`
	PriceCents   int64    `json:"price_cents"`
	Currency     string   `json:"currency"`
	Description  string   `json:"description"`
	Features     []string `json:"features"`
	Badge        string   `json:"badge"`
	// SortOrder nil appends on create and keeps the position on update.
	SortOrder *int `json:"sort_order"`
	IsActive  bool `json:"is_active"`
}

// TariffFieldError names the invalid field. It unwraps to ErrTariffInvalid.
type TariffFieldError struct{ Field string }

func (e *TariffFieldError) Error() string { return ErrTariffInvalid.Error() + ": " + e.Field }
func (e *TariffFieldError) Unwrap() error { return ErrTariffInvalid }

func invalidTariff(field string) error { return &TariffFieldError{Field: field} }

// Validate checks the static bounds; plan eligibility needs the database.
func (in TariffInput) Validate() error {
	switch {
	case in.Name == "" || utf8.RuneCountInString(in.Name) > TariffMaxNameLength || !utf8.ValidString(in.Name):
		return invalidTariff("name")
	case in.PlanID == 0:
		return invalidTariff("plan_id")
	case in.DurationDays <= 0 || in.DurationDays > MaxSubscriptionRenewalDays:
		return invalidTariff("duration_days")
	case in.PriceCents <= 0 || in.PriceCents > TariffMaxPriceCents:
		return invalidTariff("price_cents")
	case !tariffCurrencyPattern.MatchString(in.Currency):
		return invalidTariff("currency")
	case utf8.RuneCountInString(in.Description) > TariffMaxDescriptionLength || !utf8.ValidString(in.Description):
		return invalidTariff("description")
	case len(in.Features) > TariffMaxFeatures:
		return invalidTariff("features")
	case utf8.RuneCountInString(in.Badge) > TariffMaxBadgeLength || !utf8.ValidString(in.Badge):
		return invalidTariff("badge")
	case in.SortOrder != nil && (*in.SortOrder < 0 || *in.SortOrder > TariffMaxSortOrder):
		return invalidTariff("sort_order")
	}
	for _, feature := range in.Features {
		if feature == "" || utf8.RuneCountInString(feature) > TariffMaxFeatureLength || !utf8.ValidString(feature) {
			return invalidTariff("features")
		}
	}
	return nil
}

// EncodeTariffFeatures stores features as a JSON array (never null).
func EncodeTariffFeatures(features []string) string {
	if len(features) == 0 {
		return "[]"
	}
	encoded, err := json.Marshal(features)
	if err != nil {
		return "[]"
	}
	return string(encoded)
}

// DecodeTariffFeatures reads products.features; malformed or empty values
// decode to an empty list so a bad row can never break the catalogue.
func DecodeTariffFeatures(raw string) []string {
	features := []string{}
	if strings.TrimSpace(raw) == "" {
		return features
	}
	if err := json.Unmarshal([]byte(raw), &features); err != nil || features == nil {
		return []string{}
	}
	return features
}

// sameTerms reports whether the immutable purchase terms are unchanged.
func (in TariffInput) sameTerms(p *Product) bool {
	return in.Name == p.Name && in.PlanID == p.PlanID && in.DurationDays == p.DurationDays &&
		in.PriceCents == p.PriceCents && in.Currency == p.Currency
}

// ---------------------------------------------------------------------------
// Read models
// ---------------------------------------------------------------------------

// TariffRow is a product with the data the admin editor needs around it.
type TariffRow struct {
	Product
	PlanName      string
	PlanActive    bool
	PlanBuilderID *uint
	Orders        int64
	Subscriptions int64
	// ReplacedByID is the successor created from this product, if any.
	ReplacedByID *uint
}

// InUse reports whether the purchase terms are frozen.
func (r TariffRow) InUse() bool { return r.Orders > 0 || r.Subscriptions > 0 }

// ListTariffs returns every product (including retired versions) in catalogue
// order: sort_order, then price, then ID — the order the bot and the Mini App
// use for the active subset.
func (s *Service) ListTariffs(ctx context.Context) ([]TariffRow, error) {
	var rows []TariffRow
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		rows, err = loadTariffRows(tx, 0)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("list tariffs: %w", err)
	}
	return rows, nil
}

// GetTariff returns one product with its editor context.
func (s *Service) GetTariff(ctx context.Context, id uint) (*TariffRow, error) {
	if id == 0 {
		return nil, ErrProductNotFound
	}
	var rows []TariffRow
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		rows, err = loadTariffRows(tx, id)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("get tariff: %w", err)
	}
	if len(rows) == 0 {
		return nil, ErrProductNotFound
	}
	return &rows[0], nil
}

func loadTariffRows(tx *gorm.DB, onlyID uint) ([]TariffRow, error) {
	var products []Product
	query := tx.Preload("Plan").Order("sort_order ASC, price_cents ASC, id ASC")
	if onlyID != 0 {
		query = query.Where("id = ?", onlyID)
	}
	if err := query.Find(&products).Error; err != nil {
		return nil, fmt.Errorf("load products: %w", err)
	}
	rows := make([]TariffRow, 0, len(products))
	if len(products) == 0 {
		return rows, nil
	}
	ids := make([]uint, len(products))
	for i := range products {
		ids[i] = products[i].ID
	}
	type count struct {
		ProductID uint
		N         int64
	}
	orders := map[uint]int64{}
	var orderCounts []count
	if err := tx.Model(&Order{}).Select("product_id, COUNT(*) AS n").Where("product_id IN ?", ids).
		Group("product_id").Scan(&orderCounts).Error; err != nil {
		return nil, fmt.Errorf("count tariff orders: %w", err)
	}
	for _, c := range orderCounts {
		orders[c.ProductID] = c.N
	}
	subs := map[uint]int64{}
	var subCounts []count
	if err := tx.Model(&Subscription{}).Select("product_id, COUNT(*) AS n").Where("product_id IN ?", ids).
		Group("product_id").Scan(&subCounts).Error; err != nil {
		return nil, fmt.Errorf("count tariff subscriptions: %w", err)
	}
	for _, c := range subCounts {
		subs[c.ProductID] = c.N
	}
	successors := map[uint]uint{}
	var links []struct {
		ID         uint
		PreviousID uint
	}
	if err := tx.Model(&Product{}).Select("id, previous_id").Where("previous_id IN ?", ids).
		Order("id ASC").Scan(&links).Error; err != nil {
		return nil, fmt.Errorf("load tariff successors: %w", err)
	}
	for _, link := range links {
		successors[link.PreviousID] = link.ID
	}
	for i := range products {
		p := products[i]
		row := TariffRow{Product: p, Orders: orders[p.ID], Subscriptions: subs[p.ID]}
		if p.Plan != nil {
			row.PlanName, row.PlanActive, row.PlanBuilderID = p.Plan.Name, p.Plan.IsActive, p.Plan.SubscriptionBuilderID
		}
		if next, ok := successors[p.ID]; ok {
			successor := next
			row.ReplacedByID = &successor
		}
		row.Product.Plan = nil
		rows = append(rows, row)
	}
	return rows, nil
}

// AdminPlanRow is a plan as offered by the tariff editor's plan picker.
type AdminPlanRow struct {
	Plan
	BuilderName   string
	Tariffs       int64
	Subscriptions int64
}

// Selectable reports whether tariffs may be sold on this plan. The trial and
// free plans are system plans, never purchasable (validatePurchaseOffer).
func (p AdminPlanRow) Selectable() bool {
	return p.Name != TrialPlanName && p.Name != FreePlanName
}

// ListAdminPlans returns every plan with its builder and usage counters.
func (s *Service) ListAdminPlans(ctx context.Context) ([]AdminPlanRow, error) {
	var rows []AdminPlanRow
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var plans []Plan
		if err := tx.Order("id ASC").Find(&plans).Error; err != nil {
			return fmt.Errorf("load plans: %w", err)
		}
		type count struct {
			ID uint
			N  int64
		}
		tariffs := map[uint]int64{}
		var tariffCounts []count
		if err := tx.Model(&Product{}).Select("plan_id AS id, COUNT(*) AS n").Group("plan_id").Scan(&tariffCounts).Error; err != nil {
			return fmt.Errorf("count plan tariffs: %w", err)
		}
		for _, c := range tariffCounts {
			tariffs[c.ID] = c.N
		}
		subs := map[uint]int64{}
		var subCounts []count
		if err := tx.Model(&Subscription{}).Select("plan_id AS id, COUNT(*) AS n").Group("plan_id").Scan(&subCounts).Error; err != nil {
			return fmt.Errorf("count plan subscriptions: %w", err)
		}
		for _, c := range subCounts {
			subs[c.ID] = c.N
		}
		names := map[uint]string{}
		var builders []SubscriptionBuilder
		if err := tx.Select("id", "name").Find(&builders).Error; err != nil {
			return fmt.Errorf("load builder names: %w", err)
		}
		for _, b := range builders {
			names[b.ID] = b.Name
		}
		rows = make([]AdminPlanRow, 0, len(plans))
		for _, plan := range plans {
			row := AdminPlanRow{Plan: plan, Tariffs: tariffs[plan.ID], Subscriptions: subs[plan.ID]}
			if plan.SubscriptionBuilderID != nil {
				row.BuilderName = names[*plan.SubscriptionBuilderID]
			}
			rows = append(rows, row)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list admin plans: %w", err)
	}
	return rows, nil
}

// ---------------------------------------------------------------------------
// Mutations
// ---------------------------------------------------------------------------

// tariffAuditView is the audit projection of a product (no internal counters).
type tariffAuditView struct {
	ID           uint     `json:"id"`
	OfferID      string   `json:"offer_id"`
	Name         string   `json:"name"`
	PlanID       uint     `json:"plan_id"`
	DurationDays int      `json:"duration_days"`
	PriceCents   int64    `json:"price_cents"`
	Currency     string   `json:"currency"`
	IsActive     bool     `json:"is_active"`
	Description  string   `json:"description"`
	Features     []string `json:"features"`
	Badge        string   `json:"badge"`
	SortOrder    int      `json:"sort_order"`
	Version      int      `json:"version"`
	PreviousID   *uint    `json:"previous_id"`
}

func tariffAudit(p *Product) tariffAuditView {
	return tariffAuditView{
		ID: p.ID, OfferID: p.OfferID, Name: p.Name, PlanID: p.PlanID, DurationDays: p.DurationDays,
		PriceCents: p.PriceCents, Currency: p.Currency, IsActive: p.IsActive, Description: p.Description,
		Features: DecodeTariffFeatures(p.Features), Badge: p.Badge, SortOrder: p.SortOrder,
		Version: p.Version, PreviousID: p.PreviousID,
	}
}

// checkTariffPlan rejects a missing plan and the system trial/free plans.
func checkTariffPlan(tx *gorm.DB, planID uint) error {
	var plan Plan
	if err := tx.Select("id", "name").First(&plan, planID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return invalidTariff("plan_id")
		}
		return fmt.Errorf("load tariff plan: %w", err)
	}
	if plan.Name == TrialPlanName || plan.Name == FreePlanName {
		return invalidTariff("plan_id")
	}
	return nil
}

// claimTariff takes SQLite's write lock on the product row before any usage
// check, serializing the check-and-update against a concurrent first order
// (same technique as UpdateProductGuarded), then loads it and verifies the
// optimistic-locking version.
func claimTariff(tx *gorm.DB, id uint, version int) (*Product, error) {
	claim := tx.Model(&Product{}).Where("id = ?", id).UpdateColumn("version", gorm.Expr("version"))
	if claim.Error != nil {
		return nil, fmt.Errorf("lock tariff: %w", claim.Error)
	}
	if claim.RowsAffected == 0 {
		return nil, ErrProductNotFound
	}
	var p Product
	if err := tx.First(&p, id).Error; err != nil {
		return nil, fmt.Errorf("load tariff: %w", err)
	}
	if p.Version != version {
		return nil, ErrTariffVersionConflict
	}
	return &p, nil
}

func tariffUsage(tx *gorm.DB, id uint) (orders, subscriptions int64, err error) {
	if err = tx.Model(&Order{}).Where("product_id = ?", id).Count(&orders).Error; err != nil {
		return 0, 0, fmt.Errorf("count tariff orders: %w", err)
	}
	if err = tx.Model(&Subscription{}).Where("product_id = ?", id).Count(&subscriptions).Error; err != nil {
		return 0, 0, fmt.Errorf("count tariff subscriptions: %w", err)
	}
	return orders, subscriptions, nil
}

func hasSuccessor(tx *gorm.DB, id uint) (bool, error) {
	var n int64
	if err := tx.Model(&Product{}).Where("previous_id = ?", id).Count(&n).Error; err != nil {
		return false, fmt.Errorf("check tariff successor: %w", err)
	}
	return n > 0, nil
}

func nextTariffSortOrder(tx *gorm.DB) (int, error) {
	var maxOrder *int
	if err := tx.Model(&Product{}).Select("MAX(sort_order)").Scan(&maxOrder).Error; err != nil {
		return 0, fmt.Errorf("read tariff order: %w", err)
	}
	if maxOrder == nil {
		return 0, nil
	}
	if *maxOrder >= TariffMaxSortOrder {
		return TariffMaxSortOrder, nil
	}
	return *maxOrder + 1, nil
}

// insertTariff inserts p with its exact field values. GORM replaces the zero
// value of a field that declares a default (Product.IsActive: default:true) by
// that default on Create, even when the column is selected, so a disabled
// tariff would be stored enabled. The flag is therefore written explicitly
// after the insert, inside the same transaction.
func insertTariff(tx *gorm.DB, p *Product) error {
	active := p.IsActive
	if err := tx.Create(p).Error; err != nil {
		return err
	}
	if !active {
		if err := tx.Model(&Product{}).Where("id = ?", p.ID).UpdateColumn("is_active", false).Error; err != nil {
			return err
		}
		p.IsActive = false
	}
	return nil
}

// CreateTariff inserts a new product. A nil SortOrder appends it to the end of
// the catalogue.
func (s *Service) CreateTariff(ctx context.Context, meta AdminConfigMeta, in TariffInput) (*AdminConfigResult, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	return s.runAdminConfigMutation(ctx, meta, AdminActionTariffCreated, AdminTargetTariff, 0, in,
		func(tx *gorm.DB, _ time.Time) (configChange, error) {
			if err := checkTariffPlan(tx, in.PlanID); err != nil {
				return configChange{}, err
			}
			order := 0
			if in.SortOrder != nil {
				order = *in.SortOrder
			} else {
				var err error
				if order, err = nextTariffSortOrder(tx); err != nil {
					return configChange{}, err
				}
			}
			p := Product{
				PlanID: in.PlanID, Name: in.Name, DurationDays: in.DurationDays, PriceCents: in.PriceCents,
				Currency: in.Currency, IsActive: in.IsActive, Description: in.Description,
				Features: EncodeTariffFeatures(in.Features), Badge: in.Badge, SortOrder: order, Version: 1,
			}
			if err := insertTariff(tx, &p); err != nil {
				return configChange{}, fmt.Errorf("create tariff: %w", err)
			}
			return configChange{targetID: p.ID, newValue: tariffAudit(&p)}, nil
		})
}

// TariffUpdateInput replaces the editable content of a tariff. Version must
// match the stored row (ErrTariffVersionConflict otherwise).
type TariffUpdateInput struct {
	ID      uint `json:"id"`
	Version int  `json:"version"`
	TariffInput
}

// UpdateTariff edits a tariff. When the product is referenced by orders or
// subscriptions and the purchase terms change, a successor product is created
// (the result's TargetID is the successor) and the original is retired:
// is_active=false and version+1, its terms untouched. A retired version cannot
// be edited again (ErrTariffSuperseded): edit its successor instead.
func (s *Service) UpdateTariff(ctx context.Context, meta AdminConfigMeta, in TariffUpdateInput) (*AdminConfigResult, error) {
	if in.ID == 0 || in.Version <= 0 {
		return nil, fmt.Errorf("%w: id/version", ErrAdminInvalidRequest)
	}
	if err := in.Validate(); err != nil {
		return nil, err
	}
	return s.runAdminConfigMutation(ctx, meta, AdminActionTariffUpdated, AdminTargetTariff, in.ID, in,
		func(tx *gorm.DB, now time.Time) (configChange, error) {
			current, err := claimTariff(tx, in.ID, in.Version)
			if err != nil {
				return configChange{}, err
			}
			if superseded, err := hasSuccessor(tx, current.ID); err != nil {
				return configChange{}, err
			} else if superseded {
				return configChange{}, ErrTariffSuperseded
			}
			if err := checkTariffPlan(tx, in.PlanID); err != nil {
				return configChange{}, err
			}
			orders, subs, err := tariffUsage(tx, current.ID)
			if err != nil {
				return configChange{}, err
			}
			old := tariffAudit(current)
			order := current.SortOrder
			if in.SortOrder != nil {
				order = *in.SortOrder
			}

			if (orders > 0 || subs > 0) && !in.sameTerms(current) {
				// Retire the original (terms untouched), then create the successor
				// at the same catalogue position with the requested content.
				retire := tx.Model(&Product{}).Where("id = ? AND version = ?", current.ID, current.Version).
					Updates(map[string]any{"is_active": false, "version": current.Version + 1, "updated_at": now})
				if retire.Error != nil {
					return configChange{}, fmt.Errorf("retire tariff: %w", retire.Error)
				}
				if retire.RowsAffected == 0 {
					return configChange{}, ErrTariffVersionConflict
				}
				previous := current.ID
				next := Product{
					PlanID: in.PlanID, Name: in.Name, DurationDays: in.DurationDays, PriceCents: in.PriceCents,
					Currency: in.Currency, IsActive: in.IsActive, Description: in.Description,
					Features: EncodeTariffFeatures(in.Features), Badge: in.Badge, SortOrder: order, Version: 1,
					OfferEndsAt: current.OfferEndsAt, PreviousID: &previous,
				}
				if err := insertTariff(tx, &next); err != nil {
					return configChange{}, fmt.Errorf("create tariff version: %w", err)
				}
				retired := *current
				retired.IsActive, retired.Version = false, current.Version+1
				return configChange{
					targetID: next.ID,
					oldValue: old,
					newValue: map[string]any{"versioned": true, "retired": tariffAudit(&retired), "successor": tariffAudit(&next)},
				}, nil
			}

			update := tx.Model(&Product{}).Where("id = ? AND version = ?", current.ID, current.Version).Updates(map[string]any{
				"name": in.Name, "plan_id": in.PlanID, "duration_days": in.DurationDays, "price_cents": in.PriceCents,
				"currency": in.Currency, "is_active": in.IsActive, "description": in.Description,
				"features": EncodeTariffFeatures(in.Features), "badge": in.Badge, "sort_order": order,
				"version": current.Version + 1, "updated_at": now,
			})
			if update.Error != nil {
				return configChange{}, fmt.Errorf("update tariff: %w", update.Error)
			}
			if update.RowsAffected == 0 {
				return configChange{}, ErrTariffVersionConflict
			}
			var saved Product
			if err := tx.First(&saved, current.ID).Error; err != nil {
				return configChange{}, fmt.Errorf("reload tariff: %w", err)
			}
			return configChange{targetID: saved.ID, oldValue: old, newValue: tariffAudit(&saved)}, nil
		})
}

// SetTariffActive enables or disables a tariff. A retired version (one with a
// successor) cannot be enabled again: it would sell the old terms next to the
// new ones.
func (s *Service) SetTariffActive(ctx context.Context, meta AdminConfigMeta, id uint, version int, active bool) (*AdminConfigResult, error) {
	if id == 0 || version <= 0 {
		return nil, fmt.Errorf("%w: id/version", ErrAdminInvalidRequest)
	}
	action := AdminActionTariffDisabled
	if active {
		action = AdminActionTariffEnabled
	}
	payload := struct {
		ID      uint `json:"id"`
		Version int  `json:"version"`
		Active  bool `json:"active"`
	}{id, version, active}
	return s.runAdminConfigMutation(ctx, meta, action, AdminTargetTariff, id, payload,
		func(tx *gorm.DB, now time.Time) (configChange, error) {
			current, err := claimTariff(tx, id, version)
			if err != nil {
				return configChange{}, err
			}
			if active {
				if superseded, err := hasSuccessor(tx, id); err != nil {
					return configChange{}, err
				} else if superseded {
					return configChange{}, ErrTariffSuperseded
				}
				if err := checkTariffPlan(tx, current.PlanID); err != nil {
					return configChange{}, err
				}
			}
			old := tariffAudit(current)
			update := tx.Model(&Product{}).Where("id = ? AND version = ?", id, current.Version).
				Updates(map[string]any{"is_active": active, "version": current.Version + 1, "updated_at": now})
			if update.Error != nil {
				return configChange{}, fmt.Errorf("set tariff active: %w", update.Error)
			}
			if update.RowsAffected == 0 {
				return configChange{}, ErrTariffVersionConflict
			}
			current.IsActive, current.Version = active, current.Version+1
			return configChange{targetID: id, oldValue: old, newValue: tariffAudit(current)}, nil
		})
}

// ReorderTariffs assigns sort_order = position in ids. ids must list every
// product exactly once; a list built from a stale catalogue (a tariff created
// or deleted meanwhile) is ErrTariffOrderStale. Rows whose position changes get
// version+1, so an editor opened before the reorder cannot overwrite it.
func (s *Service) ReorderTariffs(ctx context.Context, meta AdminConfigMeta, ids []uint) (*AdminConfigResult, error) {
	if len(ids) == 0 || len(ids) > 10_000 {
		return nil, fmt.Errorf("%w: ids", ErrAdminInvalidRequest)
	}
	seen := make(map[uint]struct{}, len(ids))
	for _, id := range ids {
		if _, dup := seen[id]; dup || id == 0 {
			return nil, fmt.Errorf("%w: ids", ErrAdminInvalidRequest)
		}
		seen[id] = struct{}{}
	}
	payload := struct {
		IDs []uint `json:"ids"`
	}{ids}
	return s.runAdminConfigMutation(ctx, meta, AdminActionTariffsReordered, AdminTargetTariff, 0, payload,
		func(tx *gorm.DB, now time.Time) (configChange, error) {
			// Take the write lock first so the catalogue cannot change between
			// the completeness check and the update.
			if err := tx.Model(&Product{}).Where("1 = 0").UpdateColumn("version", gorm.Expr("version")).Error; err != nil {
				return configChange{}, fmt.Errorf("lock tariffs: %w", err)
			}
			var current []Product
			if err := tx.Select("id", "sort_order", "version").Order("sort_order ASC, price_cents ASC, id ASC").Find(&current).Error; err != nil {
				return configChange{}, fmt.Errorf("load tariffs: %w", err)
			}
			if len(current) != len(ids) {
				return configChange{}, ErrTariffOrderStale
			}
			before := make([]uint, 0, len(current))
			positions := make(map[uint]int, len(current))
			for _, p := range current {
				if _, ok := seen[p.ID]; !ok {
					return configChange{}, ErrTariffOrderStale
				}
				before = append(before, p.ID)
				positions[p.ID] = p.SortOrder
			}
			for position, id := range ids {
				if positions[id] == position {
					continue
				}
				res := tx.Model(&Product{}).Where("id = ?", id).
					Updates(map[string]any{"sort_order": position, "version": gorm.Expr("version + 1"), "updated_at": now})
				if res.Error != nil {
					return configChange{}, fmt.Errorf("reorder tariff %d: %w", id, res.Error)
				}
			}
			return configChange{oldValue: map[string]any{"ids": before}, newValue: map[string]any{"ids": ids}}, nil
		})
}

// DeleteTariff physically deletes an unused product. Products referenced by
// any order or subscription are protected (ErrTariffInUse): disable them
// instead. Deleting a successor never touches its retired predecessor.
func (s *Service) DeleteTariff(ctx context.Context, meta AdminConfigMeta, id uint, version int) (*AdminConfigResult, error) {
	if id == 0 || version <= 0 {
		return nil, fmt.Errorf("%w: id/version", ErrAdminInvalidRequest)
	}
	payload := struct {
		ID      uint `json:"id"`
		Version int  `json:"version"`
	}{id, version}
	return s.runAdminConfigMutation(ctx, meta, AdminActionTariffDeleted, AdminTargetTariff, id, payload,
		func(tx *gorm.DB, _ time.Time) (configChange, error) {
			current, err := claimTariff(tx, id, version)
			if err != nil {
				return configChange{}, err
			}
			orders, subs, err := tariffUsage(tx, id)
			if err != nil {
				return configChange{}, err
			}
			if orders > 0 || subs > 0 {
				return configChange{}, ErrTariffInUse
			}
			old := tariffAudit(current)
			// A retired predecessor of this row stays retired: its successor link
			// simply disappears. Rows that named this one as predecessor are
			// detached (defensive: an in-use row is never deleted).
			if err := tx.Model(&Product{}).Where("previous_id = ?", id).Update("previous_id", nil).Error; err != nil {
				return configChange{}, fmt.Errorf("detach tariff successors: %w", err)
			}
			res := tx.Delete(&Product{}, id)
			if res.Error != nil {
				return configChange{}, fmt.Errorf("delete tariff: %w", res.Error)
			}
			if res.RowsAffected == 0 {
				return configChange{}, ErrProductNotFound
			}
			return configChange{targetID: id, oldValue: old, newValue: nil}, nil
		})
}
